package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

func TestServer_APIVersion_RegionNameExtraction(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "aws-edge-apac", URL: "https://aws-edge-apac.lfr-demo.se"},
		{ID: "edge-us-east-2", URL: "https://us.lfr-demo.se"},
		{ID: "edge-in", URL: "https://in.lfr-demo.se"},
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Mark edge nodes as active (isUp = true)
	srv.edgeClientsMu.Lock()
	srv.edgeClients["aws-edge-apac"] = &safeConn{}
	srv.edgeClients["edge-us-east-2"] = &safeConn{}
	srv.edgeClients["edge-in"] = &safeConn{}
	srv.edgeClientsMu.Unlock()

	// Test GET /api/version using edge node public domain host (aws-edge-apac.lfr-demo.se)
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "aws-edge-apac.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for edge control domain host, got %d", rec.Code)
	}

	var resp struct {
		Regions map[string]string `json:"regions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	expected := map[string]string{
		"eu":             "https://tunnel.lfr-demo.se",
		"central":        "https://tunnel.lfr-demo.se",
		"apac":           "https://aws-edge-apac.lfr-demo.se",
		"aws-edge-apac":  "https://aws-edge-apac.lfr-demo.se",
		"us":             "https://us.lfr-demo.se",
		"edge-us-east-2": "https://us.lfr-demo.se",
		"in":             "https://in.lfr-demo.se",
		"edge-in":        "https://in.lfr-demo.se",
	}

	for k, wantURL := range expected {
		gotURL, ok := resp.Regions[k]
		if !ok {
			t.Errorf("missing expected region key %q in /api/version output: %#v", k, resp.Regions)
		} else if gotURL != wantURL {
			t.Errorf("region %q: got URL %q, want %q", k, gotURL, wantURL)
		}
	}
}

// The central entry used to be built as "https://tunnel." + the first domain unconditionally,
// so a control plane that is neither https nor reachable at tunnel.<domain> advertised an
// address nothing answered on -- and a client failing over to it retried the same dead host
// every time. central_url overrides both halves of that assumption (#1286).
func TestServer_APIVersion_CentralURLOverride(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.local"}
	cfg.CentralURL = "http://gateway.lfr-demo.local:8000"

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.local"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp struct {
		Regions map[string]string `json:"regions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for _, key := range []string{"eu", "central"} {
		if got := resp.Regions[key]; got != cfg.CentralURL {
			t.Errorf("region %q: got %q, want the configured central_url %q", key, got, cfg.CentralURL)
		}
	}
}

// And with central_url unset the historical construction has to survive untouched, since every
// existing deployment relies on it and none of them set the new field.
func TestServer_APIVersion_CentralURLDefaultsToTunnelPrefix(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.CentralURL = ""

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Regions map[string]string `json:"regions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got := resp.Regions["central"]; got != "https://tunnel.lfr-demo.se" {
		t.Errorf("central without central_url: got %q, want %q", got, "https://tunnel.lfr-demo.se")
	}
}
