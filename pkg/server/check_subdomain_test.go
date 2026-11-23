package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// /api/check-subdomain must answer the question registration answers (#2562). Each case below
// is a name the endpoint used to call "available" while registration would refuse it, or a
// domain it used to answer about without being asked.

const checkSubdomainTestToken = "lfr_pat_check_subdomain_secret"

func newCheckSubdomainServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(&config.ServerConfig{
		Domains:                []string{"example.se", "example.online"},
		DisableBackupScheduler: true,
		DBPath:                 filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	})
	for _, id := range []string{"asker@example.com", "other@example.com"} {
		if err := srv.db.CreateUser(&db.User{ID: id, Email: id, Role: "user", Status: "approved"}); err != nil {
			t.Fatalf("CreateUser %s: %v", id, err)
		}
	}
	hash := sha256.Sum256([]byte(checkSubdomainTestToken))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "asker@example.com", TokenHash: hex.EncodeToString(hash[:]), TokenPrefix: "lfr_pat_chec"}); err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}
	return srv
}

func checkSubdomainVia(t *testing.T, srv *Server, query string) (int, CheckSubdomainResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.se/api/check-subdomain?"+query, nil)
	req.Host = "tunnel.example.se"
	req.Header.Set("Authorization", "Bearer "+checkSubdomainTestToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var resp CheckSubdomainResponse
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, resp
}

func addEdgeLease(srv *Server, prefix, fullHost string) {
	srv.edgeLeasesMu.Lock()
	defer srv.edgeLeasesMu.Unlock()
	if srv.edgeLeases == nil {
		srv.edgeLeases = map[string][]EdgeLease{}
	}
	srv.edgeLeases["edge-us"] = append(srv.edgeLeases["edge-us"], EdgeLease{
		NodeID: "edge-us", Subdomain: prefix, UserID: "other@example.com", FullHost: fullHost,
	})
}

func TestCheckSubdomain_NameHeldOnAnEdgeIsTaken(t *testing.T) {
	srv := newCheckSubdomainServer(t)
	addEdgeLease(srv, "acme-poc", "acme-poc.example.se")

	code, resp := checkSubdomainVia(t, srv, "subdomain=acme-poc&domain=example.se")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if resp.Available {
		t.Fatalf("acme-poc.example.se is held by an edge tunnel but was reported available: %+v", resp)
	}
}

func TestCheckSubdomain_ReservationRules(t *testing.T) {
	srv := newCheckSubdomainServer(t)
	past := time.Now().Add(-24 * time.Hour)
	for _, r := range []*db.SubdomainReservation{
		{UserID: "other@example.com", Subdomain: "theirs", Domain: "example.se"},
		{UserID: "asker@example.com", Subdomain: "mine", Domain: "example.se"},
		// Expired yesterday: inside the default quarantine window, so still the holder's.
		{UserID: "other@example.com", Subdomain: "quarantined", Domain: "example.se", ExpiresAt: &past},
	} {
		if err := srv.db.CreateSubdomainReservation(r); err != nil {
			t.Fatalf("CreateSubdomainReservation: %v", err)
		}
	}
	srv.cfg.SubdomainQuarantineDays = 7

	cases := []struct {
		name       string
		wantAvail  bool
		wantReason string
	}{
		{"theirs", false, subdomainReservedByOther},
		{"mine", true, ""},
		{"quarantined", false, subdomainQuarantinedMessage},
	}
	for _, tc := range cases {
		code, resp := checkSubdomainVia(t, srv, "subdomain="+tc.name+"&domain=example.se")
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.name, code)
		}
		if resp.Available != tc.wantAvail || resp.Reason != tc.wantReason {
			t.Errorf("%s: got available=%v reason=%q, want available=%v reason=%q",
				tc.name, resp.Available, resp.Reason, tc.wantAvail, tc.wantReason)
		}
	}
}

func TestCheckSubdomain_DomainParameterIsHonouredAndEchoed(t *testing.T) {
	srv := newCheckSubdomainServer(t)
	addEdgeLease(srv, "acme-poc", "acme-poc.example.se")

	// The request's Host is tunnel.example.se, which the contextual rule maps to example.se.
	// Asking about example.online must answer about example.online, and say so.
	code, resp := checkSubdomainVia(t, srv, "subdomain=acme-poc&domain=example.online")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !resp.Available || resp.Domain != "example.online" {
		t.Errorf("domain=example.online: got available=%v domain=%q, want true, example.online", resp.Available, resp.Domain)
	}

	code, resp = checkSubdomainVia(t, srv, "subdomain=acme-poc&domain=example.se")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if resp.Available || resp.Domain != "example.se" {
		t.Errorf("domain=example.se: got available=%v domain=%q, want false, example.se", resp.Available, resp.Domain)
	}

	code, _ = checkSubdomainVia(t, srv, "subdomain=acme-poc&domain=example.invalid")
	if code != http.StatusBadRequest {
		t.Errorf("a domain this gateway does not issue on: status %d, want 400", code)
	}
}

func TestCheckSubdomain_SuggestionsSkipNamesHeldElsewhere(t *testing.T) {
	srv := newCheckSubdomainServer(t)
	// "admin" is a reserved word, so it is refused and suggestions are generated from it. The
	// first candidate GenerateSuggestions tries is admin-dev; hold it on an edge.
	addEdgeLease(srv, "admin-dev", "admin-dev.example.se")

	code, resp := checkSubdomainVia(t, srv, "subdomain=admin&domain=example.se")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if resp.Available {
		t.Fatalf("admin must be unavailable")
	}
	if len(resp.Suggestions) == 0 {
		t.Fatalf("expected suggestions, got none")
	}
	for _, s := range resp.Suggestions {
		if s == "admin-dev" {
			t.Errorf("suggested admin-dev, which an edge tunnel holds: %v", resp.Suggestions)
		}
	}
}

func TestCheckSubdomain_AnEdgeSaysItCannotAnswer(t *testing.T) {
	srv := newCheckSubdomainServer(t)
	saved := srv.db
	srv.db = nil // what an edge is, for this purpose: holdsNoControlPlaneData
	defer func() { srv.db = saved }()

	code, _ := checkSubdomainVia(t, srv, "subdomain=acme-poc")
	if code != http.StatusMisdirectedRequest {
		t.Errorf("status %d, want 421: an edge never examined the token, so 401 would blame it", code)
	}
}
