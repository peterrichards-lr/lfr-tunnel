package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Getting a collection request to an EDGE-served client, and its result back (#1991).
//
// These are end to end over the REAL control channel, between a real edge with no database and a
// real control plane behind an httptest listener, and every assertion is made against what a
// real participant actually observes: the body a client reads off its heartbeat, the rows in
// central's database, central's audit log. Nothing pokes diagCommands directly on either side.
//
// That is deliberate and it is the lesson of #1960 and #1938. The defect here was never in
// queueing a command -- diagnostics_transport.go's unit tests have always proved the queue
// behaves -- it was that nothing carried one to the gateway that could deliver it. A fixture
// that set the edge's queue by hand would have agreed with the bug for the whole of its life.
//
// Fixture reachability, asked explicitly: the tunnel is registered by POSTing /api/register to
// the EDGE, which proxies to central's /api/internal/edge-register, which is what creates the
// edgeLeases entry that makes a user "edge-served" in production. The edge's node id is derived
// from its token, so the token here matches a configured edge_nodes entry -- an invented token
// would produce a node id central has never heard of and an edge lease nothing could route to.

// edgeDiagFleet is one control plane, one edge, and a consenting user with a tunnel on the edge.
type edgeDiagFleet struct {
	central      *Server
	centralTS    *httptest.Server
	centralURL   string
	edge         *Server
	edgeNodeID   string
	adminSession string
	userID       string
	sessionToken string
}

const edgeDiagToken = "edge-us-secretvalue"

// startEdgeDiagFleet brings the whole thing up in the order production does.
func startEdgeDiagFleet(t *testing.T) *edgeDiagFleet {
	t.Helper()

	cfgCentral := config.DefaultServerConfig()
	cfgCentral.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgCentral.Domains = []string{"lfr-demo.se"}
	cfgCentral.DisableBackupScheduler = true
	cfgCentral.AllowClientAutoReservation = true
	cfgCentral.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hashOf(edgeDiagToken), URL: "https://us.lfr-demo.se"},
	}
	central, ts := startCentralForNodeSet(t, cfgCentral)

	edge := nodeSetTestEdge(t, ts.URL, edgeDiagToken, "us.lfr-demo.se")
	t.Cleanup(edge.Stop)
	waitUntil(t, stated("edge-us to authenticate with the control plane"), func() bool {
		return edgeIsRegistered(central, "edge-us")
	})

	user, _ := seedDiagnosticsUser(t, central, "edgeuser@example.com", "user")
	consented := time.Now().UTC()
	user.DiagnosticsConsentAt = &consented
	if err := central.db.UpdateUser(user); err != nil {
		t.Fatalf("recording diagnostics consent: %v", err)
	}
	_, adminSession := seedDiagnosticsUser(t, central, "edgeadmin@example.com", "admin")

	pat := "pat-edge-diagnostics"
	patHash := sha256.Sum256([]byte(pat))
	if err := central.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(patHash[:]),
		Name:      "edge-diag-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("creating the PAT: %v", err)
	}

	sessionToken := registerThroughEdge(t, edge, pat, "edgediag")

	return &edgeDiagFleet{
		central: central, centralTS: ts, centralURL: ts.URL, edge: edge, edgeNodeID: "edge-us",
		adminSession: adminSession, userID: user.ID, sessionToken: sessionToken,
	}
}

// registerThroughEdge registers a tunnel the way a client does: at the edge, which proxies to
// central. This is what puts the user in central's edgeLeases and a lease in the edge's registry
// -- the two halves of "served by an edge" that the whole feature keys on.
func registerThroughEdge(t *testing.T, edge *Server, authToken, sub string) string {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: sub,
		Ports:           []PortMapping{{LocalPort: 3000}},
		AuthToken:       authToken,
		ClientVersion:   config.Version,
		ClientOS:        "darwin",
	})
	if err != nil {
		t.Fatalf("marshalling the registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://us.lfr-demo.se/api/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registering through the edge returned %d: %s", rec.Code, rec.Body.String())
	}
	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the registration: %v", err)
	}
	if resp.SessionToken == "" {
		t.Fatalf("the edge registered the tunnel but returned no session token: %s", rec.Body.String())
	}
	return resp.SessionToken
}

// requestCollection drives the real admin endpoint and returns the decoded reply.
func requestCollection(t *testing.T, f *edgeDiagFleet) map[string]any {
	t.Helper()
	rec := postAs(t, f.central, f.central.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		f.adminSession, map[string]string{"email": "edgeuser@example.com"})
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the collect reply (%d): %s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusForbidden {
		t.Fatalf("the request was refused outright: %s", rec.Body.String())
	}
	return body
}

// queuedDiagIDs reports the command ids a gateway is holding for a user.
func queuedDiagIDs(srv *Server, userID string) []string {
	srv.diagCommandsMu.Lock()
	defer srv.diagCommandsMu.Unlock()
	ids := make([]string, 0, len(srv.diagCommands[userID]))
	for _, c := range srv.diagCommands[userID] {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// heartbeatCommands is what a client on this gateway actually reads: the commands its
// tunnel-status reply carries, as id:type pairs.
func heartbeatCommands(t *testing.T, srv *Server, sessionToken string, ack []string) []string {
	t.Helper()
	code, body := heartbeat(t, srv, sessionToken, ack)
	if code != http.StatusOK {
		t.Fatalf("the heartbeat was answered %d, not 200 -- no client would read a command from this", code)
	}
	raw, ok := body["commands"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		kind, _ := m["t"].(string)
		out = append(out, id+":"+kind)
	}
	sort.Strings(out)
	return out
}

// whereDiagStopped names the leg that failed, so a timeout below identifies ONE defect rather
// than any of four.
//
// A collection request crosses four places: central's queue, the control channel, the edge's
// queue, and the edge's heartbeat body. "It never arrived" is satisfied by a forward that never
// fired, a frame the edge has no case for, a queue the heartbeat does not read, and a command
// dropped on the way out -- four different defects with four different fixes. Reporting what
// each stage holds separates them (§5c).
func whereDiagStopped(f *edgeDiagFleet) string {
	return "central's queue holds " + listOrNothing(queuedDiagIDs(f.central, f.userID)) +
		", the edge's queue holds " + listOrNothing(queuedDiagIDs(f.edge, f.userID)) +
		", the edge's heartbeat carried " + listOrNothing(heartbeatCommandsQuiet(f))
}

// heartbeatCommandsQuiet is heartbeatCommands without a *testing.T, for use inside a failure
// message -- which must not itself be able to fail the test it is explaining.
func heartbeatCommandsQuiet(f *edgeDiagFleet) []string {
	payload, err := json.Marshal(map[string]any{"session_token": f.sessionToken, "status": "up"})
	if err != nil {
		return []string{"<unreadable>"}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tunnel-status", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	f.edge.handleTunnelStatus(w, req)
	body := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(w.Body.Bytes()), &body); err != nil {
		return []string{"<unreadable>"}
	}
	raw, ok := body["commands"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			id, _ := m["id"].(string)
			kind, _ := m["t"].(string)
			out = append(out, id+":"+kind)
		}
	}
	sort.Strings(out)
	return out
}

func listOrNothing(items []string) string {
	if len(items) == 0 {
		return "nothing"
	}
	return "[" + strings.Join(items, " ") + "]"
}

// waitForEdgeCommand waits for the edge's heartbeat to hand the client the request central
// issued, and returns what it carried.
func waitForEdgeCommand(t *testing.T, f *edgeDiagFleet, requestID string) []string {
	t.Helper()
	var got []string
	waitUntil(t, func() string {
		return "the client on edge-us to be handed collection request " + requestID +
			" -- " + whereDiagStopped(f)
	}, func() bool {
		got = heartbeatCommandsQuiet(f)
		return len(got) > 0
	})
	return got
}

// TestEdgeServedCollectionReachesTheClientAndIsAcknowledged is the request going down and the
// evidence of delivery coming back up.
//
// The ack leg is the half #1960 needed none of, and it is the half that matters most: the audit
// entry that says a collection actually reached somebody lives on central, and an edge has no
// database, so an ack that could not be relayed would be an ack that never happened.
func TestEdgeServedCollectionReachesTheClientAndIsAcknowledged(t *testing.T) {
	f := startEdgeDiagFleet(t)

	reply := requestCollection(t, f)
	if delivery, _ := reply["delivery"].(string); delivery != diagnosticsDeliveryQueued {
		t.Fatalf("delivery = %q, want %q -- an edge-served user is reachable now, and telling the "+
			"admin otherwise is the defect this closes (detail: %v)",
			delivery, diagnosticsDeliveryQueued, reply["delivery_detail"])
	}
	requestID, _ := reply["request_id"].(string)
	if requestID == "" {
		t.Fatalf("the reply carried no request id, so nothing can be correlated: %v", reply)
	}
	if detail, _ := reply["delivery_detail"].(string); !strings.Contains(detail, f.edgeNodeID) {
		t.Errorf("the admin was not told which edge is carrying this: %q", detail)
	}

	// What the CLIENT reads. Asserted on the id central issued, not merely on "a command":
	// an edge inventing its own request id would produce something no upload could be
	// matched against, and every other assertion here would still pass.
	got := waitForEdgeCommand(t, f, requestID)
	want := requestID + ":" + diagnosticsCommandCollectLogs
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the edge handed the client %v, want exactly [%s] -- %s", got, want, whereDiagStopped(f))
	}

	// The client acknowledges on its next heartbeat, exactly as pkg/client does.
	heartbeatCommands(t, f.edge, f.sessionToken, []string{requestID})

	waitUntil(t, func() string {
		return "central to record the delivery of " + requestID +
			" once the client acknowledged it to the edge -- " + whereDiagStopped(f) +
			"; central's audit log holds " + listOrNothing(auditActionNames(t, f.central, f.userID))
	}, func() bool {
		return hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditDelivered) != nil
	})

	entry := hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditDelivered)
	if !strings.Contains(entry.Details, requestID) {
		t.Errorf("the delivery entry does not name the request: %q", entry.Details)
	}
	if !strings.Contains(entry.Details, f.edgeNodeID) {
		t.Errorf("the delivery entry does not say which edge relayed it: %q", entry.Details)
	}
	if entry.ActorID != "edgeadmin@example.com" {
		t.Errorf("the delivery was attributed to %q, not the admin who asked", entry.ActorID)
	}
	// And it is retired on both sides, so a second ask is a second request rather than
	// "already requested" forever.
	if ids := queuedDiagIDs(f.central, f.userID); len(ids) != 0 {
		t.Errorf("central still holds %v after the delivery was recorded", ids)
	}
}

// auditActionNames is the audit log reduced to the actions it holds, for failure messages.
func auditActionNames(t *testing.T, srv *Server, targetID string) []string {
	t.Helper()
	var out []string
	for _, e := range auditActions(t, srv, targetID) {
		out = append(out, e.Action)
	}
	return out
}

// TestEdgeServedCollectionRelaysTheBundleToCentral is the result coming back.
//
// A request that is delivered and whose bundle never returns is worse than one that was never
// delivered: the admin is told it was requested, the client collects and redacts its logs, and
// then nothing arrives and nothing says why. Before this the edge answered the upload 501 --
// it has no database -- and the client logged a failure no admin ever sees.
func TestEdgeServedCollectionRelaysTheBundleToCentral(t *testing.T) {
	f := startEdgeDiagFleet(t)

	reply := requestCollection(t, f)
	requestID, _ := reply["request_id"].(string)
	waitForEdgeCommand(t, f, requestID)

	// The upload goes to the gateway serving the session, which is the edge -- pkg/client
	// posts to serverURL, and for an edge-served tunnel that is the edge. Driven through
	// ServeHTTP so the ROUTE is exercised: a handler that exists but is not reachable passes
	// every unit test and 404s in production.
	code, body := uploadBundle(t, f.edge, f.sessionToken, requestID, "client.log", "hello from the edge")
	if code != http.StatusOK {
		t.Fatalf("the edge answered the upload %d (%s) -- the client's logs stop here, and the "+
			"admin sees a collection that produced nothing", code, body)
	}

	bundles, err := f.central.db.ListDiagnosticsBundles(f.userID)
	if err != nil {
		t.Fatalf("listing stored bundles: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("central stored %d bundle(s), want 1 -- the relay is what puts them there, "+
			"since an edge has nowhere to keep one", len(bundles))
	}
	if bundles[0].UserID != f.userID {
		t.Errorf("the bundle was filed against %q, not %q", bundles[0].UserID, f.userID)
	}
	if bundles[0].RequestedBy != "edgeadmin@example.com" {
		t.Errorf("the bundle records %q as the requester; the edge never knew who asked, so this "+
			"can only come from central's own copy of the request", bundles[0].RequestedBy)
	}

	entry := hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditUploaded)
	if entry == nil {
		t.Fatalf("no upload was audited; central's log holds %v", auditActionNames(t, f.central, f.userID))
	}
	if !strings.Contains(entry.Details, "edge") {
		t.Errorf("the upload entry does not record that it was relayed: %q", entry.Details)
	}

	// Idempotent, the same way the direct path is: the command is the authorisation and it
	// is single-use, so a client retrying after a timeout cannot store a second copy.
	if code, _ := uploadBundle(t, f.edge, f.sessionToken, requestID, "client.log", "again"); code != http.StatusForbidden {
		t.Errorf("a repeat upload of %s was answered %d, want 403", requestID, code)
	}
}

// uploadBundle posts a bundle the way pkg/client does, through the gateway's real routing.
func uploadBundle(t *testing.T, srv *Server, sessionToken, requestID, kind, content string) (int, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"request_id": requestID,
		"logs":       []map[string]any{{"kind": kind, "content": content}},
	})
	if err != nil {
		t.Fatalf("marshalling the bundle: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://us.lfr-demo.se/api/client/diagnostics/upload",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-Token", sessionToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestCollectionIsNotQueuedForAnEdgeThatCannotBeReached holds the honesty property #1763 built
// and this change must not spend.
//
// Every edge is powered off nightly, so an edge lease on central outliving that node's control
// connection is the normal state for eight hours a day. Queueing into that would give the admin
// a request id, no delivery, and an "expired" entry five minutes later.
func TestCollectionIsNotQueuedForAnEdgeThatCannotBeReached(t *testing.T) {
	f := startEdgeDiagFleet(t)

	// The node goes away while its lease record on central remains -- exactly what a
	// scheduled stop does.
	f.edge.Stop()
	waitUntil(t, stated("central to notice edge-us's control connection close"), func() bool {
		return !edgeIsRegistered(f.central, "edge-us")
	})

	reply := requestCollection(t, f)
	if delivery, _ := reply["delivery"].(string); delivery != diagnosticsDeliveryNotReachable {
		t.Fatalf("delivery = %q, want %q -- there is no way down to a node with no control "+
			"connection, and saying otherwise is a request that goes nowhere", delivery, diagnosticsDeliveryNotReachable)
	}
	detail, _ := reply["delivery_detail"].(string)
	if !strings.Contains(detail, f.edgeNodeID) {
		t.Errorf("the admin was not told which node is unreachable: %q", detail)
	}
	if ids := queuedDiagIDs(f.central, f.userID); len(ids) != 0 {
		t.Fatalf("central queued %v for a user it cannot reach; it would block the next attempt "+
			"with \"already requested\" and then be audited undelivered", ids)
	}
}

// TestForwardedCollectionFrameCarriesNoCommandName is the bounding test.
//
// diagnostics_transport.go states why collect_logs is the only command -- "a general 'run this'
// channel to every client is a foothold, and an admin account is not a safe place to put one" --
// and a forwarding frame is exactly where that constraint would quietly be spent. The frame
// carries an id, a user and a window; the VERB is the frame type, and the edge synthesises the
// one command that exists. Adding a field an operator could put a second command in would make
// this test red, which is the point of it.
func TestForwardedCollectionFrameCarriesNoCommandName(t *testing.T) {
	raw, err := json.Marshal(ControlMessage{
		Type:              diagnosticsCollectFrameType,
		UserID:            "someone@example.com",
		DiagRequestID:     "a1b2c3d4e5f6",
		DiagExpiresInSecs: 300,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"type", "user_id", "diag_request_id", "diag_expires_in"} {
		if _, ok := wire[field]; !ok {
			t.Errorf("the frame omits %q, which the edge reads", field)
		}
	}
	for field, value := range wire {
		if s, ok := value.(string); ok && s == diagnosticsCommandCollectLogs && field != "type" {
			t.Errorf("field %q names the command to run; the frame type is the verb, and a field "+
				"that carries one is the general command channel this feature refuses to be", field)
		}
	}

	// And what an edge queues on receipt is that one command, taken from the constant rather
	// than from anything on the wire.
	edge := &Server{cfg: &config.ServerConfig{ControlPlaneURL: "http://central.invalid", EdgeToken: edgeDiagToken}}
	edge.queueForwardedDiagnosticsCollect("someone@example.com", "a1b2c3d4e5f6", diagnosticsCommandTTL)
	queued := edge.diagCommands["someone@example.com"]
	if len(queued) != 1 {
		t.Fatalf("the edge queued %d command(s), want 1", len(queued))
	}
	if queued[0].Type != diagnosticsCommandCollectLogs {
		t.Errorf("the edge queued type %q, want %q", queued[0].Type, diagnosticsCommandCollectLogs)
	}
	if queued[0].requestedBy != "" {
		t.Errorf("the edge was told who asked (%q); the admin's identity belongs in the audit "+
			"log, which is on central, and has no reader out here", queued[0].requestedBy)
	}

	// The window is central's to decide. An edge asked to hold a request open for a week
	// clamps to the one TTL this feature has, so no wire value can outlive the consent
	// guarantee the TTL exists to bound.
	edge.queueForwardedDiagnosticsCollect("someone@example.com", "ffffffffffff", 7*24*time.Hour)
	held := edge.diagCommands["someone@example.com"][0]
	if max := held.queuedAt.Add(diagnosticsCommandTTL); held.deadline().After(max) {
		t.Errorf("the edge holds %s until %s, past the %s TTL -- a forwarded window must not be "+
			"able to outlive the consent bound", held.ID, held.deadline(), diagnosticsCommandTTL)
	}
}

// TestForwardedRequestNobodyPickedUpIsAuditedExpired covers the absence.
//
// Expiry used to happen only when that user's client heartbeated the gateway holding the queue.
// A forwarded request is held by central while every one of that user's heartbeats goes to an
// edge, so nothing would ever have retired one: it would sit in central's map, never delivered,
// never audited, and still answering "already requested" to the next attempt.
func TestForwardedRequestNobodyPickedUpIsAuditedExpired(t *testing.T) {
	f := startEdgeDiagFleet(t)

	reply := requestCollection(t, f)
	requestID, _ := reply["request_id"].(string)
	waitForEdgeCommand(t, f, requestID)

	queuedAt := time.Now().UTC()

	// PREMISE, and it is the whole point of the grace: a command still inside its window --
	// or inside the wait for an ack that is already in flight -- must not be declared
	// undelivered. Without this half, a sweep that expired everything immediately would pass
	// the assertion below.
	f.central.sweepExpiredDiagnosticsCommands(queuedAt.Add(diagnosticsCommandTTL).Add(diagnosticsAckGrace / 2))
	if ids := queuedDiagIDs(f.central, f.userID); len(ids) == 0 {
		t.Fatalf("the request was retired while its acknowledgement could still have been in "+
			"flight; central would record %q for a collection that may have been delivered",
			diagnosticsAuditExpired)
	}
	if hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditExpired) != nil {
		t.Fatal("an expiry was audited inside the acknowledgement grace")
	}

	f.central.sweepExpiredDiagnosticsCommands(queuedAt.Add(diagnosticsCommandTTL).Add(diagnosticsAckGrace).Add(time.Second))

	if ids := queuedDiagIDs(f.central, f.userID); len(ids) != 0 {
		t.Errorf("central still holds %v past the deadline; the admin's next attempt is answered "+
			"\"already requested\" for a request that is dead", ids)
	}
	entry := hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditExpired)
	if entry == nil {
		t.Fatalf("nothing recorded that %s expired undelivered; central's log holds %v -- a "+
			"collection that silently never happened looks exactly like one that did",
			requestID, auditActionNames(t, f.central, f.userID))
	}
	if entry.ActorID != "edgeadmin@example.com" {
		t.Errorf("the expiry was attributed to %q, not the admin who is waiting on it", entry.ActorID)
	}
}

// TestAnAckThatCouldNotBeRelayedKeepsTheRequestQueued is the retry property.
//
// An edge whose control channel is down still serves its clients -- that is what the edges are
// FOR -- so a client can acknowledge a request during an outage. If the edge dropped the command
// on that ack, the evidence of delivery would be gone: unrelayable, unretryable, and central
// would eventually audit "expired undelivered" for a collection the client definitely received.
// So the command survives an ack that could not be reported, and rides the next heartbeat.
func TestAnAckThatCouldNotBeRelayedKeepsTheRequestQueued(t *testing.T) {
	f := startEdgeDiagFleet(t)

	reply := requestCollection(t, f)
	requestID, _ := reply["request_id"].(string)
	waitForEdgeCommand(t, f, requestID)

	// The control plane goes away AFTER the request arrived -- a restart, a deploy, a blip.
	// The edge keeps serving; only its uplink is gone.
	// The listener goes, then central's end of the websocket is closed -- which is what the
	// edge observes when central's process dies, and the only way to produce it in one
	// process: Stop() cancels contexts but nothing closes a HIJACKED connection, so the
	// handler goroutine would go on answering this edge's pings forever (measured -- an
	// earlier draft of this test waited ten seconds for a connection that was still alive).
	// The transport is what is being taken away; no diagnostics state is touched on either
	// side.
	f.centralTS.Close()
	f.central.edgeClientsMu.RLock()
	centralSide := f.central.edgeClients[f.edgeNodeID]
	f.central.edgeClientsMu.RUnlock()
	if centralSide == nil {
		t.Fatal("central holds no control connection for edge-us, so there is no outage to stage")
	}
	// Checked rather than discarded: the connection was live a line ago (centralTS.Close
	// takes the listener, not a hijacked connection), so a close that fails means the outage
	// this test stages did not happen and every assertion below would be measuring nothing.
	if err := centralSide.conn.Close(); err != nil {
		t.Fatalf("closing central's end of the control connection: %v -- the outage was not staged", err)
	}

	waitUntil(t, stated("the edge to notice its control connection is gone"), func() bool {
		f.edge.edgeUplinkMu.RLock()
		defer f.edge.edgeUplinkMu.RUnlock()
		return f.edge.edgeUplink == nil
	})

	heartbeatCommands(t, f.edge, f.sessionToken, []string{requestID})

	if ids := queuedDiagIDs(f.edge, f.userID); len(ids) != 1 || ids[0] != requestID {
		t.Fatalf("the edge holds %v after an acknowledgement it could not relay, want [%s] -- "+
			"the delivery is now unprovable and unretryable", ids, requestID)
	}
	got := heartbeatCommandsQuiet(f)
	want := requestID + ":" + diagnosticsCommandCollectLogs
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the next heartbeat carried %v, want [%s] -- at-least-once delivery is what "+
			"makes the unrelayed acknowledgement retryable", got, want)
	}
}

// TestRelayedUploadRequiresACommandCentralIssued pins the authorisation.
//
// The relay endpoint accepts a body that has already been through an edge, so the question of
// what authorises it is worth asserting rather than assuming. Two things do, and neither is the
// caller's say-so: the edge token proves the relay, and the REQUEST ID proves central asked.
// Central takes the user from its own copy of the request, so a relay cannot name one.
func TestRelayedUploadRequiresACommandCentralIssued(t *testing.T) {
	f := startEdgeDiagFleet(t)

	post := func(token, requestID string) int {
		payload, err := json.Marshal(map[string]any{
			"request_id": requestID,
			"logs":       []map[string]any{{"kind": "client.log", "content": "x"}},
		})
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "http://lfr-demo.se/api/internal/edge-diagnostics",
			bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Edge-Token", token)
		}
		rec := httptest.NewRecorder()
		f.central.ServeHTTP(rec, req)
		return rec.Code
	}

	reply := requestCollection(t, f)
	requestID, _ := reply["request_id"].(string)

	if code := post("", requestID); code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated relay was answered %d, want 401", code)
	}
	if code := post("not-a-real-edge-token", requestID); code != http.StatusUnauthorized {
		t.Errorf("a relay with the wrong edge token was answered %d, want 401", code)
	}
	if code := post(edgeDiagToken, "000000000000"); code != http.StatusForbidden {
		t.Errorf("a relay quoting a request id central never issued was answered %d, want 403 -- "+
			"the id is what stops this endpoint accepting an upload nobody asked for", code)
	}
	if code := post(edgeDiagToken, requestID); code != http.StatusOK {
		t.Errorf("a properly authorised relay was answered %d, want 200", code)
	}

	bundles, err := f.central.db.ListDiagnosticsBundles(f.userID)
	if err != nil {
		t.Fatalf("listing bundles: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("stored %d bundle(s) across four attempts, want exactly the 1 that was authorised", len(bundles))
	}
}

// TestRelayedUploadIsRefusedWhenConsentWentAway holds the consent guarantee on the new path.
//
// An edge cannot check consent -- it has no database -- so the check central makes on the relayed
// upload is the ONLY one standing between an edge-served collection and the store. #1696's
// promise is that a withdrawal takes effect immediately, and "immediately" must not acquire an
// exception for users who happen to be served by an edge.
func TestRelayedUploadIsRefusedWhenConsentWentAway(t *testing.T) {
	f := startEdgeDiagFleet(t)

	reply := requestCollection(t, f)
	requestID, _ := reply["request_id"].(string)
	waitForEdgeCommand(t, f, requestID)

	user, err := f.central.db.GetUser(f.userID)
	if err != nil {
		t.Fatalf("reading the user: %v", err)
	}
	user.DiagnosticsConsentAt = nil
	if err := f.central.db.UpdateUser(user); err != nil {
		t.Fatalf("withdrawing consent: %v", err)
	}

	code, body := uploadBundle(t, f.edge, f.sessionToken, requestID, "client.log", "secret")
	if code != http.StatusForbidden {
		t.Fatalf("an upload from a user who withdrew consent was answered %d (%s), want 403", code, body)
	}
	bundles, err := f.central.db.ListDiagnosticsBundles(f.userID)
	if err != nil {
		t.Fatalf("listing bundles: %v", err)
	}
	if len(bundles) != 0 {
		t.Fatalf("central stored %d bundle(s) for a user who had withdrawn consent", len(bundles))
	}
	if hasAuditAction(auditActions(t, f.central, f.userID), diagnosticsAuditRefused) == nil {
		t.Errorf("the refusal was not recorded; central's log holds %v", auditActionNames(t, f.central, f.userID))
	}
}
