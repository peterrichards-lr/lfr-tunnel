package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// #2347: since #2342 the client treats a registration 401 as final, so a token lookup that FAILED
// -- SQLITE_BUSY, an I/O error -- must not be answered as one. Every FIRING case here goes red
// against the code before it, which answered all of these 401.

// failingPATLookup is the real token repository with its hash lookup failing the way a busy or
// broken database does: with an error that is not ErrNotFound.
type failingPATLookup struct{ db.PATRepository }

func (failingPATLookup) GetPATByHash(string) (*db.PersonalAccessToken, error) {
	return nil, errors.New("database is locked (5) (SQLITE_BUSY)")
}

// failingUserLookup fails only the SECOND read, after the token row has been found.
type failingUserLookup struct{ db.UserRepository }

func (failingUserLookup) GetUser(string) (*db.User, error) {
	return nil, errors.New("disk I/O error")
}

func seedApprovedTokenHolder(t *testing.T, srv *Server, raw string) {
	t.Helper()
	if err := srv.db.CreateUser(&db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "user", Status: db.UserStatusApproved}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(raw))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "dev@example.com", TokenHash: hex.EncodeToString(sum[:]), TokenPrefix: raw[:6], Name: "t"}); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnavailableTokenStoreIsNotARefusal(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	srv.cfg.AllowClientAutoReservation = true
	seedApprovedTokenHolder(t, srv, "lft_pat_real_token")
	srv.db.PATRepository = failingPATLookup{srv.db.PATRepository}

	rec, resp := registerTunnelAsVersion(t, srv, "lft_pat_real_token", "busy-sub", "v9.9.9")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a failed token lookup must be 503, which the client retries -- got %d %q, and a 401 is final", rec.Code, resp.Error)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("the 503 should say when to retry")
	}
	if resp.Error == gatewayTokenRefusalForTest {
		t.Errorf("the 503 reused the token refusal's text %q", resp.Error)
	}
}

// gatewayTokenRefusalForTest is the client's gatewayTokenRefusal, restated because the client is
// another package. #2342's TestGatewayTokenRefusalMatchesTheServer holds the two in step.
const gatewayTokenRefusalForTest = "unauthorized"

// BOUNDING, and the reason the 503 is safe: with the store failing, the answer is the same for a
// token that exists and one that does not. A difference would make the 503 an existence oracle.
func TestAnUnavailableTokenStoreAnswersTheSameForAnyToken(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	srv.cfg.AllowClientAutoReservation = true
	seedApprovedTokenHolder(t, srv, "lft_pat_real_token")
	srv.db.PATRepository = failingPATLookup{srv.db.PATRepository}

	recReal, real := registerTunnelAsVersion(t, srv, "lft_pat_real_token", "sub-a", "v9.9.9")
	recFake, fake := registerTunnelAsVersion(t, srv, "lft_pat_never_issued", "sub-b", "v9.9.9")
	if recReal.Code != recFake.Code || real.Error != fake.Error {
		t.Errorf("the answer depends on the token: existing -> %d %q, never issued -> %d %q",
			recReal.Code, real.Error, recFake.Code, fake.Error)
	}
}

// Only the FIRST lookup may be reported as unavailable. Once the token row is found, a failure is
// a refusal -- answering it differently would be an answer only ever given for a real token.
func TestAFailureAfterTheTokenIsFoundIsStillARefusal(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	seedApprovedTokenHolder(t, srv, "lft_pat_real_token")
	srv.db.UserRepository = failingUserLookup{srv.db.UserRepository}

	rec, resp := registerTunnelAsVersion(t, srv, "lft_pat_real_token", "sub-c", "v9.9.9")
	if rec.Code != http.StatusUnauthorized || resp.Error != gatewayTokenRefusalForTest {
		t.Errorf("a failure after the token row was found must stay the uniform 401; got %d %q", rec.Code, resp.Error)
	}
}

// The control: a healthy store still refuses a bad token with the uniform 401, and an empty token
// is refused before the store is consulted at all.
func TestARefusedTokenIsStillA401(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	if rec, resp := registerTunnelAsVersion(t, srv, "lft_pat_never_issued", "sub-d", "v9.9.9"); rec.Code != http.StatusUnauthorized || resp.Error != gatewayTokenRefusalForTest {
		t.Errorf("an unknown token on a healthy store: got %d %q, want 401 %q", rec.Code, resp.Error, gatewayTokenRefusalForTest)
	}

	srv.db.PATRepository = failingPATLookup{srv.db.PATRepository}
	if rec, _ := registerTunnelAsVersion(t, srv, "", "sub-e", "v9.9.9"); rec.Code != http.StatusUnauthorized {
		t.Errorf("an empty token must be refused without a lookup, store or no store; got %d", rec.Code)
	}
}

// Most clients register through an edge, which relays central's answer. Central failing its token
// lookup must reach the client as 503 through a real edge, not only on the direct path.
func TestAnUnavailableTokenStoreReachesAnEdgeClientAs503(t *testing.T) {
	tmp := t.TempDir()
	edgeSecret := "usedge-busy-secret"
	sum := sha256.Sum256([]byte(edgeSecret))

	controlCfg := config.DefaultServerConfig()
	controlCfg.DBPath = filepath.Join(tmp, "control.db")
	controlCfg.Domains = []string{"example.se"}
	controlCfg.DisableBackupScheduler = true
	controlCfg.AllowClientAutoReservation = true
	controlCfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "usedge", TokenHash: hex.EncodeToString(sum[:])}}
	control, err := NewServer(controlCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { time.Sleep(50 * time.Millisecond); control.Stop() }()
	seedApprovedTokenHolder(t, control, "lft_pat_edge_token")
	control.db.PATRepository = failingPATLookup{control.db.PATRepository}
	ts := httptest.NewServer(control)
	defer ts.Close()

	edgeCfg := config.DefaultServerConfig()
	edgeCfg.DBPath = ""
	edgeCfg.Domains = []string{"usedge.example.se"}
	edgeCfg.ControlPlaneURL = ts.URL
	edgeCfg.EdgeToken = edgeSecret
	edgeCfg.DisableBackupScheduler = true
	edge, err := NewServer(edgeCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { time.Sleep(50 * time.Millisecond); edge.Stop() }()

	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: "edge-busy", AuthToken: "lft_pat_edge_token",
		Ports: []PortMapping{{LocalPort: 8080}}, ClientVersion: "v9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://usedge.example.se/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	edge.handleRegister(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("central's failed token lookup reached the edge client as %d, not 503: %s", rec.Code, rec.Body.String())
	}
}
