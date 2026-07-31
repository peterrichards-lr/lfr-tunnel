package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreferredIP(t *testing.T) {
	cases := []struct {
		ipv4, ipv6, want string
	}{
		{"203.0.113.10", "2001:db8::1", "203.0.113.10"}, // dual-stack prefers IPv4
		{"", "2001:db8::1", "2001:db8::1"},              // IPv6-only falls back
		{"203.0.113.10", "", "203.0.113.10"},            // IPv4-only
		{"", "", ""},
	}
	for _, c := range cases {
		if got := preferredIP(c.ipv4, c.ipv6); got != c.want {
			t.Errorf("preferredIP(%q, %q) = %q, want %q", c.ipv4, c.ipv6, got, c.want)
		}
	}
}

// TestEdgeHealth_BackfillsIPv6OnlyFallback exercises the s.edgeIPs fallback
// path in handleEdgeHealth with an IPv6 literal, confirming it's classified
// into ResolvedIPv6 (not ResolvedIPv4) rather than being dropped or
// misclassified -- this is the fallback used when a node is only known via
// its live WS connection, not DNS.
func TestEdgeHealth_BackfillsIPv6OnlyFallback(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeClientsMu.Lock()
	srv.edgeIPs["edge-v6only"] = "2001:db8::1"
	// The s.edgeIPs fallback in handleEdgeHealth is only consulted for nodes
	// with a live entry in s.edgeClients (it's the "known via an active WS
	// connection, not DNS" path) -- a nil *safeConn is enough to exercise it
	// without a real connection.
	srv.edgeClients["edge-v6only"] = nil
	srv.edgeClientsMu.Unlock()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-v6only"] = EdgeHealthStatus{Status: "Offline"}
	srv.edgeHealthMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/portal/edge-health", nil)
	w := httptest.NewRecorder()
	srv.handleEdgeHealth(w, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	node, ok := resp.Nodes["edge-v6only"]
	if !ok {
		t.Fatal("expected edge-v6only in response")
	}
	if node.ResolvedIPv6 != "2001:db8::1" {
		t.Errorf("expected ResolvedIPv6 2001:db8::1, got %q", node.ResolvedIPv6)
	}
	if node.ResolvedIPv4 != "" {
		t.Errorf("expected ResolvedIPv4 empty for an IPv6-only fallback, got %q", node.ResolvedIPv4)
	}
}

// TestEdgeHealth_PassesThroughDualStackFields confirms that when
// updateEdgeHealth (DNS-based resolution) has already populated both
// ResolvedIPv4 and ResolvedIPv6, handleEdgeHealth's fallback backfill
// doesn't clobber or skip either one.
func TestEdgeHealth_PassesThroughDualStackFields(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-dualstack"] = EdgeHealthStatus{
		Status:       "Online",
		ResolvedIP:   "203.0.113.10",
		ResolvedIPv4: "203.0.113.10",
		ResolvedIPv6: "2001:db8::1",
	}
	srv.edgeHealthMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/portal/edge-health", nil)
	w := httptest.NewRecorder()
	srv.handleEdgeHealth(w, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	node, ok := resp.Nodes["edge-dualstack"]
	if !ok {
		t.Fatal("expected edge-dualstack in response")
	}
	if node.ResolvedIPv4 != "203.0.113.10" || node.ResolvedIPv6 != "2001:db8::1" {
		t.Errorf("expected both addresses preserved, got v4=%q v6=%q", node.ResolvedIPv4, node.ResolvedIPv6)
	}
}
