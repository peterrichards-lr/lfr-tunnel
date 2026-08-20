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
