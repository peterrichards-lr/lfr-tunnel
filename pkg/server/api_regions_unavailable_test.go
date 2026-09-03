package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

// A configured edge that is down must still be reported, under regions_unavailable (#1690).
//
// It used to be omitted entirely, which left the client unable to distinguish "every region
// answered" from "a region exists but is asleep". The client caches a complete election for 24h
// and an incomplete one for 30 minutes, so the omission silently bought the wrong TTL: a US user
// who connected inside edge-us's scheduled power-off window stayed on the EU control plane for a
// full day after edge-us came back.
func TestAPIVersion_ReportsConfiguredButDownEdges(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "edge-us", URL: "https://us.lfr-demo.se"},
		{ID: "edge-sa", URL: "https://sa.lfr-demo.se"},
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// edge-sa is connected; edge-us is asleep.
	srv.edgeClientsMu.Lock()
	srv.edgeClients["edge-sa"] = &safeConn{}
	srv.edgeClientsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version: got %d, want 200", rec.Code)
	}

	var resp struct {
		Regions            map[string]string `json:"regions"`
		RegionsUnavailable map[string]string `json:"regions_unavailable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}

	// The sleeping edge is absent from the connectable list -- clients must not be sent there.
	for _, name := range []string{"us", "edge-us"} {
		if url, ok := resp.Regions[name]; ok {
			t.Errorf("sleeping edge advertised as connectable: regions[%q] = %q", name, url)
		}
	}

	// ...but it IS declared, under both the node ID and its short alias, exactly as an
	// available edge would be. Same naming rules, so the client can compare the two sets.
	for _, name := range []string{"us", "edge-us"} {
		if got := resp.RegionsUnavailable[name]; got != "https://us.lfr-demo.se" {
			t.Errorf("regions_unavailable[%q] = %q, want %q", name, got, "https://us.lfr-demo.se")
		}
	}

	// The live edge stays where it was and does not leak into the unavailable set.
	if got := resp.Regions["sa"]; got != "https://sa.lfr-demo.se" {
		t.Errorf("regions[\"sa\"] = %q, want the live edge URL", got)
	}
	if _, ok := resp.RegionsUnavailable["sa"]; ok {
		t.Errorf("a connected edge was reported as unavailable: %#v", resp.RegionsUnavailable)
	}
}

// With every edge up, the unavailable set must be empty rather than merely small -- a client
// treats a non-empty set as reason to re-probe every 30 minutes, so a stray entry here would
// make every client on a healthy deployment re-probe all day.
func TestAPIVersion_NoUnavailableEdgesWhenAllUp(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "edge-us", URL: "https://us.lfr-demo.se"}}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.edgeClientsMu.Lock()
	srv.edgeClients["edge-us"] = &safeConn{}
	srv.edgeClientsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		RegionsUnavailable map[string]string `json:"regions_unavailable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}
	if len(resp.RegionsUnavailable) != 0 {
		t.Errorf("all edges are up, so nothing should be unavailable: %#v", resp.RegionsUnavailable)
	}
}
