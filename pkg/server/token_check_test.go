package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const tokenCheckPath = "/api/client/token-check"

// tokenCheck drives the real router, so every case also proves the route is reachable.
func tokenCheck(t *testing.T, srv *Server, header, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com"+tokenCheckPath+query, nil)
	if header != "" {
		req.Header.Set("X-Auth-Token", header)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// #2458: login asks this before reporting success, so each verdict must be distinct and exact.
func TestClientTokenCheckAnswersEachVerdict(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	const good = "0123456789abcdef0123456789abcdef"
	seedApprovedTokenHolder(t, srv, good)

	if w := tokenCheck(t, srv, good, ""); w.Code != http.StatusNoContent {
		t.Errorf("a valid token: %d %s, want 204", w.Code, w.Body.String())
	}
	for name, tok := range map[string]string{"unknown token": strings.Repeat("e", 32), "no token": ""} {
		w := tokenCheck(t, srv, tok, "")
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %q, want a JSON 401", name, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// Header only: a token in the URL would be written to every access log on the way.
	if w := tokenCheck(t, srv, "", "?token="+good); w.Code != http.StatusUnauthorized {
		t.Errorf("a token in the query string was honoured: %d", w.Code)
	}
	if got := tokenCheck(t, srv, good, "").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	// A check is not a use: last-used must not move. touchPAT is asynchronous, so watch for it
	// for a while rather than reading once -- a single read would race past a mutant that adds
	// it. (Not bgWG.Wait(): the server's long-lived consumers are tracked too, and it never
	// returns.)
	sum := sha256.Sum256([]byte(good))
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		pat, err := srv.db.GetPATByHash(hex.EncodeToString(sum[:]))
		if err != nil {
			t.Fatalf("reading the PAT: %v", err)
		}
		if pat.LastUsedAt != nil {
			t.Fatalf("checking the token recorded it as used at %v", pat.LastUsedAt)
		}
	}
}

func TestClientTokenCheckRefusesARevokedToken(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	const tok = "abcdefabcdefabcdefabcdefabcdefab"
	seedApprovedTokenHolder(t, srv, tok)
	sum := sha256.Sum256([]byte(tok))
	pat, err := srv.db.GetPATByHash(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("reading the PAT: %v", err)
	}
	if err := srv.db.RevokePAT(pat.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if w := tokenCheck(t, srv, tok, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token: %d, want 401", w.Code)
	}
}

// Unknown is not absent: a store that cannot be read says nothing about the token, so it must
// not be answered 401, which the client treats as final.
func TestClientTokenCheckReportsAnUnreadableStoreAs503(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	srv.db.PATRepository = failingPATLookup{srv.db.PATRepository}
	w := tokenCheck(t, srv, "0123456789abcdef0123456789abcdef", "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), tokenStoreUnavailableMessage) {
		t.Errorf("unreadable store: %d %s, want 503 with the store message", w.Code, w.Body.String())
	}
}

// An edge holds no tokens. Checking there would refuse every token unread, so it must say it
// cannot answer instead.
func TestClientTokenCheckOnAnEdgeIsMisdirectedNotRefused(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	originalDB := srv.db
	srv.db = nil
	defer func() { srv.db = originalDB }()

	w := tokenCheck(t, srv, "0123456789abcdef0123456789abcdef", "")
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("on an edge: %d %s, want 421", w.Code, w.Body.String())
	}
}
