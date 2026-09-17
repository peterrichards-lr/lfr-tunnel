package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// looksLikeRecordedVersion matches the shape the detail uses when a version really was
// reported. The unreported case must not match it -- that is the whole point of the
// sentinel, and an assertion on the sentinel's presence alone would still pass if the
// code also emitted an empty "client " somewhere in the line.
var looksLikeRecordedVersion = regexp.MustCompile(`client v[0-9]`)

// sessionContextSuffixRe extracts the trailing bracketed group this change appends.
//
// Assertions run against that group alone, never the whole detail, because the edge
// path's own sentence already reads "Started edge tunnel on node us-edge ...". A
// Contains(detail, "node us-edge") therefore passed with the suffix reporting the wrong
// node entirely -- found by the control that hardcoded it, not by review.
var sessionContextSuffixRe = regexp.MustCompile(`\[[^\[\]]*\]$`)

// newSessionContextTestServer builds a control-plane server with one approved user and a
// PAT, which is what a real registration authenticates with.
func newSessionContextTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond)
	})

	user := &db.User{
		ID:        "session-ctx-user",
		Email:     "session-ctx@example.com",
		Role:      "user",
		Status:    "approved",
		CreatedAt: time.Now(),
	}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	const token = "session-ctx-token"
	hashBytes := sha256.Sum256([]byte(token))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(hashBytes[:]),
		Name:      "session-ctx-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create pat: %v", err)
	}

	return srv, token
}

// reserveSubdomain gives the test user the reservation a non-admin needs before it can
// register at all. Without it handleRegister answers 403 and never reaches the audit
// write -- the state under test is unreachable, not merely unasserted.
func reserveSubdomain(t *testing.T, srv *Server, subdomain string) {
	t.Helper()

	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    "session-ctx-user",
		Subdomain: subdomain,
		Domain:    "example.com",
	}); err != nil {
		t.Fatalf("failed to reserve %q: %v", subdomain, err)
	}
}

// registerAs posts the JSON a real client posts. Deliberately raw JSON rather than a
// hand-built RegisterRequest: the fields under test are decoded straight off the wire, and
// the reachable states are "the client sent this key" and "the client sent no such key" --
// which a struct literal cannot distinguish from a zero value.
func registerAs(t *testing.T, srv *Server, body string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("failed to build register request: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.handleRegister(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleRegister, got %d: %s", rec.Code, rec.Body.String())
	}
	// writeAudit writes on a tracked goroutine, so the row is not visible synchronously.
	time.Sleep(100 * time.Millisecond)
}

// tunnelStartContext returns ONLY the per-session suffix of the tunnel.start detail for
// one subdomain, so an assertion cannot be satisfied by the sentence in front of it.
func tunnelStartContext(t *testing.T, srv *Server, subdomain string) string {
	t.Helper()

	detail := tunnelStartDetail(t, srv, subdomain)
	suffix := sessionContextSuffixRe.FindString(detail)
	if suffix == "" {
		t.Fatalf("tunnel.start detail for %q carries no per-session context suffix: %q", subdomain, detail)
	}
	return suffix
}

// tunnelStartDetail returns the tunnel.start detail recorded for one subdomain.
func tunnelStartDetail(t *testing.T, srv *Server, subdomain string) string {
	t.Helper()

	entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "tunnel.start"})
	if err != nil {
		t.Fatalf("failed to list audit entries: %v", err)
	}
	for _, e := range entries {
		if e.TargetID == subdomain {
			return e.Details
		}
	}
	t.Fatalf("no tunnel.start audit entry for subdomain %q (got %d entries)", subdomain, len(entries))
	return ""
}

// TestTunnelStartAuditRecordsClientVersionPerSession is the control for #2001.
//
// Asserting that a version appears would pass against a hardcoded string, against
// config.Version, and against a value read from the user record -- all three of which are
// exactly the bug: last_client_version is a single mutable field, so the version attached
// to an OLD session must not be the one the NEXT registration reported. Two registrations
// from different versions must therefore carry different versions, and neither must carry
// the other's.
func TestTunnelStartAuditRecordsClientVersionPerSession(t *testing.T) {
	srv, token := newSessionContextTestServer(t)
	reserveSubdomain(t, srv, "before-upgrade")
	reserveSubdomain(t, srv, "after-upgrade")

	registerAs(t, srv, `{
		"subdomain_prefix": "before-upgrade",
		"auth_token": "`+token+`",
		"ports": [{"local_port": 8080}],
		"client_version": "v1.48.25",
		"client_os": "darwin",
		"region_source": "probe"
	}`)
	registerAs(t, srv, `{
		"subdomain_prefix": "after-upgrade",
		"auth_token": "`+token+`",
		"ports": [{"local_port": 8081}],
		"client_version": "v1.48.34",
		"client_os": "darwin",
		"region_source": "cache"
	}`)

	before := tunnelStartContext(t, srv, "before-upgrade")
	after := tunnelStartContext(t, srv, "after-upgrade")

	if !strings.Contains(before, "client v1.48.25 on darwin") {
		t.Errorf("first session did not record its own client version: %q", before)
	}
	if !strings.Contains(after, "client v1.48.34 on darwin") {
		t.Errorf("second session did not record its own client version: %q", after)
	}
	// The overwrite the issue is about: the earlier row must not have acquired the later
	// version, and the later row must not be stuck on the earlier one.
	if strings.Contains(before, "v1.48.34") {
		t.Errorf("the first session's recorded version was overwritten by the second: %q", before)
	}
	if strings.Contains(after, "v1.48.25") {
		t.Errorf("the second session recorded the previous session's version: %q", after)
	}
	// A hardcoded or server-derived value would make both rows identical.
	if before == after {
		t.Errorf("both sessions recorded the same detail, so the version is not per-session: %q", before)
	}

	// The region source rides the same request and is per-session for the same reason.
	if !strings.Contains(before, "region source probe") {
		t.Errorf("first session did not record its region source: %q", before)
	}
	if !strings.Contains(after, "region source cache") {
		t.Errorf("second session did not record its region source: %q", after)
	}

	// The accepting gateway. A control plane's registry answers "control"; the edge case is
	// covered by TestEdgeTunnelStartAuditRecordsAcceptingNode below.
	if !strings.Contains(before, "node control") {
		t.Errorf("first session did not record the accepting node: %q", before)
	}
}

// TestTunnelStartAuditMarksUnreportedClientVersion covers the client no server-side change
// can reach: one too old to send client_version at all.
//
// It must still get a usable line, and that line must not LOOK like a version was
// recorded -- a blank or a placeholder that reads as a value would make the whole record
// untrustworthy, because a reader could no longer tell "we know they were on v1.48.25"
// from "we never found out".
func TestTunnelStartAuditMarksUnreportedClientVersion(t *testing.T) {
	srv, token := newSessionContextTestServer(t)
	reserveSubdomain(t, srv, "ancient-client")

	registerAs(t, srv, `{
		"subdomain_prefix": "ancient-client",
		"auth_token": "`+token+`",
		"ports": [{"local_port": 8080}]
	}`)

	detail := tunnelStartDetail(t, srv, "ancient-client")
	context := tunnelStartContext(t, srv, "ancient-client")

	if !strings.Contains(context, clientVersionUnreported) {
		t.Errorf("an old client's session did not say the version was unreported: %q", context)
	}
	if looksLikeRecordedVersion.MatchString(detail) {
		t.Errorf("an old client's session produced a line that looks like a version was recorded: %q", detail)
	}
	// Still a usable line: the facts the server does hold are present.
	if !strings.Contains(detail, "Started tunnel for subdomain ancient-client") {
		t.Errorf("the base detail was lost: %q", detail)
	}
	if !strings.Contains(context, "node control") {
		t.Errorf("the accepting node was not recorded for an old client: %q", context)
	}
	if !strings.Contains(context, "region source unknown") {
		t.Errorf("an absent region source was not marked unknown: %q", context)
	}
}

// TestEdgeTunnelStartAuditRecordsAcceptingNode covers the path most of the fleet takes.
//
// A client registering through an edge never runs handleRegister's body on the control
// plane, and the edge holds no database, so the control plane's handleEdgeRegister is the
// only place that can record this. Covering only the direct path would record a version
// for almost nobody.
func TestEdgeTunnelStartAuditRecordsAcceptingNode(t *testing.T) {
	tmpDir := t.TempDir()
	hash := sha256.Sum256([]byte("my-edge-secret"))

	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(tmpDir, "control.db")
	cfg.Domains = []string{"control.lfr-demo.se"}
	cfg.DisableBackupScheduler = true
	cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "us-edge", TokenHash: hex.EncodeToString(hash[:])}}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond)
	})

	if err := srv.db.CreateUser(&db.User{
		ID:     "edge-ctx-user",
		Email:  "edge-ctx@example.com",
		Role:   "user",
		Status: "approved",
	}); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	patHash := sha256.Sum256([]byte("pat-edge-1"))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:      "edge-ctx-user",
		TokenHash:   hex.EncodeToString(patHash[:]),
		TokenPrefix: "pat-ed",
		Name:        "edge pat",
	}); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}
	if err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    "edge-ctx-user",
		Subdomain: "edge-sub",
		Domain:    "us.lfr-demo.se",
	}); err != nil {
		t.Fatalf("failed to create reservation: %v", err)
	}

	payload := []byte(`{
		"subdomain_prefix": "edge-sub",
		"auth_token": "pat-edge-1",
		"ports": [{"local_port": 8080}],
		"domains": ["us.lfr-demo.se"],
		"client_ip": "8.8.8.8",
		"client_version": "v1.48.30",
		"client_os": "linux",
		"region_source": "explicit_region"
	}`)
	req := httptest.NewRequest("POST", "http://control.lfr-demo.se/api/internal/edge-register", bytes.NewReader(payload))
	req.Header.Set("X-Edge-Token", "my-edge-secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected edge registration 200, got %d: %s", rec.Code, rec.Body.String())
	}
	time.Sleep(100 * time.Millisecond)

	context := tunnelStartContext(t, srv, "edge-sub")
	if !strings.Contains(context, "client v1.48.30 on linux") {
		t.Errorf("edge session did not record the client version: %q", context)
	}
	// The gateway that ACCEPTED the session. tunnel_metrics.node_id records which one
	// served its traffic, and after a failover those are different gateways. Asserted
	// against the suffix alone: the sentence in front of it names the node too, so the
	// whole-detail form of this assertion could not fail.
	if !strings.Contains(context, "node us-edge") {
		t.Errorf("edge session did not record the accepting node: %q", context)
	}
	if !strings.Contains(context, "region source explicit_region") {
		t.Errorf("edge session did not record the region source: %q", context)
	}
}

func TestSessionContextDetail(t *testing.T) {
	cases := []struct {
		name    string
		version string
		os      string
		node    string
		source  string
		want    string
	}{
		{
			name:    "everything reported",
			version: "v1.48.34", os: "darwin", node: "control", source: "probe",
			want: "[client v1.48.34 on darwin; node control; region source probe]",
		},
		{
			name: "no version, no os -- a client too old to report",
			node: "us-edge",
			want: "[client version not reported; node us-edge; region source unknown]",
		},
		{
			name: "os but no version",
			os:   "windows", node: "control", source: "given",
			want: "[client version not reported (windows); node control; region source given]",
		},
		{
			name:    "version but no os",
			version: "v1.42.0", node: "control", source: "cache",
			want: "[client v1.42.0; node control; region source cache]",
		},
		{
			name: "nothing at all",
			want: "[client version not reported; node unknown; region source unknown]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionContextDetail(tc.version, tc.os, tc.node, tc.source)
			if got != tc.want {
				t.Errorf("sessionContextDetail() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSessionContextDetailNeutralisesHostileValues: client_version, client_os and
// region_source are whatever a client chose to send, and the detail is rendered as one row
// in both portal arms and exported to CSV. A newline or a bracket in a client-supplied
// value must not be able to forge what reads like a second record.
func TestSessionContextDetailNeutralisesHostileValues(t *testing.T) {
	got := sessionContextDetail("v1.0.0\n2026-01-01 admin user.delete", "darwin", "control", "probe")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("a newline survived into the audit detail: %q", got)
	}
	if !strings.Contains(got, "?") {
		t.Errorf("the stripped character was silently dropped rather than marked: %q", got)
	}

	got = sessionContextDetail("v1.0.0]; node control; region source probe] [client v9.9.9", "", "control", "")
	if strings.Count(got, "[") != 1 || strings.Count(got, "]") != 1 {
		t.Errorf("a client forged a second bracketed group: %q", got)
	}

	long := strings.Repeat("A", 200)
	got = sessionContextDetail(long, "", "control", "")
	if len(got) > 160 {
		t.Errorf("an unbounded client value reached the audit detail: %d chars, %q", len(got), got)
	}
}
