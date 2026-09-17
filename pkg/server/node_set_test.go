package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

// newRosterServer is a control plane with two edges configured and neither connected.
func newRosterServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "edge-us", URL: "https://us.lfr-demo.se"},
		{ID: "edge-sa", URL: "https://sa.lfr-demo.se"},
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("creating the server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func edgeIsUp(srv *Server, nodeID string) {
	srv.edgeClientsMu.Lock()
	srv.edgeClients[nodeID] = &safeConn{}
	srv.edgeClientsMu.Unlock()
}

func edgeIsDown(srv *Server, nodeID string) {
	srv.edgeClientsMu.Lock()
	delete(srv.edgeClients, nodeID)
	srv.edgeClientsMu.Unlock()
}

// The signal has to move when, and only when, the set of gateways a client could elect moves
// (#1937). A fingerprint that did not change when an edge woke would be a mechanism that reports
// success and delivers nothing, which is exactly what this issue is about.
func TestNodeSetFingerprintTracksWhichEdgesAreUp(t *testing.T) {
	srv := newRosterServer(t)

	bothAsleep := srv.nodeSetFingerprint()
	if bothAsleep == "" {
		t.Fatal("a gateway holding a roster produced no fingerprint")
	}

	// PREMISE: asking twice with nothing changed must give the same answer, or the assertions
	// below would pass for any implementation at all -- including a random string.
	if again := srv.nodeSetFingerprint(); again != bothAsleep {
		t.Fatalf("the fingerprint is not stable: %q then %q", bothAsleep, again)
	}

	edgeIsUp(srv, "edge-us")
	usAwake := srv.nodeSetFingerprint()
	if usAwake == bothAsleep {
		t.Fatalf("edge-us waking left the fingerprint at %q -- a running client would never "+
			"learn that a gateway it can elect now exists", bothAsleep)
	}

	edgeIsUp(srv, "edge-sa")
	bothAwake := srv.nodeSetFingerprint()
	if bothAwake == usAwake || bothAwake == bothAsleep {
		t.Fatalf("a second edge waking did not move the fingerprint (%q)", bothAwake)
	}

	// And back: an edge going to sleep is a roster change too, and the value must return to
	// the one that described that roster rather than drifting off somewhere new.
	edgeIsDown(srv, "edge-sa")
	edgeIsDown(srv, "edge-us")
	if back := srv.nodeSetFingerprint(); back != bothAsleep {
		t.Errorf("the roster returned to its starting state but the fingerprint did not: %q, want %q", back, bothAsleep)
	}
}

// The fingerprint must describe what /api/version actually advertises, because that is the list
// the client elects from. Computed from the same function for that reason; this pins the property
// rather than the plumbing, so inlining the roster construction again would turn it red.
func TestNodeSetFingerprintDescribesWhatAPIVersionAdvertises(t *testing.T) {
	srv := newRosterServer(t)
	edgeIsUp(srv, "edge-sa")

	regions, unavailable := srv.advertisedRegions()

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Regions            map[string]string `json:"regions"`
		RegionsUnavailable map[string]string `json:"regions_unavailable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}

	if len(resp.Regions) != len(regions) {
		t.Fatalf("/api/version advertises %d regions, the fingerprint is computed over %d", len(resp.Regions), len(regions))
	}
	for name, url := range regions {
		if resp.Regions[name] != url {
			t.Errorf("regions[%q]: /api/version says %q, the fingerprint source says %q", name, resp.Regions[name], url)
		}
	}
	for name, url := range unavailable {
		if resp.RegionsUnavailable[name] != url {
			t.Errorf("regions_unavailable[%q]: /api/version says %q, the fingerprint source says %q", name, resp.RegionsUnavailable[name], url)
		}
	}
}

// The wiring, which is the part that actually breaks: a fingerprint the heartbeat never carries
// is a feature that silently does nothing, and every unit test above would still pass (§5b).
func TestHeartbeatCarriesTheNodeSetFingerprint(t *testing.T) {
	srv := newRosterServer(t)
	edgeIsUp(srv, "edge-sa")

	token, _, err := srv.registry.Register("user-1937", "node-set",
		[]PortMapping{{LocalPort: 3000}}, []string{"lfr-demo.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registering a tunnel: %v", err)
	}

	code, body := heartbeat(t, srv, token, nil)
	if code != http.StatusOK {
		t.Fatalf("the heartbeat returned %d, want 200", code)
	}

	got, _ := body[nodeSetFingerprintField].(string)
	if got == "" {
		t.Fatalf("the heartbeat carried no %q field: %v -- a running client is told nothing", nodeSetFingerprintField, body)
	}
	if want := srv.nodeSetFingerprint(); got != want {
		t.Fatalf("the heartbeat advertised %q, the roster hashes to %q", got, want)
	}

	// A top-level "status" would be read by the client's gatewayHasNoLease as "this gateway
	// holds no lease for me" and would re-register the tunnel every five seconds. Asserted here
	// because this change adds a field to that body.
	if _, present := body["status"]; present {
		t.Errorf("the heartbeat body grew a top-level status field: %v", body)
	}

	// And it moves on the wire, not merely in the helper.
	edgeIsUp(srv, "edge-us")
	_, after := heartbeat(t, srv, token, nil)
	if after[nodeSetFingerprintField] == got {
		t.Errorf("edge-us woke and the heartbeat still advertised %q", got)
	}
}

// BOUNDING (§5b.5), rewritten deliberately by #1960.
//
// It previously asserted that an edge advertises NO fingerprint, ever. That was the honest
// answer while nothing told an edge what the roster was: an edge holds no roster -- measured
// against production, in.lfr-demo.se answers /api/version naming only itself -- so anything it
// computed for itself would have been stable while the real roster moved underneath it.
//
// #1960 removed the premise rather than the limit. Central now pushes its fingerprint down the
// control channel, so an edge has a true value to echo and the old assertion went red, which is
// exactly what a bounding case is for. What remains bounded, and is what this test now pins, is
// that an edge NEVER makes one up: told nothing, it still says nothing. That covers an edge
// during startup, and an edge whose control channel has never come up -- which is what the
// closed loopback port below actually produces.
func TestEdgeToldNothingAdvertisesNoFingerprint(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	// Configured as an edge: a control plane to report to, and -- the load-bearing part --
	// no EdgeNodes of its own, which is what an edge's configuration actually looks like.
	// Pointed at a closed loopback port so the control channel fails immediately rather than
	// dialling the real production gateway from a unit test. Nothing can have told this node
	// anything, which is the state under test.
	cfg.ControlPlaneURL = "http://127.0.0.1:1"
	cfg.EdgeToken = "edge-token"
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("creating the edge: %v", err)
	}
	defer srv.Stop()

	if fp := srv.nodeSetFingerprint(); fp != "" {
		t.Fatalf("an edge central has not told anything advertised the fingerprint %q -- it "+
			"would be stable while the real roster changed underneath it, and clients would "+
			"trust it", fp)
	}

	token, _, err := srv.registry.Register("user-1937", "edge-served",
		[]PortMapping{{LocalPort: 3000}}, []string{"lfr-demo.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registering a tunnel: %v", err)
	}
	_, body := heartbeat(t, srv, token, nil)
	if _, present := body[nodeSetFingerprintField]; present {
		t.Errorf("an untold edge's heartbeat carried a node-set fingerprint: %v", body)
	}

	// And the other half of the same property: once central HAS told it, it echoes exactly
	// that and does not embellish it. The unit-level counterpart to the end-to-end coverage
	// in edge_node_set_test.go, which is where the value actually has to travel.
	srv.setUpstreamNodeSet("a1b2c3d4e5f6")
	if fp := srv.nodeSetFingerprint(); fp != "a1b2c3d4e5f6" {
		t.Errorf("an edge told %q advertises %q", "a1b2c3d4e5f6", fp)
	}
}
