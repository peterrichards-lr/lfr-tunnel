package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// These tests are the assertions that would have caught #1956.
//
// Every state below already produced a nil provisionerClient, a hidden set of power
// controls and a 501, so every pre-existing test in server_edge_provisioner_test.go passes
// either way -- which is precisely why the defect survived them. What was missing is that
// "this deployment has no edge-provisioner sidecar" and "this deployment HAS one and its
// token file is mistyped" were the same state everywhere an operator could look.
//
// Each case goes through newProvisionerClient, the constructor NewServer itself calls, with
// a config an operator can actually produce. Nothing here asserts on a value the real code
// path cannot generate.

// tokenFileFixture writes a token file and returns its path.
func tokenFileFixture(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestNewProvisionerClient_TellsABrokenTokenFromNoSidecar(t *testing.T) {
	good := tokenFileFixture(t, "token", "s3cr3t-value\n")
	missing := filepath.Join(t.TempDir(), "edge-provisioner.tokne")
	empty := tokenFileFixture(t, "empty", "\n")

	cases := []struct {
		name       string
		cfg        config.ServerConfig
		wantClient bool
		wantReason edgePowerReason
		wantFile   string
	}{
		{
			// The default and the state every non-AWS deployment is in. It must keep
			// reporting exactly this, or a gateway that never wanted the feature starts
			// telling its admins something is wrong.
			name:       "no sidecar configured",
			cfg:        config.ServerConfig{},
			wantReason: edgePowerNotConfigured,
		},
		{
			name:       "url set, token file setting missing",
			cfg:        config.ServerConfig{EdgeProvisionerURL: "http://127.0.0.1:9999"},
			wantReason: edgePowerTokenPathUnset,
		},
		{
			name: "url set, token path mistyped",
			cfg: config.ServerConfig{
				EdgeProvisionerURL:       "http://127.0.0.1:9999",
				EdgeProvisionerTokenFile: missing,
			},
			wantReason: edgePowerTokenNotFound,
			wantFile:   missing,
		},
		{
			// The sidecar created the file and was interrupted before writing it. A path
			// check will never explain this one, which is why it is not folded into
			// token_not_found.
			name: "url set, token file empty",
			cfg: config.ServerConfig{
				EdgeProvisionerURL:       "http://127.0.0.1:9999",
				EdgeProvisionerTokenFile: empty,
			},
			wantReason: edgePowerTokenEmpty,
			wantFile:   empty,
		},
		{
			name: "url set, token loads",
			cfg: config.ServerConfig{
				EdgeProvisionerURL:       "http://127.0.0.1:9999",
				EdgeProvisionerTokenFile: good,
			},
			wantClient: true,
			wantReason: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			client, d := newProvisionerClient(&cfg)

			if (client != nil) != tc.wantClient {
				t.Fatalf("client non-nil = %v, want %v", client != nil, tc.wantClient)
			}
			if d.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", d.Reason, tc.wantReason)
			}
			if d.TokenFile != tc.wantFile {
				t.Errorf("token file = %q, want %q", d.TokenFile, tc.wantFile)
			}
			// A misconfiguration must still never stop the gateway serving: nil is a
			// working no-op, not a startup failure. faulty() is what the portals branch on.
			if faulty := d.faulty(); faulty == (tc.wantReason == "" || tc.wantReason == edgePowerNotConfigured) {
				t.Errorf("faulty() = %v for reason %q", faulty, tc.wantReason)
			}
		})
	}
}

// TestNewProvisionerClient_UnreadableTokenIsItsOwnState covers the one case that needs a
// permission bit, and the one that carries a Detail string.
func TestNewProvisionerClient_UnreadableTokenIsItsOwnState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not honoured on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file regardless of its mode")
	}
	// Built rather than written out: a 64-char hex literal is what a real token looks like,
	// and the repo's gitleaks pre-commit hook blocks one on sight -- correctly.
	secret := strings.Repeat("fixture-not-a-real-token.", 3)
	path := tokenFileFixture(t, "token", secret+"\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Logf("cleanup: restoring mode on %s: %v", path, err)
		}
	})

	cfg := config.ServerConfig{
		EdgeProvisionerURL:       "http://127.0.0.1:9999",
		EdgeProvisionerTokenFile: path,
	}
	client, d := newProvisionerClient(&cfg)
	if client != nil {
		t.Fatal("an unreadable token must leave the client nil, not half-built")
	}
	if d.Reason != edgePowerTokenUnreadable {
		t.Errorf("reason = %q, want %q", d.Reason, edgePowerTokenUnreadable)
	}
	// "permission denied" is the sentence that resolves this state and it exists nowhere
	// else -- the panel shows it verbatim.
	if d.Detail == "" {
		t.Error("no Detail: the filesystem's own complaint is the whole value of this state")
	}

	// And the half that must NOT improve: the diagnosis says the load failed and why, never
	// what the file held. A length or a prefix would narrow the shared secret just as well.
	for _, field := range []string{d.Detail, d.TokenFile, string(d.Reason)} {
		if strings.Contains(field, secret) || strings.Contains(field, secret[:12]) {
			t.Errorf("the diagnosis carries the token or a prefix of it: %q", field)
		}
	}
}

// newUserSession is newAdminSession's non-admin twin. The token path and the filesystem
// error are gateway internals, and this route is the portal's Network Health page, which
// every signed-in user can load.
func newUserSession(t *testing.T, srv *Server, email string) string {
	t.Helper()
	if srv.db != nil {
		if u, err := srv.db.GetUserByEmail(email); err == nil && u != nil {
			u.Role = "user"
			if err := srv.db.UpdateUser(u); err != nil {
				t.Fatalf("demoting %s: %v", email, err)
			}
		} else if err := srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "user", Status: "approved"}); err != nil {
			t.Fatalf("seeding user %s: %v", email, err)
		}
	}
	token := "test-user-session-" + email
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{Email: email, ExpiresAt: time.Now().Add(time.Hour)})
	return token
}

// breakTheTokenFile puts srv into the state an operator reaches by mistyping
// edge_provisioner_token_file, using the same constructor NewServer uses.
func breakTheTokenFile(t *testing.T, srv *Server) string {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "edge-provisioner.tokne")
	cfg := config.ServerConfig{
		EdgeProvisionerURL:       "http://127.0.0.1:9999",
		EdgeProvisionerTokenFile: missing,
	}
	srv.provisionerClient, srv.edgePowerDiagnosis = newProvisionerClient(&cfg)
	if srv.provisionerClient != nil {
		t.Fatal("setup: expected a nil client for a missing token file")
	}
	return missing
}

func TestEdgeHealth_SaysWhyPowerActionsAreOff(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	admin := newAdminSession(t, srv, "admin@example.com")

	// 1. The default: no sidecar configured. Unchanged from before #1956 apart from the
	// reason code itself -- no path, no detail, nothing for a deployment to react to.
	req := adminRequest(http.MethodGet, "/api/portal/edge-health", nil, admin)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	body := decodeEdgeHealth(t, w)
	if body.Enabled {
		t.Fatal("expected edge_power_actions_enabled=false with no sidecar configured")
	}
	if body.Reason != string(edgePowerNotConfigured) {
		t.Errorf("reason = %q, want %q", body.Reason, edgePowerNotConfigured)
	}
	if body.TokenFile != "" || body.Detail != "" {
		t.Errorf("an unconfigured gateway volunteered a path/detail: %q / %q", body.TokenFile, body.Detail)
	}

	// 2. Configured, and the token file is mistyped. This is the state that used to be
	// reported as the one above.
	missing := breakTheTokenFile(t, srv)
	req2 := adminRequest(http.MethodGet, "/api/portal/edge-health", nil, admin)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	body2 := decodeEdgeHealth(t, w2)
	if body2.Enabled {
		t.Fatal("a broken token must still leave the feature off")
	}
	if body2.Reason != string(edgePowerTokenNotFound) {
		t.Errorf("reason = %q, want %q -- a mistyped token file is not 'never configured'", body2.Reason, edgePowerTokenNotFound)
	}
	// The path is named because that is what makes a typo self-evident to the person who
	// typed it.
	if body2.TokenFile != missing {
		t.Errorf("token file = %q, want the configured path %q", body2.TokenFile, missing)
	}
}

// TestEdgeHealth_DiagnosisIsAdminOnly holds the scope of the new fields.
//
// This route is not admin-gated -- every signed-in user loads Network Health -- so the
// diagnosis has to be gated inside the handler. A non-admin response must be exactly what
// it was before #1956.
func TestEdgeHealth_DiagnosisIsAdminOnly(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	breakTheTokenFile(t, srv)
	user := newUserSession(t, srv, "user@example.com")

	req := adminRequest(http.MethodGet, "/api/portal/edge-health", nil, user)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	body := decodeEdgeHealth(t, w)
	if body.Enabled {
		t.Fatal("setup did not take: expected the feature off")
	}
	if body.Reason != "" || body.TokenFile != "" || body.Detail != "" {
		t.Errorf("a non-admin was sent the gateway's token path/diagnosis: reason=%q file=%q detail=%q",
			body.Reason, body.TokenFile, body.Detail)
	}
}

// TestRequireProvisioner_501SaysWhichStateItIsIn covers the other admin surface: the 501
// body a stray call gets. The status is unchanged, and so is the sentence for the one state
// that sentence was ever true of.
func TestRequireProvisioner_501SaysWhichStateItIsIn(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	admin := newAdminSession(t, srv, "admin@example.com")

	post := func() (int, string) {
		req := adminRequest(http.MethodPost, "/api/admin/edge/edge-sa/start", []byte(`{}`), admin)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	code, unconfigured := post()
	if code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", code)
	}
	if !strings.Contains(unconfigured, "not configured on this server") {
		t.Errorf("the no-sidecar wording changed: %s", unconfigured)
	}

	missing := breakTheTokenFile(t, srv)
	code2, broken := post()
	if code2 != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", code2)
	}
	if strings.Contains(broken, "not configured on this server") {
		t.Errorf("a configured sidecar with a bad token file is still reported as not configured: %s", broken)
	}
	// Compared against the JSON-ENCODED path, not the raw one.
	//
	// The body is JSON, so a Windows path's backslashes arrive doubled:
	// C:\Users\... is written as C:\\Users\\... . The raw path is therefore not a
	// substring of the body, and this assertion failed on windows-latest while passing on
	// Linux and macOS, where the separator needs no escaping. Encoding the expectation the
	// same way the handler encodes the value compares like with like on every platform.
	encoded, err := json.Marshal(missing)
	if err != nil {
		t.Fatalf("encoding the expected path: %v", err)
	}
	// Trim the quotes json.Marshal adds around the string.
	wantPath := string(encoded[1 : len(encoded)-1])
	if !strings.Contains(broken, wantPath) {
		t.Errorf("the 501 body does not name the token file it tried (%s): %s", wantPath, broken)
	}
	// Still a JSON object with an "error" key: the portals read `data.error` from it, and a
	// path can contain a quote.
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(broken)), &parsed); err != nil {
		t.Fatalf("the 501 body is not valid JSON: %v (%s)", err, broken)
	}
	if parsed.Error == "" {
		t.Error("the 501 body has no error message")
	}
}

type edgeHealthBody struct {
	Enabled   bool   `json:"edge_power_actions_enabled"`
	Reason    string `json:"edge_power_actions_reason"`
	TokenFile string `json:"edge_power_actions_token_file"`
	Detail    string `json:"edge_power_actions_detail"`
}

func decodeEdgeHealth(t *testing.T, w *httptest.ResponseRecorder) edgeHealthBody {
	t.Helper()
	var out edgeHealthBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v (status %d, body %s)", err, w.Code, w.Body.String())
	}
	return out
}

// TestAWindowsPathInAJSONBodyNeedsEncodingToBeFound reproduces, on ANY platform, the failure
// that only windows-latest saw.
//
// The 501 body is JSON, so a Windows path's separators arrive doubled. An assertion written
// against the raw path therefore cannot match, while the same assertion passes on Linux and
// macOS where the separator is "/" and needs no escaping -- so the defect was invisible to
// every developer machine and to two of the three CI platforms.
//
// This pins the property rather than the platform: no Windows runner is needed to know the
// comparison is being made like with like.
func TestAWindowsPathInAJSONBodyNeedsEncodingToBeFound(t *testing.T) {
	const windowsPath = `C:\Users\RUNNER~1\AppData\Local\Temp\edge-provisioner.token`

	body, err := json.Marshal(map[string]string{
		"error": "no file exists at the edge_provisioner_token_file path " + windowsPath,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The old assertion. This is what failed on windows-latest.
	if strings.Contains(string(body), windowsPath) {
		t.Error("PREMISE FAILED: the raw Windows path was found in the JSON body, so this test " +
			"is not reproducing the escaping that caused the failure")
	}

	// The fixed assertion: encode the expectation the same way the handler encoded the value.
	encoded, err := json.Marshal(windowsPath)
	if err != nil {
		t.Fatalf("marshal path: %v", err)
	}
	if !strings.Contains(string(body), string(encoded[1:len(encoded)-1])) {
		t.Error("the JSON-encoded path was not found in the body, so the fix does not hold")
	}

	// BOUNDING: a POSIX path needs no escaping, so the fix must be a no-op there rather than
	// changing behaviour on the platforms that were already passing.
	const posixPath = "/etc/lfr-tunneld/edge-provisioner.token"
	enc2, err := json.Marshal(posixPath)
	if err != nil {
		t.Fatalf("marshal posix: %v", err)
	}
	if string(enc2[1:len(enc2)-1]) != posixPath {
		t.Errorf("encoding changed a POSIX path (%s -> %s); the fix must not alter behaviour "+
			"where it was already correct", posixPath, string(enc2[1:len(enc2)-1]))
	}
}
