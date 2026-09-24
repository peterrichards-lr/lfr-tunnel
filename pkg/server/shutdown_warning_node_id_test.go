package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
)

// The shutdown warning a CLIENT receives has to name the gateway that is going down (#2187).
//
// Two carriers exist for this warning and only one of them ever set the field: the
// ControlMessage central pushes down the edge control channel carries NodeID, and the body that
// rides the tunnel-status heartbeat -- the only one a client ever sees -- did not. So
// LFT_NODE_ID reached every warning_received hook empty, a field the client has decoded since
// #1708 and the documentation has promised for as long.
//
// These tests drive the production symbols on both sides: the server's real heartbeat body and
// the client's real ParseNodeShutdownWarning. Decoding into a mirror of the struct is how a
// field that production drops still looks present (§5c rule 4).

// THE CLASS, not the instance: every field the client's decoder declares must survive the trip.
//
// node_id is one member of it. The guard is written against the struct rather than against the
// key, so a field added to NodeShutdownWarning that the heartbeat body never sets fails here
// instead of reaching a user's hook as an empty string a year later.
//
// The fixture announces a drain with every optional field populated, so a zero value in the
// decoded struct means the server did not send it -- not that this test declined to ask for it.
func TestTheHeartbeatWarningCarriesEveryFieldTheClientDecodes(t *testing.T) {
	srv := newDrainTestServer(t)
	postDrain(t, srv, `{"seconds": 45, "reason": "Deploying"}`)

	body := srv.pendingShutdownWarning()
	if body == nil {
		t.Fatal("the heartbeat carries no warning at all, so there is nothing for a client to decode")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("the heartbeat body does not marshal: %v", err)
	}

	// The client's own parser, not a local copy of it.
	warning, ok := client.ParseNodeShutdownWarning(raw)
	if !ok {
		t.Fatalf("the client's parser does not recognise the body the server sends: %s", raw)
	}

	decoded := reflect.ValueOf(*warning)
	for i := 0; i < decoded.NumField(); i++ {
		field := decoded.Type().Field(i)
		if !decoded.Field(i).IsZero() {
			continue
		}
		t.Errorf("client.NodeShutdownWarning.%s (json tag %q) decoded to its zero value from %s.\n\n"+
			"The client declares the field, RunHook publishes it to every lifecycle hook, and the "+
			"gateway never puts it on the wire -- which is exactly how LFT_NODE_ID came to be "+
			"documented and always empty (#2187). Either the heartbeat body must set it or the "+
			"client must stop declaring it.",
			field.Name, field.Tag.Get("json"), raw)
	}
}

// node_id must name THIS gateway, and the value has to be the one the rest of the system uses
// for it -- the id central addressed the frame to, and the id this gateway stamps on the leases
// it creates (#1167).
//
// Asserted against a real EDGE, over the real control channel, because "the field is not empty"
// is satisfied by a gateway that hardcodes the control plane's own id. An edge-served client is
// also the case that matters: the control plane is not the node that gets powered off nightly.
func TestAnEdgesHeartbeatWarningNamesTheEdgeAndNotTheControlPlane(t *testing.T) {
	central, ts := aControlPlane(t, "edgesa")
	edge := aConnectedEdge(t, central, ts, "edgesa")

	// The production path: central warns one node, down the edge control channel.
	central.BroadcastNodeShutdownWarning("edgesa", 90, "Scheduled edge node shutdown")

	var body map[string]interface{}
	waitUntil(t, stated("the edge to record the shutdown warning central sent it"), func() bool {
		body = edge.pendingShutdownWarning()
		return body != nil
	})

	got, present := body["node_id"]
	if !present {
		t.Fatalf("the heartbeat body an edge hands its clients has no node_id key at all: %#v", body)
	}
	if got != "edgesa" {
		t.Errorf("node_id = %v, want edgesa.\n\n"+
			"A client attached to edge-sa is told edge-sa is stopping, or its warning_received "+
			"hook cannot tell which gateway the warning is about -- and %q is what the same "+
			"gateway stamps on the lease that client holds.", got, "edgesa")
	}
	// The control plane's own default, reported by a node that is not the control plane, is the
	// specific wrong answer a hardcoded value would give.
	if got == "control" {
		t.Error("the edge reported the control plane's id as the node that is going down")
	}

	// And the control plane answers for itself, rather than for whichever node it last warned.
	postDrain(t, central, `{"seconds": 45, "reason": "Deploying"}`)
	centralBody := central.pendingShutdownWarning()
	if centralBody == nil {
		t.Fatal("the control plane announced a drain that its own heartbeat does not carry")
	}
	if centralBody["node_id"] != "control" {
		t.Errorf("the control plane's own warning names %v as the node going down, want control",
			centralBody["node_id"])
	}
}

// The warning reaches a client on the tunnel-status heartbeat and nowhere else, so the field has
// to be on THAT response -- not merely on the map a unit test can reach.
//
// Driven through ServeHTTP against the real endpoint, because a body assembled correctly and
// then dropped by the handler that answers the client is the shape #1238 already found once.
func TestTheNodeIdReachesTheClientOnTheTunnelStatusResponse(t *testing.T) {
	srv := newDrainTestServer(t)
	postDrain(t, srv, `{"seconds": 45, "reason": "Deploying"}`)

	// A lease is what makes a heartbeat a heartbeat: without one the handler answers the
	// no-lease body and never reaches the warning at all.
	sessionToken, _, err := srv.registry.Register("user-2187", "warned",
		[]PortMapping{{LocalPort: 3000}}, []string{"lfr-demo.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("staging a tunnel to heartbeat for: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tunnel-status",
		strings.NewReader(`{"session_token":"`+sessionToken+`","status":"up"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleTunnelStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tunnel-status answered %d: %s", rec.Code, rec.Body.String())
	}
	warning, ok := client.ParseNodeShutdownWarning(rec.Body.Bytes())
	if !ok {
		t.Fatalf("the heartbeat response does not parse as a shutdown warning: %s", rec.Body.String())
	}
	if warning.NodeID == "" {
		t.Errorf("the client decoded an empty NodeID from the heartbeat response %s.\n\n"+
			"RunHook publishes this as LFT_NODE_ID, so a warning_received hook is handed an "+
			"empty string and cannot say which gateway is stopping (#2187).", rec.Body.String())
	}
}
