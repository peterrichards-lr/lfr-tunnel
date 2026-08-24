package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// readEdgeHealth calls the endpoint the portal reads and returns one node's entry.
func readEdgeHealth(t *testing.T, srv *Server, nodeID string) EdgeHealthStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/portal/edge-health", nil)
	w := httptest.NewRecorder()
	srv.handleEdgeHealth(w, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	node, ok := resp.Nodes[nodeID]
	if !ok {
		t.Fatalf("expected %s in response", nodeID)
	}
	return node
}

// TestEdgeHealthCorrectsStaleOfflineForConnectedNode is the regression test for #1271.
//
// A superseded connection's cleanup wrote "Offline" into edgeHealth unconditionally, while
// registration never writes "Online" -- so the status could disagree with reality. The one
// check positioned to notice then refused to act on it, because its guard read
// `h.Status != "Offline" && h.Status != "Disabled"`, excluding the exact contradiction it
// existed to resolve.
//
// Live consequence: central logged "Edge node edge-apac successfully authenticated" and then
// reported that node Offline for eight and a half minutes while it was serving traffic.
func TestEdgeHealthCorrectsStaleOfflineForConnectedNode(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeClientsMu.Lock()
	// A nil *safeConn is enough to mark the node as holding a live control channel,
	// matching the existing edge-health tests.
	srv.edgeClients["edge-apac"] = nil
	srv.edgeClientsMu.Unlock()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-apac"] = EdgeHealthStatus{
		Status:       "Offline",
		ErrorMessage: "Control connection disconnected",
	}
	srv.edgeHealthMu.Unlock()

	node := readEdgeHealth(t, srv, "edge-apac")
	if node.Status != "Online" {
		t.Errorf("a node holding a live control channel must not be reported %q", node.Status)
	}
	if node.ErrorMessage != "" {
		t.Errorf("expected the stale error to be cleared, got %q", node.ErrorMessage)
	}
}

// TestEdgeHealthKeepsDisabledForConnectedNode guards the other side of that guard. "Disabled"
// means scheduled downtime or an operator action (#887), and must not be overwritten just
// because a connection is present -- otherwise a node inside its stop window would flip to
// Online and read as an incident rather than the intended state.
func TestEdgeHealthKeepsDisabledForConnectedNode(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeClientsMu.Lock()
	srv.edgeClients["edge-us"] = nil
	srv.edgeClientsMu.Unlock()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-us"] = EdgeHealthStatus{Status: "Disabled"}
	srv.edgeHealthMu.Unlock()

	if node := readEdgeHealth(t, srv, "edge-us"); node.Status != "Disabled" {
		t.Errorf("scheduled or operator-set Disabled must win over a live connection, got %q", node.Status)
	}
}

// TestEdgeHealthReportsUnconnectedNodeAsCached checks that the correction is scoped to nodes
// actually in edgeClients -- a genuinely disconnected node must keep reporting Offline.
func TestEdgeHealthReportsUnconnectedNodeAsCached(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-gone"] = EdgeHealthStatus{
		Status:       "Offline",
		ErrorMessage: "Control connection disconnected",
	}
	srv.edgeHealthMu.Unlock()

	node := readEdgeHealth(t, srv, "edge-gone")
	if node.Status != "Offline" {
		t.Errorf("a node with no live connection must stay Offline, got %q", node.Status)
	}
	if node.ErrorMessage == "" {
		t.Error("expected the disconnect reason to be preserved for a genuinely offline node")
	}
}
