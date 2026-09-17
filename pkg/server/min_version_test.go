package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// registerTunnelAsVersion is registerTunnel with the client naming a version, which is the
// only input the floor is evaluated against.
func registerTunnelAsVersion(t *testing.T, srv *Server, token, subdomain, clientVersion string) (*httptest.ResponseRecorder, RegisterResponse) {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		AuthToken:       token,
		Ports:           []PortMapping{{LocalPort: 8080}},
		ClientVersion:   clientVersion,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.handleRegister(rec, req)

	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the register response: %v", err)
	}
	return rec, resp
}

// minVersionFirstSeenDaysAgo backdates when this gateway first saw the user below the floor.
// Keyed on the floor string, exactly as production is, so a test that backdated the wrong key
// would move nobody between phases.
func minVersionFirstSeenDaysAgo(t *testing.T, srv *Server, userID string, days int) {
	t.Helper()
	when := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	if _, err := srv.db.RecordFirstSeen(userID, MinVersionDocumentID, srv.cfg.MinClientVersion, when); err != nil {
		t.Fatalf("backdating first sight below the floor: %v", err)
	}
}

// The default floor is v1.0.0, i.e. effectively disabled, so nothing changes for a
// deployment that has not raised it. This is the upgrade path, and it is the state the whole
// fleet is in today.
func TestMinVersionInertAtTheDefaultFloor(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()

	user := seedConsentUser(t, srv, "inert-ver@example.com", "tok-inert-ver")
	if state := srv.minVersionState(user, "v1.40.0", true); state.Required {
		t.Errorf("a v1.40.0 client reported as below the default v1.0.0 floor: %+v", state)
	}
	rec, _ := registerTunnelAsVersion(t, srv, "tok-inert-ver", "inert-ver-sub", "v1.40.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected registration to succeed at the default floor, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A client below the floor but inside its window keeps working, and is told how long it has.
func TestMinVersionWithinGraceAllowsNewTunnels(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"

	user := seedConsentUser(t, srv, "grace-ver@example.com", "tok-grace-ver")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 2)

	rec, resp := registerTunnelAsVersion(t, srv, "tok-grace-ver", "grace-ver-sub", "v1.40.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 within the grace window, got %d: %s", rec.Code, rec.Body.String())
	}
	if resp.MinVersion == nil || !resp.MinVersion.Required {
		t.Fatal("expected the min_version state on the registration response")
	}
	if resp.MinVersion.Phase != GracePhaseGrace {
		t.Errorf("phase = %q, want %q", resp.MinVersion.Phase, GracePhaseGrace)
	}
	// Silent during grace, for the same reason consent is.
	if notice := MinVersionNoticeText(resp.MinVersion); notice != "" {
		t.Errorf("expected no client warning during grace, got %q", notice)
	}
}

// The warning window is the whole point of the grace model: the client has to SAY something
// while there is still time to act on it.
func TestMinVersionWarningWindowWarnsButAllows(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"

	user := seedConsentUser(t, srv, "warn-ver@example.com", "tok-warn-ver")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 11)

	rec, resp := registerTunnelAsVersion(t, srv, "tok-warn-ver", "warn-ver-sub", "v1.40.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 inside the warning window, got %d: %s", rec.Code, rec.Body.String())
	}
	if resp.MinVersion == nil || resp.MinVersion.Phase != GracePhaseWarning {
		t.Fatalf("phase = %+v, want %q", resp.MinVersion, GracePhaseWarning)
	}

	notice := MinVersionNoticeText(resp.MinVersion)
	if notice == "" {
		t.Fatal("no client warning inside the warning window -- the deadline would arrive unannounced")
	}
	// Assert the CAUSE, not merely that something was printed: a message that does not name
	// the remedy is the defect #1948 reports, and it would satisfy a non-empty check.
	for _, want := range []string{"v1.40.0", "v1.45.0", "lfr-tunnel -upgrade"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the warning does not name %q: %s", want, notice)
		}
	}
	if resp.MinVersion.SecondsRemaining <= 0 {
		t.Errorf("seconds_remaining = %d, want a positive countdown inside the window", resp.MinVersion.SecondsRemaining)
	}
}

// The enforcement half: server-side, so it reaches a client that would not refuse itself.
func TestMinVersionExpiredRefusesNewTunnelsServerSide(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"

	user := seedConsentUser(t, srv, "expired-ver@example.com", "tok-expired-ver")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 30)

	rec, resp := registerTunnelAsVersion(t, srv, "tok-expired-ver", "expired-ver-sub", "v1.40.0")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 once the upgrade period has ended, got %d: %s", rec.Code, rec.Body.String())
	}
	// Assert the message, not the status: the reservation gate, the quota gate and the
	// consent gate all answer 403 here, so a status-only assertion is satisfied by three
	// unrelated causes.
	for _, want := range []string{"v1.40.0", "v1.45.0", "lfr-tunnel -upgrade"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("the refusal does not name %q: %s", want, resp.Error)
		}
	}
	if resp.MinVersion == nil || resp.MinVersion.Phase != GracePhaseExpired {
		t.Fatalf("expected the expired min_version block on the refusal, got %+v", resp.MinVersion)
	}
	if resp.MinVersion.UpgradeCommand != UpgradeCommand {
		t.Errorf("upgrade_command = %q, want %q", resp.MinVersion.UpgradeCommand, UpgradeCommand)
	}

	// The control that makes the refusal mean something: an up-to-date client on the same
	// gateway, with the same backdated row, still registers.
	current := seedConsentUser(t, srv, "current-ver@example.com", "tok-current-ver")
	minVersionFirstSeenDaysAgo(t, srv, current.ID, 30)
	recOK, _ := registerTunnelAsVersion(t, srv, "tok-current-ver", "current-ver-sub", "v1.48.0")
	if recOK.Code != http.StatusOK {
		t.Fatalf("a client at the floor was refused too -- the gate is not reading the version: %d %s", recOK.Code, recOK.Body.String())
	}
}

// The two deadlines are separate obligations with separate remedies (#1948). A user can be
// outside one and inside the other, and the message they get must name the one that actually
// stopped them -- sending somebody to the portal to fix a version, or to `-upgrade` to fix
// consent, is worse than no message.
func TestVersionAndConsentDeadlinesAreIndependent(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"
	srv.cfg.PolicyVersion = "2"
	srv.cfg.PortalURL = "https://portal.example.com"

	// Expired on version, fresh on consent.
	verUser := seedConsentUser(t, srv, "ver-only@example.com", "tok-ver-only")
	minVersionFirstSeenDaysAgo(t, srv, verUser.ID, 30)
	firstSeenDaysAgo(t, srv, verUser.ID, 1)

	rec, resp := registerTunnelAsVersion(t, srv, "tok-ver-only", "ver-only-sub", "v1.40.0")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the expired version, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(resp.Error, "lfr-tunnel -upgrade") {
		t.Errorf("a version refusal must name the upgrade command, got: %s", resp.Error)
	}
	if strings.Contains(resp.Error, "Privacy Policy") {
		t.Errorf("a version refusal must not be worded as a consent refusal: %s", resp.Error)
	}

	// Expired on consent, current client.
	conUser := seedConsentUser(t, srv, "con-only@example.com", "tok-con-only")
	firstSeenDaysAgo(t, srv, conUser.ID, 30)

	rec2, resp2 := registerTunnelAsVersion(t, srv, "tok-con-only", "con-only-sub", "v1.48.0")
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the expired consent, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(resp2.Error, "Privacy Policy") {
		t.Errorf("a consent refusal must still be worded as one, got: %s", resp2.Error)
	}
	if strings.Contains(resp2.Error, "lfr-tunnel -upgrade") {
		t.Errorf("a consent refusal must not send the user to the upgrade command: %s", resp2.Error)
	}
}

// Raising the floor again starts a fresh window rather than inheriting the previous
// rollout's expired one. Without this, one bump would leave every later bump instantaneous.
func TestRaisingTheFloorStartsANewWindow(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"

	user := seedConsentUser(t, srv, "rollout@example.com", "tok-rollout")
	minVersionFirstSeenDaysAgo(t, srv, user.ID, 30)
	if state := srv.minVersionState(user, "v1.40.0", false); state.Phase != GracePhaseExpired {
		t.Fatalf("phase = %q, want %q before the floor moves", state.Phase, GracePhaseExpired)
	}

	srv.cfg.MinClientVersion = "v1.46.0"
	state := srv.minVersionState(user, "v1.40.0", true)
	if state.Phase != GracePhaseGrace {
		t.Errorf("phase = %q after raising the floor, want a fresh %q window", state.Phase, GracePhaseGrace)
	}
}

// A development build must not be locked out by a floor it orders below by construction --
// the client exempts itself the same way, and enforcing it here would stop anyone building
// from source.
func TestMinVersionExemptsDevAndUnknownBuilds(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.MinClientVersion = "v1.45.0"

	user := seedConsentUser(t, srv, "dev-build@example.com", "tok-dev-build")
	for _, version := range []string{"dev", ""} {
		if state := srv.minVersionState(user, version, true); state.Required {
			t.Errorf("client version %q was judged against the floor: %+v", version, state)
		}
	}
}

// The windows are configured per rollout, and the warning may not start before the window it
// warns about.
func TestMinVersionWindowsAreConfigurableAndClamped(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()

	srv.cfg.MinClientVersionGraceDays = 60
	srv.cfg.MinClientVersionWarningDays = 14
	if got := srv.minVersionGraceDays(); got != 60 {
		t.Errorf("grace days = %d, want the configured 60", got)
	}
	if got := srv.minVersionWarningDays(); got != 14 {
		t.Errorf("warning days = %d, want the configured 14", got)
	}

	srv.cfg.MinClientVersionGraceDays = 3
	srv.cfg.MinClientVersionWarningDays = 10
	if got := srv.minVersionWarningDays(); got != 3 {
		t.Errorf("warning days = %d, want it clamped to the 3-day grace window", got)
	}

	srv.cfg.MinClientVersionGraceDays = 0
	srv.cfg.MinClientVersionWarningDays = 0
	if got := srv.minVersionGraceDays(); got != 14 {
		t.Errorf("grace days = %d, want the 14-day default when unset", got)
	}
	if got := srv.minVersionWarningDays(); got != 5 {
		t.Errorf("warning days = %d, want the 5-day default when unset", got)
	}
}

// /api/version has to say that this gateway enforces the floor itself, because that is what
// tells a client it can defer to registration instead of killing itself first. Without the
// flag the grace window is unreachable for every client new enough to look at it.
func TestAPIVersionAdvertisesServerSideEnforcement(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version = %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}
	enforced, ok := body["min_version_server_enforced"].(bool)
	if !ok {
		t.Fatalf("min_version_server_enforced missing from /api/version: %s", rec.Body.String())
	}
	if !enforced {
		t.Error("min_version_server_enforced = false -- a client would keep hard-stopping and never reach its grace window")
	}
}

// The edge path is most of the fleet. An edge holds no database, so the control plane has to
// decide and the edge has to relay -- enforcing only on the direct path would leave every
// edge region accepting clients the floor refuses.
func TestEdgeRegisterEnforcesMinVersion(t *testing.T) {
	tmpDir := t.TempDir()

	hash := sha256.Sum256([]byte("my-edge-secret"))
	hashStr := hex.EncodeToString(hash[:])

	controlCfg := config.DefaultServerConfig()
	controlCfg.DBPath = filepath.Join(tmpDir, "control.db")
	controlCfg.Domains = []string{"control.lfr-demo.se"}
	controlCfg.DisableBackupScheduler = true
	controlCfg.AllowClientAutoReservation = true
	controlCfg.MinClientVersion = "v1.45.0"
	controlCfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "us-edge", TokenHash: hashStr}}

	controlSrv, err := NewServer(controlCfg)
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}
	defer func() {
		controlSrv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races
		_ = os.RemoveAll(tmpDir)          //nolint:errcheck
	}()

	if err := controlSrv.db.CreateUser(&db.User{ID: "edge-user", Email: "edge@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	patHash := sha256.Sum256([]byte("pat-edge-123"))
	if err := controlSrv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    "edge-user",
		TokenHash: hex.EncodeToString(patHash[:]),
		Name:      "edge-test-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("creating PAT: %v", err)
	}
	when := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if _, err := controlSrv.db.RecordFirstSeen("edge-user", MinVersionDocumentID, "v1.45.0", when); err != nil {
		t.Fatalf("backdating first sight: %v", err)
	}

	edgeRegister := func(clientVersion, subdomain string) *httptest.ResponseRecorder {
		payload := []byte(`{
			"subdomain_prefix": "` + subdomain + `",
			"auth_token": "pat-edge-123",
			"ports": [{"local_port": 8080}],
			"domains": ["us.lfr-demo.se"],
			"client_ip": "8.8.8.8",
			"client_version": "` + clientVersion + `",
			"client_os": "darwin"
		}`)
		req := httptest.NewRequest("POST", "http://control.lfr-demo.se/api/internal/edge-register", bytes.NewReader(payload))
		req.Header.Set("X-Edge-Token", "my-edge-secret")
		rec := httptest.NewRecorder()
		controlSrv.ServeHTTP(rec, req)
		return rec
	}

	rec := edgeRegister("v1.40.0", "edge-old")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected the control plane to refuse an expired client on the edge path, got %d: %s", rec.Code, rec.Body.String())
	}
	var refusal struct {
		Error      string          `json:"error"`
		MinVersion MinVersionState `json:"min_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decoding the edge refusal: %v", err)
	}
	for _, want := range []string{"v1.40.0", "v1.45.0", "lfr-tunnel -upgrade"} {
		if !strings.Contains(refusal.Error, want) {
			t.Errorf("the edge refusal does not name %q: %s", want, refusal.Error)
		}
	}
	// Relayed as structured state too, because the edge hands this to the client verbatim
	// and an edge holds no database to recompute it from.
	if !refusal.MinVersion.Required || refusal.MinVersion.Phase != GracePhaseExpired {
		t.Errorf("expected the expired min_version block on the edge refusal, got %+v", refusal.MinVersion)
	}

	// The control: a current client on the same path, same backdated row, still registers.
	recOK := edgeRegister("v1.48.0", "edge-new")
	if recOK.Code != http.StatusOK {
		t.Fatalf("a client at the floor was refused on the edge path too: %d %s", recOK.Code, recOK.Body.String())
	}
}
