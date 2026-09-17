package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// Telling an EDGE-served client that the node set changed (#1960).
//
// #1937 put a roster fingerprint on the tunnel-status heartbeat, and a running client that sees
// it move re-probes. It only ever worked for clients served by a node holding a roster, which is
// central and only central: an edge is configured with no edge_nodes and has no database, so
// nodeSetFingerprint() returned "" there and an edge's heartbeats carried nothing. A client that
// elected an edge while another edge was asleep was never told the sleeping edge had woken.
//
// The fix follows the shape the schedule and the access-control rules already use rather than
// inventing a second one: central decides, the control channel carries it, the edge applies it.
// So the tests below are end-to-end over the REAL control channel with real edge servers, not a
// value poked into a field. That matters here specifically -- the defect was never in computing
// a fingerprint, it was that nothing carried one to the node that needed it, and a fixture that
// sets upstreamNodeSet directly would have passed against the bug for the whole of its life.

// waitForNodeSet polls until cond holds, or fails naming what never happened.
//
// Polled rather than slept: these paths cross two servers, a websocket and two goroutines, and a
// fixed sleep here would either be slow on every run or flaky on a loaded one.
func waitForNodeSet(t *testing.T, whatShouldHappen func() string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after 10s waiting for: %s", whatShouldHappen())
}

// stated is the fixed-string form of waitForNodeSet's message, for the waits whose failure has
// only one possible cause.
func stated(msg string) func() string { return func() string { return msg } }

// startCentralForNodeSet brings up a real control plane behind a real HTTP listener, which is
// what an edge's control channel dials.
func startCentralForNodeSet(t *testing.T, cfg *config.ServerConfig) (*Server, *httptest.Server) {
	t.Helper()
	central, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("creating the control plane: %v", err)
	}
	ts := httptest.NewServer(central)
	t.Cleanup(func() {
		ts.Close()
		central.Stop()
	})
	return central, ts
}

// heartbeatFingerprint is what a client on this gateway actually reads: the node-set field of a
// tunnel-status reply, or "" when the reply carries none.
func heartbeatFingerprint(t *testing.T, srv *Server, sessionToken string) string {
	t.Helper()
	code, body := heartbeat(t, srv, sessionToken, nil)
	if code != http.StatusOK {
		t.Fatalf("the heartbeat was answered %d, not 200 -- no client would read a fingerprint "+
			"from this at all", code)
	}
	fp, _ := body[nodeSetFingerprintField].(string)
	return fp
}

// whereItStopped names which leg of the propagation failed, so a timeout below identifies one
// defect rather than any of four (§5c).
//
// The value crosses three places: central computes it, the control channel carries it to the
// edge's store, and the edge's heartbeat echoes the store. A bare "it never arrived" is
// satisfied by a broadcast that never fired, a frame the edge has no case for, and a heartbeat
// that drops what the edge is holding -- all of which have a different fix.
func whereItStopped(edge, central *Server) string {
	return "central holds " + quoteFP(central.nodeSetFingerprint()) +
		", the edge's store holds " + quoteFP(edge.UpstreamNodeSet()) +
		", the edge's heartbeat carried " + quoteFP(edge.nodeSetFingerprint())
}

func quoteFP(fp string) string {
	if fp == "" {
		return "nothing"
	}
	return `"` + fp + `"`
}

// waitForHeartbeatFingerprint waits for the heartbeat to start carrying any fingerprint.
func waitForHeartbeatFingerprint(t *testing.T, edge, central *Server, sessionToken, whatShouldHappen string) string {
	t.Helper()
	var fp string
	waitForNodeSet(t, func() string {
		return whatShouldHappen + " -- " + whereItStopped(edge, central)
	}, func() bool {
		fp = heartbeatFingerprint(t, edge, sessionToken)
		return fp != ""
	})
	return fp
}

// waitForHeartbeatChange waits for the heartbeat to carry something other than previous, and
// fails naming the roster change that never reached the client.
func waitForHeartbeatChange(t *testing.T, edge, central *Server, sessionToken, previous, whatShouldHappen string) string {
	t.Helper()
	var fp string
	waitForNodeSet(t, func() string {
		return whatShouldHappen + " -- it stayed at " + quoteFP(previous) + "; " + whereItStopped(edge, central)
	}, func() bool {
		fp = heartbeatFingerprint(t, edge, sessionToken)
		return fp != "" && fp != previous
	})
	return fp
}

// nodeSetTestEdge is a real edge server: no database, pointed at a real control plane.
func nodeSetTestEdge(t *testing.T, controlPlaneURL, token, domain string) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.DBPath = "" // Edge mode: no database, exactly as production runs them.
	cfg.Domains = []string{domain}
	cfg.ControlPlaneURL = controlPlaneURL
	cfg.EdgeToken = token
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("creating the edge: %v", err)
	}
	return srv
}

func edgeIsRegistered(central *Server, nodeID string) bool {
	central.edgeClientsMu.RLock()
	defer central.edgeClientsMu.RUnlock()
	_, ok := central.edgeClients[nodeID]
	return ok
}

// TestEdgeEchoesCentralsNodeSetAsTheRosterMoves is the whole feature, end to end.
//
// Two real edges and a real control plane. edge-us serves a client; edge-sa arriving and leaving
// is the roster change that client has to hear about. Every assertion is made against what the
// client actually reads -- the tunnel-status heartbeat body -- rather than against the field
// behind it, because the field being right while the heartbeat carried nothing is precisely the
// state this issue describes (§5b).
func TestEdgeEchoesCentralsNodeSetAsTheRosterMoves(t *testing.T) {
	const usToken, saToken = "edge-us-secretvalue", "edge-sa-secretvalue"

	cfgCentral := config.DefaultServerConfig()
	cfgCentral.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgCentral.Domains = []string{"lfr-demo.se"}
	cfgCentral.DisableBackupScheduler = true
	cfgCentral.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hashOf(usToken), URL: "https://us.lfr-demo.se"},
		{ID: "edge-sa", TokenHash: hashOf(saToken), URL: "https://sa.lfr-demo.se"},
	}
	central, ts := startCentralForNodeSet(t, cfgCentral)

	edgeUS := nodeSetTestEdge(t, ts.URL, usToken, "us.lfr-demo.se")
	defer edgeUS.Stop()
	waitForNodeSet(t, stated("edge-us to authenticate with the control plane"), func() bool {
		return edgeIsRegistered(central, "edge-us")
	})

	// A client with a tunnel ON THE EDGE. This is what makes the heartbeat below the same
	// request a real client makes: handleTunnelStatus only returns a body at all for a
	// session this node holds a lease for.
	token, _, err := edgeUS.registry.Register("user-1960", "edge-served",
		[]PortMapping{{LocalPort: 3000}}, []string{"us.lfr-demo.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registering a tunnel on the edge: %v", err)
	}

	// One edge up. The edge must be advertising exactly what central advertises -- not merely
	// something non-empty, which a hardcoded constant would also satisfy.
	oneUp := waitForHeartbeatFingerprint(t, edgeUS, central, token,
		"edge-us to be told the node set once it had connected")
	if want := central.nodeSetFingerprint(); oneUp != want {
		t.Fatalf("the edge advertises %q, central advertises %q -- two clients on different "+
			"gateways would disagree about when the roster moved", oneUp, want)
	}

	// PREMISE: with nothing changed, the value must not move on its own, or every assertion
	// below would be satisfied by an implementation that returned a fresh random string.
	if again := heartbeatFingerprint(t, edgeUS, token); again != oneUp {
		t.Fatalf("the edge's fingerprint moved with the roster unchanged: %q then %q", oneUp, again)
	}

	// A second edge WAKES. This is the case the issue is about: the client on edge-us can now
	// elect edge-sa, and before this it had no way to find that out short of restarting.
	edgeSA := nodeSetTestEdge(t, ts.URL, saToken, "sa.lfr-demo.se")
	waitForNodeSet(t, stated("edge-sa to authenticate with the control plane"), func() bool {
		return edgeIsRegistered(central, "edge-sa")
	})

	bothUp := waitForHeartbeatChange(t, edgeUS, central, token, oneUp,
		"edge-us's heartbeat to report a new node set after edge-sa woke -- a client served by "+
			"edge-us would otherwise never learn that a gateway it can elect now exists")
	if want := central.nodeSetFingerprint(); bothUp != want {
		t.Errorf("after edge-sa woke the edge advertises %q, central advertises %q", bothUp, want)
	}

	// And back down. An edge going to sleep is a roster change too -- every edge is powered
	// off nightly -- and the value has to return to the one that described that roster rather
	// than drifting somewhere new.
	edgeSA.Stop()
	waitForNodeSet(t, stated("central to notice edge-sa's control connection close"), func() bool {
		return !edgeIsRegistered(central, "edge-sa")
	})

	backDown := waitForHeartbeatChange(t, edgeUS, central, token, bothUp,
		"edge-us's heartbeat to report a new node set after edge-sa dropped")
	if backDown != oneUp {
		t.Errorf("the roster returned to its starting state but the edge advertises %q, want %q",
			backDown, oneUp)
	}
}

// TestReloadingTheRosterRetellsTheEdges covers the change no connect or disconnect announces.
//
// An operator editing edge_nodes and sending SIGHUP changes what /api/version advertises without
// anything connecting or dropping, so the two broadcasts above cannot cover it. Left out, an edge
// would keep echoing the pre-reload roster until some unrelated edge event happened to refresh
// it -- which on a quiet fleet is the next morning.
func TestReloadingTheRosterRetellsTheEdges(t *testing.T) {
	const usToken = "edge-us-secretvalue"

	cfgCentral := config.DefaultServerConfig()
	cfgCentral.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgCentral.Domains = []string{"lfr-demo.se"}
	cfgCentral.DisableBackupScheduler = true
	cfgCentral.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hashOf(usToken), URL: "https://us.lfr-demo.se"},
	}
	central, ts := startCentralForNodeSet(t, cfgCentral)

	edgeUS := nodeSetTestEdge(t, ts.URL, usToken, "us.lfr-demo.se")
	defer edgeUS.Stop()
	waitForNodeSet(t, stated("edge-us to authenticate with the control plane"), func() bool {
		return edgeIsRegistered(central, "edge-us")
	})

	token, _, err := edgeUS.registry.Register("user-1960", "edge-served",
		[]PortMapping{{LocalPort: 3000}}, []string{"us.lfr-demo.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registering a tunnel on the edge: %v", err)
	}
	before := waitForHeartbeatFingerprint(t, edgeUS, central, token, "edge-us to be told the node set")

	// The connected node's URL moved, which is what the edge domain rename actually did to
	// all four edges. This is a change to `regions` -- the set a client may elect from --
	// and so a change every client has to hear about, because the address it would fail over
	// to is no longer the one it was given.
	//
	// Deliberately NOT "a second node added to the file": an unconnected node lands in
	// regions_unavailable, which nodeSetFingerprint does not hash, so that edit legitimately
	// moves nothing and this test would have been asserting on a value that never changed.
	// Measured, not assumed -- the premise check below caught exactly that draft.
	path := writeConfig(t, `
domains:
  - lfr-demo.se
edge_nodes:
  - id: "edge-us"
    token_hash: "`+hashOf(usToken)+`"
    url: "https://us-east-1.lfr-demo.se"
`)
	if err := central.ReloadEdgeNodes(path); err != nil {
		t.Fatalf("reloading the roster: %v", err)
	}
	// PREMISE: the reload has to have moved central's own answer, or the assertion below
	// would pass on a reload that changed nothing at all.
	if central.nodeSetFingerprint() == before {
		t.Fatalf("the reload left central's own fingerprint at %q -- this fixture is not "+
			"producing a roster change, so it cannot show one being propagated", before)
	}

	after := waitForHeartbeatChange(t, edgeUS, central, token, before,
		"edge-us's heartbeat to report the reloaded roster")
	if want := central.nodeSetFingerprint(); after != want {
		t.Errorf("after the reload the edge advertises %q, central advertises %q", after, want)
	}
}

// TestNodeSetFrameRoundTrip pins the wire format. The edge's switch keys on the type and reads
// one field, so a rename on either side is a frame every edge logs as unknown and discards --
// the failure #1245 documented, where central sent node_shutdown_warning for weeks to a fleet
// with no case for it.
func TestNodeSetFrameRoundTrip(t *testing.T) {
	raw, err := json.Marshal(ControlMessage{Type: nodeSetFrameType, NodeSet: "a1b2c3d4e5f6"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ControlMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "node_set" {
		t.Errorf("type = %q, want %q", got.Type, "node_set")
	}
	if got.NodeSet != "a1b2c3d4e5f6" {
		t.Errorf("node_set = %q, want %q", got.NodeSet, "a1b2c3d4e5f6")
	}
	// The field the client reads is "nodes" on the heartbeat and "node_set" on the control
	// channel; they are different wires and must not be assumed to be the same key.
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := wire["node_set"]; !ok {
		t.Errorf("the frame does not carry a node_set key: %v", wire)
	}
}
