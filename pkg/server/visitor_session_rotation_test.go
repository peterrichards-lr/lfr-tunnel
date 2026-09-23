package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"

	"github.com/gorilla/websocket"
)

// Rotating the fleet's visitor session-signing key without ending a single session (#2195).
//
// The property these tests are written against is one sentence: A ROTATION CHANGES THE KEY
// WITHOUT LOGGING ANYBODY OUT. "The rotation completed" is true of an implementation that
// invalidates the previous key at the commit and bounces every visitor to the passcode page,
// which is #2181 on the rotation's cadence -- so no test here asserts completion alone.

// withShortVisitorSessionAckTimeout shrinks the acknowledgement deadline for one test.
//
// Only the WAIT is shortened. Nothing about what counts as an acknowledgement changes, so a test
// that aborts here aborts in production too, just ten seconds later.
func withShortVisitorSessionAckTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	visitorSessionTunableMu.Lock()
	previous := visitorSessionAckTimeout
	visitorSessionAckTimeout = timeout
	visitorSessionTunableMu.Unlock()
	t.Cleanup(func() {
		visitorSessionTunableMu.Lock()
		visitorSessionAckTimeout = previous
		visitorSessionTunableMu.Unlock()
	})
}

// edgeTokenFor is the token an edge node authenticates with in these fixtures.
func edgeTokenFor(nodeID string) string { return nodeID + "-mysecrettokenvalue" }

// aControlPlane starts a real control plane with a database, configured for the given edge node
// ids. Configured is not the same as connected, and the difference is constraint 2.
func aControlPlane(t *testing.T, nodeIDs ...string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.DisableBackupScheduler = true
	for _, nodeID := range nodeIDs {
		hash := sha256.Sum256([]byte(edgeTokenFor(nodeID)))
		cfg.EdgeNodes = append(cfg.EdgeNodes, config.EdgeNodeConfig{ID: nodeID, TokenHash: hex.EncodeToString(hash[:])})
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create the control plane: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	})
	if srv.db == nil {
		t.Fatal("the control-plane fixture has no database, so it cannot own a key set")
	}
	return srv, ts
}

// aConnectedEdge starts a real edge -- no database, the real control channel -- and waits until
// the control plane sees it. It is the production symbol on both sides: an edge told the keys
// applies them and acknowledges, exactly as one in Sao Paulo does.
func aConnectedEdge(t *testing.T, central *Server, ts *httptest.Server, nodeID string) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.DBPath = "" // an edge has no database, which is why it has to be told
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.ControlPlaneURL = ts.URL
	cfg.EdgeToken = edgeTokenFor(nodeID)
	cfg.DisableBackupScheduler = true

	edge, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create edge %s: %v", nodeID, err)
	}
	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
		edge.Stop()
	})
	if edge.db != nil {
		t.Fatalf("the edge fixture for %s has a database, so it does not exercise the case this design exists for", nodeID)
	}

	waitUntil(t, stated("edge "+nodeID+" to hold a control connection to the control plane"), func() bool {
		for _, connected := range central.connectedEdgeNodeIDs() {
			if connected == nodeID {
				return true
			}
		}
		return false
	})
	waitUntil(t, stated("edge "+nodeID+" to acknowledge the key set it was handed at its handshake"), func() bool {
		_, seen := central.visitorSessionAcks.get(nodeID)
		return seen
	})
	return edge
}

// aSilentEdgeNode is a node that authenticates for real and then never says anything again.
//
// A real websocket completing the real HMAC challenge, so central counts it as connected and a
// rotation gates on it -- but it drains frames without applying or acknowledging them. This is
// the wedged node a fail-closed commit exists to catch, and no production symbol is stubbed to
// produce it.
func aSilentEdgeNode(t *testing.T, central *Server, ts *httptest.Server, nodeID string) {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("could not parse the control plane's address: %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(
		fmt.Sprintf("ws://%s/api/internal/edge-control-ws?node_id=%s", u.Host, nodeID), nil)
	if err != nil {
		t.Fatalf("the silent node could not dial the control channel: %v", err)
	}
	// Handled, not blanked: errcheck runs with check-blank, and a close that fails here usually
	// means the connection this test stages was already gone.
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Logf("closing the silent node's control connection: %v", err)
		}
	})

	var challenge struct {
		Type  string `json:"type"`
		Nonce string `json:"nonce"`
	}
	if err := conn.ReadJSON(&challenge); err != nil {
		t.Fatalf("the silent node could not read the auth challenge: %v", err)
	}
	key := sha256.Sum256([]byte(edgeTokenFor(nodeID)))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(challenge.Nonce))
	if err := conn.WriteJSON(map[string]string{"type": "auth", "response": hex.EncodeToString(mac.Sum(nil))}); err != nil {
		t.Fatalf("the silent node could not answer the auth challenge: %v", err)
	}

	// Drains for the life of the test. A node that stopped reading would have central's writes
	// block on a full socket buffer, and the rotation would then abort because the PUSH failed
	// rather than because the node stayed silent -- two different findings that must not be
	// able to wear each other's clothes (§5c rule 5).
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	waitUntil(t, stated("the silent node "+nodeID+" to be counted as connected by the control plane"), func() bool {
		for _, connected := range central.connectedEdgeNodeIDs() {
			if connected == nodeID {
				return true
			}
		}
		return false
	})
}

// An acknowledgement must be evidence of STATE, not of receipt.
//
// The discriminator is the node-local bootstrap key. It exists only in the edge's own handler --
// central never sends it, and SetVisitorSessionSecrets refuses a set that names it -- so an
// acknowledgement that contains it was read back out of the live handler, and one that does not
// was echoed from the frame that arrived. An echoed acknowledgement would be sent by a node that
// received the keys and then refused them, which is exactly the node the commit gate exists to
// catch (§5c: ask what the broken implementation does to your assertion).
func TestAnAcknowledgementIsReadBackFromTheHandlerNotEchoedFromTheFrame(t *testing.T) {
	central, ts := aControlPlane(t, "usedge")
	aConnectedEdge(t, central, ts, "usedge")

	var ack visitorSessionAck
	waitUntil(t, stated("usedge to acknowledge the key set it was handed at its handshake"), func() bool {
		var seen bool
		ack, seen = central.visitorSessionAcks.get("usedge")
		return seen
	})

	if !ack.holds(nodeLocalSessionKeyID) {
		t.Errorf("the acknowledgement lists %v, which is exactly what central sent -- it is an echo of the frame rather than a read-back of "+
			"what the node applied, so a node that REFUSED the key set would acknowledge it just the same", ack.Accepted)
	}
	if ack.CurrentID != central.visitorSessionSecrets.get().CurrentID {
		t.Errorf("the node reports it is minting with %q while the control plane's current generation is %q",
			ack.CurrentID, central.visitorSessionSecrets.get().CurrentID)
	}
}

// MANDATORY TEST 1. Rotation must COMMIT while a configured node is powered off.
//
// edge-us and edge-sa power off nightly (edge-sync: "Every Edge is powered off nightly"). A
// commit gated on every CONFIGURED node would fail closed every night while appearing to work,
// and a daily schedule would very likely land inside that window.
func TestARotationCommitsWhileAConfiguredNodeIsPoweredOff(t *testing.T) {
	// Two nodes configured. Only one of them ever connects; "sleeper" is powered off, exactly
	// as edge-sa is between 00:00 and 08:00 local.
	central, ts := aControlPlane(t, "usedge", "sleeper")
	edge := aConnectedEdge(t, central, ts, "usedge")

	before := central.visitorSessionSecrets.get().CurrentID
	if before == "" {
		t.Fatal("the control plane established no key set, so there is nothing to rotate")
	}
	if connected := central.connectedEdgeNodeIDs(); len(connected) != 1 || connected[0] != "usedge" {
		t.Fatalf("the fixture does not stage the case this test means: connected nodes are %v, wanted only usedge", connected)
	}

	outcome := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerPeriodic, visitorSessionPeriodicActor, nil)

	if !outcome.committed() {
		t.Fatalf("a rotation aborted because a CONFIGURED node was powered off: %s. "+
			"Readiness is gated on the configured node list rather than on currently-connected nodes, "+
			"so every periodic rotation between 00:00 and 08:00 fails closed while appearing to work", outcome.Reason)
	}
	// Not merely "it committed": the generation has to have actually moved, and it has to have
	// moved on the node that was awake.
	if outcome.Generation == before || central.visitorSessionSecrets.get().CurrentID != outcome.Generation {
		t.Fatalf("the rotation reported a commit but the minting generation did not move (%s -> %s, control plane now on %s)",
			before, outcome.Generation, central.visitorSessionSecrets.get().CurrentID)
	}
	if len(outcome.Acknowledged) != 1 || outcome.Acknowledged[0] != "usedge" {
		t.Errorf("the acknowledgement roster was %v, wanted exactly the one connected node", outcome.Acknowledged)
	}
	if len(outcome.Unacknowledged) != 0 {
		t.Errorf("the powered-off node was recorded as unacknowledged (%v); a node with no control connection is not a node the commit waits for", outcome.Unacknowledged)
	}

	waitUntil(t, stated("the awake edge to be minting with the new generation"), func() bool {
		current, _ := edge.proxyHandler.VisitorSessionGenerations()
		return current == outcome.Generation
	})
}

// MANDATORY TEST 2, and the one most likely to be written so it passes against a broken
// implementation. It asserts THE SESSION SURVIVES, not that the rotation completed.
//
// A cookie lives 24 hours. If the previous generation stopped verifying at the commit, every
// visitor holding one would be bounced to the passcode page at that instant. Retirement is a
// third phase and must lag the commit by longer than a cookie can live.
func TestASessionMintedBeforeARotationSurvivesTheCommitAndDiesOnlyAtRetirement(t *testing.T) {
	central, ts := aControlPlane(t, "usedge")
	edge := aConnectedEdge(t, central, ts, "usedge")

	outgoing := central.visitorSessionSecrets.get().CurrentID

	// A visitor enters the passcode BEFORE the rotation. Minted by the production symbol on the
	// real control plane, not assembled by the test.
	cookie, ok := central.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the control plane would not sign a session at all")
	}
	waitUntil(t, stated("the edge to honour the session minted before the rotation"), func() bool {
		return edge.proxyHandler.verifySessionCookie(cookie, "peters")
	})

	outcome := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerManual, "peter@example.com", nil)
	if !outcome.committed() {
		t.Fatalf("the rotation did not commit, so this test never reaches its subject: %s", outcome.Reason)
	}

	// THE ASSERTION. The visitor is still signed in, on both nodes, immediately after the
	// commit. A commit that retired the outgoing generation fails here.
	if !central.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Fatalf("a session minted before the rotation stopped working on the control plane the moment generation %s committed -- "+
			"the outgoing generation %s was retired AT the commit, so every visitor holding a cookie is bounced to the passcode page. "+
			"This is #2181 again, on the rotation's cadence", outcome.Generation, outgoing)
	}
	waitUntil(t, stated("the edge to have been told the committed key set"), func() bool {
		current, _ := edge.proxyHandler.VisitorSessionGenerations()
		return current == outcome.Generation
	})
	if !edge.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Fatalf("a session minted before the rotation stopped working on the edge once it switched to generation %s -- "+
			"the edge is verifying against its minting key instead of against the whole accepted set", outcome.Generation)
	}

	// And the switch really happened, so "the session survives" is not being satisfied by a
	// rotation that changed nothing. A gateway holding only the OUTGOING generation must reject
	// what the rotated fleet now mints.
	afterCookie, ok := central.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the control plane would not sign a session after the rotation")
	}
	onlyOutgoing := aGateway(t)
	stored := central.visitorSessionSecrets.get()
	for _, secret := range stored.Secrets {
		if secret.ID != outgoing {
			continue
		}
		if err := onlyOutgoing.SetVisitorSessionSecrets([]VisitorSessionSecret{secret}, outgoing); err != nil {
			t.Fatalf("could not stage a gateway holding only the outgoing generation: %v", err)
		}
	}
	if onlyOutgoing.verifySessionCookie(afterCookie, "peters") {
		t.Errorf("a gateway holding only the outgoing generation %s accepted a cookie minted after the commit, "+
			"so the fleet is still signing with the old generation and nothing was actually rotated", outgoing)
	}

	// Phase three has NOT happened yet. Driven with an explicit clock -- the sweep takes `now`
	// as an argument for exactly this reason -- rather than by rewriting the schedule, so what
	// is under test is the engine's own decision about when a generation may go.
	central.retireVisitorSessionGenerations(time.Now().UTC())
	if !central.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Fatalf("the retirement sweep dropped generation %s immediately after the commit; retirement must lag it by at least the %s a cookie can live",
			outgoing, visitorSessionCookieLifetime)
	}

	// Now it has. The WALL clock is untouched, so the cookie is still well inside its own
	// 24-hour expiry -- the only thing that can reject it here is the key having been retired.
	central.retireVisitorSessionGenerations(time.Now().UTC().Add(visitorSessionRetirementLag + time.Minute))
	if central.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Errorf("generation %s was still verifying %s after it stopped minting, so nothing ever retires a key and the old signing key lives forever",
			outgoing, visitorSessionRetirementLag)
	}
	// And the fleet is told, not just the control plane: an edge still honouring a retired key
	// is the split brain edge-sync exists to prevent.
	waitUntil(t, stated("the edge to stop honouring the retired generation"), func() bool {
		return !edge.proxyHandler.verifySessionCookie(cookie, "peters")
	})
	// The visitor who signed in AFTER the rotation is untouched by the retirement.
	if !central.proxyHandler.verifySessionCookie(afterCookie, "peters") {
		t.Error("retiring the outgoing generation also ended sessions signed with the CURRENT one")
	}
}

// MANDATORY TEST 3. An unacknowledged node aborts the commit, and the old key still mints.
func TestAnUnacknowledgedNodeAbortsTheCommitAndTheOldKeyStillMints(t *testing.T) {
	withShortVisitorSessionAckTimeout(t, 750*time.Millisecond)

	central, ts := aControlPlane(t, "usedge", "wedged")
	edge := aConnectedEdge(t, central, ts, "usedge")
	// Authenticated, connected, and silent: it will be pushed the new generation and will never
	// say whether it applied it.
	aSilentEdgeNode(t, central, ts, "wedged")

	outgoing := central.visitorSessionSecrets.get().CurrentID
	cookie, ok := central.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the control plane would not sign a session at all")
	}

	outcome := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerPeriodic, visitorSessionPeriodicActor, nil)

	if outcome.committed() {
		t.Fatalf("the commit went ahead with a connected node that never acknowledged the new generation %s -- "+
			"the fleet is now split, with some nodes minting a generation the wedged node has never heard of", outcome.Generation)
	}
	// The reason, not merely "it aborted": several guards in this engine abort, and "it did not
	// commit" is satisfied by any of them.
	if !strings.Contains(outcome.Reason, "did not confirm they hold generation") {
		t.Errorf("the rotation aborted for the wrong reason: %q", outcome.Reason)
	}
	if len(outcome.Unacknowledged) != 1 || outcome.Unacknowledged[0].NodeID != "wedged" {
		t.Fatalf("the aborted rotation named %v as unacknowledged, wanted exactly the wedged node", outcome.Unacknowledged)
	}
	if outcome.Unacknowledged[0].Why == "" {
		t.Error("the audit event records WHICH node did not acknowledge but not WHY; on an abort the reason is the whole value of the event")
	}
	if len(outcome.Acknowledged) != 1 || outcome.Acknowledged[0] != "usedge" {
		t.Errorf("the node that DID acknowledge was recorded as %v, wanted usedge; an abort must still say who was ready", outcome.Acknowledged)
	}

	// FAIL CLOSED: the old generation is still the one that mints, on the control plane and on
	// the edge that was perfectly healthy.
	if got := central.visitorSessionSecrets.get().CurrentID; got != outgoing {
		t.Fatalf("the control plane moved to generation %s despite the abort; the switch must not happen at all", got)
	}
	if !central.proxyHandler.verifySessionCookie(cookie, "peters") {
		t.Error("a session minted before the aborted rotation stopped working, so an abort is not free")
	}
	afterAbort, ok := central.proxyHandler.createSessionCookie("peters")
	if !ok {
		t.Fatal("the control plane stopped signing sessions after an aborted rotation")
	}
	onlyOutgoing := aGateway(t)
	for _, secret := range central.visitorSessionSecrets.get().Secrets {
		if secret.ID != outgoing {
			continue
		}
		if err := onlyOutgoing.SetVisitorSessionSecrets([]VisitorSessionSecret{secret}, outgoing); err != nil {
			t.Fatalf("could not stage a gateway holding only the outgoing generation: %v", err)
		}
	}
	if !onlyOutgoing.verifySessionCookie(afterAbort, "peters") {
		t.Errorf("after the abort the control plane is minting with something other than generation %s -- "+
			"'do not switch' has to mean the OLD key goes on signing, not merely that the database was not updated", outgoing)
	}
	waitUntil(t, stated("the healthy edge to still be minting with the outgoing generation"), func() bool {
		current, _ := edge.proxyHandler.VisitorSessionGenerations()
		return current == outgoing
	})
}

// A central restart must neither skip nor double-fire a scheduled rotation.
//
// Driven through the production sweep with an explicit clock, against a real database that is
// closed and reopened -- which is what a restart is.
func TestARestartNeitherSkipsNorDoubleFiresAScheduledRotation(t *testing.T) {
	central, _ := aControlPlane(t)

	state := loadVisitorSessionRotationState(central.db)
	if state.NextRotationAt.IsZero() {
		t.Fatal("a fresh control plane anchored no rotation schedule, so a periodic rotation would never fire")
	}
	// Not at startup. A rotation on every start is a rotation on every deploy.
	if !state.NextRotationAt.After(time.Now().UTC()) {
		t.Fatalf("the first rotation is due %s, in the past: every restart would rotate", state.NextRotationAt)
	}

	// A restart BEFORE the due time must not fire and must not move the anchor.
	central.initVisitorSessionRotation(central.db)
	if again := loadVisitorSessionRotationState(central.db); !again.NextRotationAt.Equal(state.NextRotationAt) {
		t.Errorf("restarting moved the next rotation from %s to %s, so a control plane that deploys more often than it rotates never rotates",
			state.NextRotationAt, again.NextRotationAt)
	}
	before := central.visitorSessionSecrets.get().CurrentID
	central.sweepVisitorSessionRotation(context.Background(), time.Now().UTC())
	if central.visitorSessionSecrets.get().CurrentID != before {
		t.Error("a rotation fired before its due time")
	}

	// The due time passes while central is down: the first tick after it comes back fires.
	overdue := state.NextRotationAt.Add(time.Minute)
	central.sweepVisitorSessionRotation(context.Background(), overdue)
	rotated := central.visitorSessionSecrets.get().CurrentID
	if rotated == before {
		t.Fatalf("an overdue rotation did not fire; the schedule is a countdown that restarts at zero rather than an absolute instant, so a control plane that restarts daily never rotates")
	}

	// And it does not fire again. The due time was advanced and persisted BEFORE the rotation
	// ran, so a restart immediately afterwards reads a due time in the future.
	advanced := loadVisitorSessionRotationState(central.db)
	if !advanced.NextRotationAt.After(overdue) {
		t.Fatalf("after firing, the next rotation is due %s which is not after %s -- it would fire again on the very next tick", advanced.NextRotationAt, overdue)
	}
	central.sweepVisitorSessionRotation(context.Background(), overdue.Add(time.Second))
	if central.visitorSessionSecrets.get().CurrentID != rotated {
		t.Error("the rotation fired twice for one due time")
	}

	// A LONG outage fires once, not once per missed interval. Five days of downtime must not
	// burn five generations out of a set bounded at four.
	longOutage := advanced.NextRotationAt.Add(5 * visitorSessionRotationInterval)
	central.sweepVisitorSessionRotation(context.Background(), longOutage)
	afterOutage := central.visitorSessionSecrets.get().CurrentID
	if afterOutage == rotated {
		t.Fatal("no rotation fired after a long outage")
	}
	next := loadVisitorSessionRotationState(central.db)
	if !next.NextRotationAt.After(longOutage) {
		t.Errorf("after a long outage the next rotation is due %s, not after %s -- the engine is catching up one missed interval at a time",
			next.NextRotationAt, longOutage)
	}
}

// The audit event has to say everything the issue asks for, and the owner asked specifically to
// be able to tell a manual rotation from a periodic one.
func TestTheAuditEventDistinguishesManualFromPeriodic(t *testing.T) {
	central, _ := aControlPlane(t)

	manual := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerManual, "peter@example.com", nil)
	if !manual.committed() {
		t.Fatalf("the manual rotation aborted: %s", manual.Reason)
	}
	periodic := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerPeriodic, visitorSessionPeriodicActor, nil)
	if !periodic.committed() {
		t.Fatalf("the periodic rotation aborted: %s", periodic.Reason)
	}

	// Read back through the production filter the portal uses, not by querying the table with a
	// statement of the test's own.
	entries, err := central.db.ListAuditEntries(db.AuditFilter{Action: visitorSessionAuditRotated, Limit: 50})
	if err != nil {
		t.Fatalf("could not read the audit log: %v", err)
	}
	byGeneration := map[string]string{}
	details := map[string]string{}
	for _, e := range entries {
		byGeneration[e.TargetID] = e.ActorID
		details[e.TargetID] = e.Details
	}

	if got := byGeneration[manual.Generation]; got != "peter@example.com" {
		t.Errorf("the manual rotation was filed under actor %q, wanted the administrator who asked for it; "+
			"actor_id is the field ListAuditEntries can filter on, so this is what makes manual and periodic separable", got)
	}
	if got := byGeneration[periodic.Generation]; got != visitorSessionPeriodicActor {
		t.Errorf("the periodic rotation was filed under actor %q, wanted %q", got, visitorSessionPeriodicActor)
	}
	if !strings.Contains(details[manual.Generation], "Manual rotation requested by peter@example.com") {
		t.Errorf("the manual rotation's details do not name the trigger or the actor: %q", details[manual.Generation])
	}
	if !strings.Contains(details[periodic.Generation], "Periodic rotation") {
		t.Errorf("the periodic rotation's details do not name the trigger: %q", details[periodic.Generation])
	}
	// The generation moved to, and the fact the previous one survives the commit.
	for _, outcome := range []visitorSessionRotationOutcome{manual, periodic} {
		if !strings.Contains(details[outcome.Generation], outcome.PreviousGeneration) {
			t.Errorf("the audit entry for %s does not name the generation it moved FROM: %q", outcome.Generation, details[outcome.Generation])
		}
	}
}

// An abort is audited too, and the reason is the whole value of the event.
func TestAnAbortedRotationIsAudited(t *testing.T) {
	withShortVisitorSessionAckTimeout(t, 750*time.Millisecond)

	central, ts := aControlPlane(t, "wedged")
	aSilentEdgeNode(t, central, ts, "wedged")

	outcome := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerPeriodic, visitorSessionPeriodicActor, nil)
	if outcome.committed() {
		t.Fatal("the rotation committed with a node that never acknowledged; this test never reaches its subject")
	}

	entries, err := central.db.ListAuditEntries(db.AuditFilter{Action: visitorSessionAuditAborted, Limit: 50})
	if err != nil {
		t.Fatalf("could not read the audit log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("an aborted rotation wrote %d audit entries, wanted exactly 1 -- an abort nobody can see is the failure mode this repo keeps finding", len(entries))
	}
	got := entries[0].Details
	for _, want := range []string{"Periodic rotation", "Aborted", "wedged", "Reason:"} {
		if !strings.Contains(got, want) {
			t.Errorf("the aborted rotation's audit entry does not mention %q: %q", want, got)
		}
	}
	// And it is readable back through the persisted state, which is what the portal renders.
	state := loadVisitorSessionRotationState(central.db)
	if state.Last == nil || state.Last.Outcome != visitorSessionOutcomeAborted || state.Last.Reason == "" {
		t.Errorf("the aborted rotation was not recorded as the last outcome, so a portal would show nothing: %+v", state.Last)
	}
}

// Retirement must lag the commit by at least the cookie lifetime. Asserted as a property of the
// constants rather than as an observation about one run, so any future edit that shortens either
// one fails here rather than in production.
func TestRetirementCannotOutrunTheCookieLifetime(t *testing.T) {
	if visitorSessionRetirementLag < visitorSessionCookieLifetime {
		t.Fatalf("a generation is retired %s after it stops minting but a cookie lives %s, so every rotation logs out the visitors holding one",
			visitorSessionRetirementLag, visitorSessionCookieLifetime)
	}
	// The cookie lifetime constant has to be the one createSessionCookie actually stamps, or
	// the guard above is comparing against a number nothing uses.
	p := aGateway(t)
	cookie, ok := p.createSessionCookie("peters")
	if !ok {
		t.Fatal("the gateway would not sign a session")
	}
	parts := strings.Split(cookie, ":")
	if len(parts) != 3 {
		t.Fatalf("unexpected cookie shape: %q", cookie)
	}
	var expiry int64
	if _, err := fmt.Sscanf(parts[1], "%d", &expiry); err != nil {
		t.Fatalf("could not read the cookie's expiry: %v", err)
	}
	life := time.Until(time.Unix(expiry, 0))
	if life < visitorSessionCookieLifetime-time.Minute || life > visitorSessionCookieLifetime+time.Minute {
		t.Errorf("createSessionCookie stamps a %s life but visitorSessionCookieLifetime says %s; the retirement lag is derived from a number nothing uses",
			life.Round(time.Second), visitorSessionCookieLifetime)
	}
}

// The accepted set is bounded at four and a rotation adds to it. A rotation that would breach the
// bound must abort rather than produce a set every node refuses.
func TestARotationThatWouldBreachTheAcceptedBoundAborts(t *testing.T) {
	central, _ := aControlPlane(t)

	// Four rotations back to back, with nothing retiring in between: the fifth has nowhere to
	// put a new generation.
	var last visitorSessionRotationOutcome
	for i := 0; i < maxAcceptedVisitorSessionSecrets+1; i++ {
		last = central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerManual, "peter@example.com", nil)
		if !last.committed() {
			break
		}
	}
	if last.committed() {
		t.Fatalf("rotations kept committing past the %d-key bound, so the fleet is being handed a set every node refuses", maxAcceptedVisitorSessionSecrets)
	}
	if !strings.Contains(last.Reason, "more than the") {
		t.Errorf("the rotation aborted for the wrong reason: %q", last.Reason)
	}
	// Fail closed: the last committed generation is still minting.
	if _, ok := central.proxyHandler.createSessionCookie("peters"); !ok {
		t.Error("the control plane stopped signing sessions once rotation hit its bound")
	}
	if len(central.visitorSessionSecrets.get().Secrets) > maxAcceptedVisitorSessionSecrets {
		t.Errorf("the stored set grew to %d keys, past the %d a node will accept",
			len(central.visitorSessionSecrets.get().Secrets), maxAcceptedVisitorSessionSecrets)
	}
}

// Nothing on the rotation's paths may carry key material: not the acknowledgement frame, not the
// persisted bookkeeping, not the admin API's response. Both recent credential leaks (#2135,
// #2137) were a struct that reached a format string long after the line was written.
func TestNoRotationPathCarriesAKey(t *testing.T) {
	central, _ := aControlPlane(t)
	outcome := central.RotateVisitorSessionSecret(context.Background(), visitorSessionTriggerManual, "peter@example.com", nil)
	if !outcome.committed() {
		t.Fatalf("the rotation aborted: %s", outcome.Reason)
	}

	stored := central.visitorSessionSecrets.get()
	if len(stored.Secrets) == 0 {
		t.Fatal("no keys to check against")
	}

	// Every rendering a key could escape through, checked against every key actually held.
	rendered := map[string]string{}
	outcomeJSON, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("could not encode the rotation outcome: %v", err)
	}
	rendered["the rotation outcome the admin API returns"] = string(outcomeJSON)

	raw, _, err := central.db.GetAdminSettingOptional(visitorSessionRotationSettingKey)
	if err != nil {
		t.Fatalf("could not read the rotation bookkeeping row: %v", err)
	}
	rendered["the persisted rotation bookkeeping"] = raw

	// The production builder, not a copy of it assembled here: a test that reassembles the
	// frame proves only that the test omits the keys (§5c rule 4).
	current, accepted := central.proxyHandler.VisitorSessionGenerations()
	ackJSON, err := json.Marshal(visitorSessionAckFrame(current, accepted))
	if err != nil {
		t.Fatalf("could not encode an acknowledgement frame: %v", err)
	}
	rendered["the acknowledgement frame an edge sends upwards"] = string(ackJSON)
	rendered["the audit entry"] = outcome.auditDetails()

	for where, text := range rendered {
		for _, secret := range stored.Secrets {
			if strings.Contains(text, secret.Key) {
				t.Errorf("%s contains signing key material for generation %s", where, secret.ID)
			}
		}
	}
	// And the generation ids ARE there -- they are labels, and redacting them would leave an
	// audit trail that cannot name what it is about.
	if !strings.Contains(rendered["the audit entry"], outcome.Generation) {
		t.Errorf("the audit entry does not name the generation it moved to: %q", rendered["the audit entry"])
	}
}

// The acknowledgement frame has to stay well inside the control channel's read limit: gorilla
// CLOSES a connection that exceeds it, and this channel also carries kicks, schedules and
// blacklist pushes.
func TestTheAckFrameStaysInsideTheControlChannelReadLimit(t *testing.T) {
	payload, err := json.Marshal(visitorSessionAckFrame("d", []string{"a", "b", "c", "d", nodeLocalSessionKeyID}))
	if err != nil {
		t.Fatalf("could not encode an acknowledgement frame: %v", err)
	}
	if len(payload) > edgeControlReadLimit/8 {
		t.Errorf("an acknowledgement frame is %d bytes, uncomfortably close to the %d byte read limit that closes the connection",
			len(payload), edgeControlReadLimit)
	}
}

// The admin API is what the portal UI (#2196) drives, so it has to answer three questions
// without the UI having to know how the engine works: which generation is current, when the
// next rotation runs, and what the last one did.
func TestTheAdminEndpointsReadBackTheGenerationAndTheLastOutcome(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	_, adminSession := seedDiagnosticsUser(t, srv, "rotation-admin@example.com", "admin")

	rec := postAs(t, srv, srv.handleAdminVisitorSessionRotate, "/api/admin/session-secrets/rotate", adminSession, map[string]string{})
	if rec.Code != http.StatusOK {
		t.Fatalf("a manual rotation returned %d: %s", rec.Code, rec.Body.String())
	}
	var outcome visitorSessionRotationOutcome
	if err := json.Unmarshal(rec.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("could not decode the rotation outcome: %v", err)
	}
	if outcome.Trigger != visitorSessionTriggerManual || outcome.Actor != "rotation-admin@example.com" {
		t.Errorf("the endpoint recorded the rotation as %s by %q; a manual rotation must name the administrator who asked for it",
			outcome.Trigger, outcome.Actor)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "http://example.com/api/admin/session-secrets", nil)
	statusReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: adminSession})
	statusRec := httptest.NewRecorder()
	srv.handleAdminVisitorSessionRotationStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("the read-back returned %d: %s", statusRec.Code, statusRec.Body.String())
	}
	var status visitorSessionRotationStatus
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("could not decode the rotation status: %v", err)
	}
	if status.CurrentGeneration != outcome.Generation {
		t.Errorf("the read-back reports generation %q as current but the rotation moved to %q", status.CurrentGeneration, outcome.Generation)
	}
	if status.LastRotation == nil || status.LastRotation.Generation != outcome.Generation {
		t.Errorf("the read-back does not carry the last rotation's outcome, so an aborted rotation would be invisible to a portal: %+v", status.LastRotation)
	}
	if status.NextRotationAt.IsZero() {
		t.Error("the read-back names no next rotation, so a portal cannot say when one is due")
	}
	if _, retiring := status.RetiringAt[outcome.PreviousGeneration]; !retiring {
		t.Errorf("the read-back does not say when generation %s stops verifying; retirement is a scheduled event and the whole reason a session survives the commit",
			outcome.PreviousGeneration)
	}

	// No key material on either response.
	for _, secret := range srv.visitorSessionSecrets.get().Secrets {
		if strings.Contains(rec.Body.String(), secret.Key) || strings.Contains(statusRec.Body.String(), secret.Key) {
			t.Fatalf("an admin API response carries signing key material for generation %s", secret.ID)
		}
	}
}

// Rotating the fleet's signing key is not something a logged-in portal user may do.
//
// The role is re-checked against the database rather than inherited from requireAdmin, because
// requireAdmin's cookie path rewrites a stored role of "user" to "admin" (#1760).
func TestANonAdminCannotRotateTheSessionKey(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	_, userSession := seedDiagnosticsUser(t, srv, "ordinary@example.com", "user")
	before := srv.visitorSessionSecrets.get().CurrentID

	rec := postAs(t, srv, srv.handleAdminVisitorSessionRotate, "/api/admin/session-secrets/rotate", userSession, map[string]string{})
	if rec.Code != http.StatusForbidden {
		t.Errorf("an ordinary portal user rotating the fleet's signing key got %d, wanted 403: %s", rec.Code, rec.Body.String())
	}
	if got := srv.visitorSessionSecrets.get().CurrentID; got != before {
		t.Errorf("the generation moved from %s to %s on a refused request", before, got)
	}
}
