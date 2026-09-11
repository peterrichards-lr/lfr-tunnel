package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The gateway half of #1763's transport.
//
// The property under test is not "a queue works" -- it is that a collection request reaches the
// client it was authorised for, reaches nobody else, and stops being deliverable the moment its
// basis goes away. #1696 made consent enforceable at the request; queueing put time between the
// request and the delivery, and these are what keep the guarantee across that gap.

func TestDiagnosticsCommandQueueRoundTrip(t *testing.T) {
	s := &Server{}

	cmd := s.queueDiagnosticsCollect("user-1", "admin@example.com")
	if cmd.ID == "" || cmd.Type != diagnosticsCommandCollectLogs {
		t.Fatalf("queued command is malformed: %+v", cmd)
	}
	if !s.hasPendingDiagnosticsCommand("user-1") {
		t.Error("a queued command does not show as pending")
	}

	live, expired := s.pendingDiagnosticsCommands("user-1")
	if len(live) != 1 || len(expired) != 0 {
		t.Fatalf("live=%d expired=%d, want 1 and 0", len(live), len(expired))
	}
	// Still pending after being handed out: delivery is at-least-once, and dropping it on the
	// first send would lose the request whenever a heartbeat response did not arrive.
	if !s.hasPendingDiagnosticsCommand("user-1") {
		t.Error("the command was consumed by being delivered; it must survive until acked")
	}

	if got := s.ackDiagnosticsCommand("user-1", cmd.ID); got == nil {
		t.Fatal("acking the command returned nothing, so the delivery cannot be audited")
	}
	if s.hasPendingDiagnosticsCommand("user-1") {
		t.Error("the command is still pending after being acked")
	}
	// A second ack of the same id is normal under at-least-once delivery and must not produce
	// a second audit entry.
	if got := s.ackDiagnosticsCommand("user-1", cmd.ID); got != nil {
		t.Error("a repeat ack returned a command again, which would double-audit the delivery")
	}
}

func TestDiagnosticsCommandsAreScopedToOneUser(t *testing.T) {
	// The whole feature is "collect from the person who agreed to it". A queue that leaked
	// across users would hand one person's command to another person's client.
	s := &Server{}
	s.queueDiagnosticsCollect("user-1", "admin@example.com")

	if live, _ := s.pendingDiagnosticsCommands("user-2"); len(live) != 0 {
		t.Fatalf("user-2 was handed user-1's command: %+v", live)
	}
	if s.ackDiagnosticsCommand("user-2", "anything") != nil {
		t.Error("user-2 was able to ack a command that was not theirs")
	}
}

func TestDiagnosticsCommandExpires(t *testing.T) {
	// The TTL is a consent guarantee, not housekeeping: it bounds how long a request
	// authorised at T can still be delivered at T+n.
	s := &Server{}
	cmd := s.queueDiagnosticsCollect("user-1", "admin@example.com")

	s.diagCommandsMu.Lock()
	s.diagCommands["user-1"][0].queuedAt = time.Now().UTC().Add(-diagnosticsCommandTTL - time.Minute)
	s.diagCommandsMu.Unlock()

	live, expired := s.pendingDiagnosticsCommands("user-1")
	if len(live) != 0 {
		t.Errorf("an expired command was still offered for delivery: %+v", live)
	}
	if len(expired) != 1 || expired[0].ID != cmd.ID {
		t.Fatalf("the expiry was not reported, so it could not be audited: %+v", expired)
	}
	if s.hasPendingDiagnosticsCommand("user-1") {
		t.Error("the expired command is still queued")
	}
}

func TestDiagnosticsSecondRequestReplacesRatherThanQueues(t *testing.T) {
	// An admin clicking twice wants the logs once. Two commands would mean two uploads of the
	// same files, and the second would look like a second incident in the audit trail.
	s := &Server{}
	first := s.queueDiagnosticsCollect("user-1", "admin@example.com")
	second := s.queueDiagnosticsCollect("user-1", "admin@example.com")

	live, _ := s.pendingDiagnosticsCommands("user-1")
	if len(live) != 1 {
		t.Fatalf("two requests produced %d queued commands, want 1", len(live))
	}
	if live[0].ID != second.ID {
		t.Errorf("the surviving command is %s, want the newer %s", live[0].ID, second.ID)
	}
	if first.ID == second.ID {
		t.Error("two requests produced the same id, so the client cannot tell them apart")
	}
}

// Only one command may ride a heartbeat: the client reads 512 bytes of the body and shares that
// budget with the shutdown warning.
func TestOnlyOneCommandRidesAHeartbeat(t *testing.T) {
	s := &Server{}
	s.diagCommandsMu.Lock()
	s.diagCommands = map[string][]*diagnosticsCommand{
		"user-1": {
			{ID: "aaa", Type: diagnosticsCommandCollectLogs, userID: "user-1", queuedAt: time.Now().UTC()},
			{ID: "bbb", Type: diagnosticsCommandCollectLogs, userID: "user-1", queuedAt: time.Now().UTC()},
			{ID: "ccc", Type: diagnosticsCommandCollectLogs, userID: "user-1", queuedAt: time.Now().UTC()},
		},
	}
	s.diagCommandsMu.Unlock()

	live, _ := s.pendingDiagnosticsCommands("user-1")
	if len(live) != 1 {
		t.Fatalf("%d commands offered for one heartbeat; the body would exceed the client's 512-byte read", len(live))
	}
}

// The wire format the gateway emits must not collide with the two meanings the heartbeat body
// already has: {"status":"ok"} means "no lease here" and would re-register the tunnel.
func TestCommandPayloadCarriesNoStatusField(t *testing.T) {
	body := map[string]interface{}{
		"commands": []*diagnosticsCommand{
			{ID: "0123456789ab", Type: diagnosticsCommandCollectLogs},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, present := decoded["status"]; present {
		t.Fatal("the command body carries a top-level status field; the client would read it as a lost lease and re-register the tunnel")
	}

	// The gateway's bookkeeping must not reach the client either.
	if len(raw) == 0 || strings.Contains(string(raw), "requestedBy") || strings.Contains(string(raw), "admin@") {
		t.Errorf("the command payload leaks server-side fields: %s", raw)
	}
}

// Reachability decides what the admin is told. Getting this wrong means either queueing a
// command nobody can collect, or refusing one that would have worked.
func TestDiagnosticsReachabilityExplainsWhyNot(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	reach := srv.diagnosticsReachability("nobody-home")
	if reach.served {
		t.Fatal("a user with no tunnel was reported as reachable")
	}
	if reach.reason == "" {
		t.Error("an unreachable user produced no explanation for the admin")
	}
}

func TestAckOfAnUnknownCommandIsNotAnError(t *testing.T) {
	// A client can legitimately ack an id the gateway has already forgotten -- the ack raced
	// the TTL, or a previous ack arrived. Treating it as an error would put noise in the audit
	// trail for a case that is normal.
	s := &Server{}
	leases := []*TunnelLease{{UserID: "user-1"}}
	req, _ := http.NewRequest(http.MethodPost, "/api/tunnel-status", nil) //nolint:errcheck
	s.recordDiagnosticsAcks(leases, []string{"never-existed"}, req)
}

// heartbeat drives a real POST through handleTunnelStatus and returns the decoded body.
//
// The unit tests above prove the queue behaves. This proves the WIRING, which is the part that
// actually breaks: a command that never reaches the response body is a feature that silently
// does nothing, and every test above would still pass.
func heartbeat(t *testing.T, srv *Server, sessionToken string, ack []string) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{"session_token": sessionToken, "status": "up"}
	if len(ack) > 0 {
		payload["ack"] = ack
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling the heartbeat: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tunnel-status", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	srv.handleTunnelStatus(w, req)

	body := map[string]any{}
	if b := bytes.TrimSpace(w.Body.Bytes()); len(b) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatalf("the heartbeat body is not JSON (%q): %v", b, err)
		}
	}
	return w.Code, body
}

// registerDiagnosticsTunnel gives a user a lease on this gateway, which is what makes them
// reachable, and returns the session token its client would heartbeat with.
func registerDiagnosticsTunnel(t *testing.T, srv *Server, userID, sub string) string {
	t.Helper()
	// Three characters minimum and at least one host domain, both enforced by Register.
	token, _, err := srv.registry.Register(userID, sub,
		[]PortMapping{{LocalPort: 3000}}, []string{"diag.example.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registering a tunnel for %s: %v", userID, err)
	}
	return token
}

func TestQueuedCommandRidesTheHeartbeatAndStopsOnAck(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user, userSession := seedDiagnosticsUser(t, srv, "heartbeat@example.com", "user")
	grantDiagnosticsConsent(t, srv, userSession, true)
	sessionToken := registerDiagnosticsTunnel(t, srv, user.ID, "diag-heartbeat")

	// PREMISE. With nothing queued the heartbeat must carry no commands -- otherwise the
	// assertions below would pass against a response that always contains one.
	code, body := heartbeat(t, srv, sessionToken, nil)
	if code != http.StatusOK {
		t.Fatalf("a plain heartbeat returned %d", code)
	}
	if _, present := body["commands"]; present {
		t.Fatalf("a heartbeat with nothing queued carried commands: %v", body)
	}

	cmd := srv.queueDiagnosticsCollect(user.ID, "admin@example.com")

	code, body = heartbeat(t, srv, sessionToken, nil)
	if code != http.StatusOK {
		t.Fatalf("the heartbeat carrying a command returned %d", code)
	}
	// The body must never gain a top-level status field: the client reads {"status":"ok"} as
	// "this gateway holds no lease for me" and re-registers the tunnel.
	if _, present := body["status"]; present {
		t.Fatalf("the command body carries a status field, which would re-register the tunnel: %v", body)
	}
	cmds, _ := body["commands"].([]any)
	if len(cmds) != 1 {
		t.Fatalf("the queued command did not ride the heartbeat: %v", body)
	}
	first, _ := cmds[0].(map[string]any)
	if id, _ := first["id"].(string); id != cmd.ID {
		t.Errorf("delivered id = %v, want %s", first["id"], cmd.ID)
	}
	if typ, _ := first["t"].(string); typ != diagnosticsCommandCollectLogs {
		t.Errorf("delivered type = %v, want %s", first["t"], diagnosticsCommandCollectLogs)
	}

	// At-least-once: still delivered until acked.
	if _, body2 := heartbeat(t, srv, sessionToken, nil); len(body2) == 0 {
		t.Error("the command stopped being delivered before it was acknowledged")
	}

	// The ack ends it.
	heartbeat(t, srv, sessionToken, []string{cmd.ID})
	_, after := heartbeat(t, srv, sessionToken, nil)
	if _, present := after["commands"]; present {
		t.Errorf("the command was still delivered after being acknowledged: %v", after)
	}

	if hasAuditAction(auditActions(t, srv, user.ID), diagnosticsAuditDelivered) == nil {
		t.Error("the acknowledged delivery left no audit entry, so a collection that happened is unrecorded")
	}
}

func TestWithdrawnConsentStopsAQueuedCommandBeforeDelivery(t *testing.T) {
	// The guarantee #1696 makes is that a withdrawal is honoured from the moment it is made.
	// Queueing put time between authorisation and delivery; this is what keeps the promise
	// across that gap.
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user, userSession := seedDiagnosticsUser(t, srv, "changed@example.com", "user")
	grantDiagnosticsConsent(t, srv, userSession, true)
	sessionToken := registerDiagnosticsTunnel(t, srv, user.ID, "diag-withdrawn")

	srv.queueDiagnosticsCollect(user.ID, "admin@example.com")
	grantDiagnosticsConsent(t, srv, userSession, false)

	_, body := heartbeat(t, srv, sessionToken, nil)
	if _, present := body["commands"]; present {
		t.Fatalf("a command authorised before the withdrawal was still delivered after it: %v", body)
	}
	if srv.hasPendingDiagnosticsCommand(user.ID) {
		t.Error("the command is still queued after consent was withdrawn")
	}
}

func TestCommandsAreNotDeliveredToAnotherUsersSession(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	subject, subjectSession := seedDiagnosticsUser(t, srv, "subject@example.com", "user")
	other, _ := seedDiagnosticsUser(t, srv, "other@example.com", "user")
	grantDiagnosticsConsent(t, srv, subjectSession, true)
	otherToken := registerDiagnosticsTunnel(t, srv, other.ID, "diag-other")

	srv.queueDiagnosticsCollect(subject.ID, "admin@example.com")

	_, body := heartbeat(t, srv, otherToken, nil)
	if _, present := body["commands"]; present {
		t.Fatalf("one user's collection command was delivered to another user's client: %v", body)
	}
}

// grantDiagnosticsConsent goes through the endpoint the user's own Account Settings calls, not a
// direct column write: the consent state these tests depend on is then the same state the product
// produces, including any side effects the handler has.
func grantDiagnosticsConsent(t *testing.T, srv *Server, sessionToken string, enabled bool) {
	t.Helper()
	rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent",
		sessionToken, map[string]bool{"enabled": enabled})
	if rec.Code != http.StatusOK {
		t.Fatalf("setting diagnostics consent to %v returned %d: %s", enabled, rec.Code, rec.Body.String())
	}
}

// The route existed as a handler and was wired to nothing (#1894). It compiled, every test
// passed, and the client would have POSTed into a 404 -- an admin would have requested logs and
// simply never received them. golangci-lint found it indirectly, by reporting two constants as
// unused because the only thing referencing them was a handler no route reached.
//
// This asserts the seam directly, since that is what nothing covered: the tests exercised the
// store and the client separately and never the path between them.
func TestTheClientUploadRouteIsReachable(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req := httptest.NewRequest(http.MethodPost, "/api/client/diagnostics/upload",
		bytes.NewReader([]byte(`{"request_id":"nope","logs":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// 404 is the failure this guards: it means no route reaches the handler. Any other status
	// -- 401 without a session, 403 for an unrequested collection -- proves the handler ran.
	if w.Code == http.StatusNotFound {
		t.Fatalf("POST /api/client/diagnostics/upload returned 404: the handler is not routed, "+
			"so a client would upload into nothing (body: %s)", w.Body.String())
	}
}

// The admin surface has the same exposure: a handler nothing routes to looks identical to a
// working one from the Go side.
func TestTheAdminBundlesRouteIsReachable(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/diagnostics/bundles?email=nobody@example.com", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("GET /api/admin/diagnostics/bundles returned 404: the handler is not routed")
	}
}
