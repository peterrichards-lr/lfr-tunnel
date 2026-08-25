package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// TestCrossNodeProxy_CentralToEdge tests that when Central receives a request for an
// edge-hosted tunnel during DNS propagation, Central reverse-proxies the request to the Edge.
func TestCrossNodeProxy_CentralToEdge(t *testing.T) {
	var receivedHost string
	var receivedHop string
	var receivedVisited string
	var receivedXFF string

	// 1. Start a mock regional Edge server
	mockEdgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		receivedHop = r.Header.Get("X-LFR-Cross-Node-Hop")
		receivedVisited = r.Header.Get("X-LFR-Cross-Node-Visited")
		receivedXFF = r.Header.Get("X-Forwarded-For")

		w.Header().Set("X-Served-By", "mock-edge-in")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from edge-in"))
	}))
	defer mockEdgeServer.Close()

	// 2. Setup Central control plane server
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{
			ID:  "edge-in",
			URL: mockEdgeServer.URL,
		},
	}

	centralSrv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create central server: %v", err)
	}

	// Add edge lease for demo.lfr-demo.se on edge-in
	centralSrv.edgeLeasesMu.Lock()
	centralSrv.edgeLeases["user-123"] = []EdgeLease{
		{
			NodeID:    "edge-in",
			Subdomain: "demo",
			UserID:    "user-123",
			FullHost:  "demo.lfr-demo.se",
			LocalPort: 8080,
			CreatedAt: time.Now(),
		},
	}
	centralSrv.edgeLeasesMu.Unlock()

	// 3. Send request to Central's proxy handler with Host: demo.lfr-demo.se
	req := httptest.NewRequest(http.MethodGet, "http://demo.lfr-demo.se/api/test?q=search", nil)
	req.Host = "demo.lfr-demo.se"
	req.RemoteAddr = "198.51.100.25:54321"

	rec := httptest.NewRecorder()
	centralSrv.proxyHandler.ServeHTTP(rec, req)

	// 4. Verify response and headers
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello from edge-in" {
		t.Errorf("expected body 'hello from edge-in', got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Served-By") != "mock-edge-in" {
		t.Errorf("expected X-Served-By header 'mock-edge-in', got %q", rec.Header().Get("X-Served-By"))
	}

	// 5. Verify forwarded proxy metadata
	if receivedHost != "demo.lfr-demo.se" {
		t.Errorf("expected downstream Host header 'demo.lfr-demo.se', got %q", receivedHost)
	}
	if receivedHop != "1" {
		t.Errorf("expected X-LFR-Cross-Node-Hop '1', got %q", receivedHop)
	}
	if receivedVisited != "control" {
		t.Errorf("expected X-LFR-Cross-Node-Visited 'control', got %q", receivedVisited)
	}
	if receivedXFF == "" {
		t.Errorf("expected X-Forwarded-For header to be populated")
	}
}

// TestCrossNodeProxy_EdgeToCentral tests that when an Edge gateway receives a request for a
// tunnel hosted on Central, the Edge forwards the request to the Control Plane.
func TestCrossNodeProxy_EdgeToCentral(t *testing.T) {
	var receivedHost string
	var receivedHop string
	var receivedVisited string

	// 1. Start a mock Control Plane server
	mockControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		receivedHop = r.Header.Get("X-LFR-Cross-Node-Hop")
		receivedVisited = r.Header.Get("X-LFR-Cross-Node-Visited")

		w.Header().Set("X-Served-By", "mock-control-plane")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from central"))
	}))
	defer mockControlPlane.Close()

	// 2. Setup Edge server
	edgeCfg := &config.ServerConfig{
		Domains:         []string{"lfr-demo.se"},
		BindAddr:        ":0",
		ChiselBindAddr:  ":0",
		ControlPlaneURL: mockControlPlane.URL,
		EdgeToken:       "edge-token-in",
	}

	edgeSrv, err := NewServer(edgeCfg)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}

	// 3. Send request to Edge proxy handler
	req := httptest.NewRequest(http.MethodGet, "http://central-tunnel.lfr-demo.se/dashboard", nil)
	req.Host = "central-tunnel.lfr-demo.se"
	req.RemoteAddr = "203.0.113.50:43210"

	rec := httptest.NewRecorder()
	edgeSrv.proxyHandler.ServeHTTP(rec, req)

	// 4. Verify response
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello from central" {
		t.Errorf("expected body 'hello from central', got %q", rec.Body.String())
	}
	if receivedHost != "central-tunnel.lfr-demo.se" {
		t.Errorf("expected downstream Host header 'central-tunnel.lfr-demo.se', got %q", receivedHost)
	}
	if receivedHop != "1" {
		t.Errorf("expected X-LFR-Cross-Node-Hop '1', got %q", receivedHop)
	}
	if receivedVisited == "" {
		t.Errorf("expected X-LFR-Cross-Node-Visited to be non-empty")
	}
}

// TestCrossNodeProxy_EdgeToEdgeViaCentral tests multi-hop routing:
// Edge A receives request -> proxies to Central (hop 1) -> Central proxies to Edge B (hop 2).
func TestCrossNodeProxy_EdgeToEdgeViaCentral(t *testing.T) {
	// 1. Start mock Edge B
	mockEdgeB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Served-By", "mock-edge-b")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from edge-b"))
	}))
	defer mockEdgeB.Close()

	// 2. Setup Central Server
	centralCfg := config.DefaultServerConfig()
	centralCfg.Domains = []string{"lfr-demo.se"}
	centralCfg.EdgeNodes = []config.EdgeNodeConfig{
		{
			ID:  "edge-b",
			URL: mockEdgeB.URL,
		},
	}

	centralSrv, err := NewServer(centralCfg)
	if err != nil {
		t.Fatalf("failed to create central server: %v", err)
	}

	centralSrv.edgeLeasesMu.Lock()
	centralSrv.edgeLeases["user-b"] = []EdgeLease{
		{
			NodeID:    "edge-b",
			Subdomain: "service-b",
			UserID:    "user-b",
			FullHost:  "service-b.lfr-demo.se",
			LocalPort: 9000,
			CreatedAt: time.Now(),
		},
	}
	centralSrv.edgeLeasesMu.Unlock()

	// Wrap Central in httptest.Server so Edge A can proxy to it over HTTP
	centralHTTP := httptest.NewServer(centralSrv.proxyHandler)
	defer centralHTTP.Close()

	// 3. Setup Edge A Server pointing to Central
	edgeACfg := &config.ServerConfig{
		Domains:         []string{"lfr-demo.se"},
		BindAddr:        ":0",
		ChiselBindAddr:  ":0",
		ControlPlaneURL: centralHTTP.URL,
		EdgeToken:       "edge-token-a",
	}

	edgeASrv, err := NewServer(edgeACfg)
	if err != nil {
		t.Fatalf("failed to create edge-a server: %v", err)
	}

	// 4. Send request to Edge A for service-b.lfr-demo.se
	req := httptest.NewRequest(http.MethodGet, "http://service-b.lfr-demo.se/status", nil)
	req.Host = "service-b.lfr-demo.se"

	rec := httptest.NewRecorder()
	edgeASrv.proxyHandler.ServeHTTP(rec, req)

	// 5. Verify response routed all the way from Edge A -> Central -> Edge B
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello from edge-b" {
		t.Errorf("expected body 'hello from edge-b', got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Served-By") != "mock-edge-b" {
		t.Errorf("expected X-Served-By header 'mock-edge-b', got %q", rec.Header().Get("X-Served-By"))
	}
}

// TestCrossNodeProxy_HopLimitAndLoopPrevention tests that hop counts >= 2 or circular visits
// are rejected cleanly without looping.
func TestCrossNodeProxy_HopLimitAndLoopPrevention(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{
			ID:  "edge-in",
			URL: "http://127.0.0.1:54321",
		},
	}

	centralSrv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create central server: %v", err)
	}

	centralSrv.edgeLeasesMu.Lock()
	centralSrv.edgeLeases["user-1"] = []EdgeLease{
		{
			NodeID:    "edge-in",
			Subdomain: "loop-test",
			UserID:    "user-1",
			FullHost:  "loop-test.lfr-demo.se",
			LocalPort: 8080,
			CreatedAt: time.Now(),
		},
	}
	centralSrv.edgeLeasesMu.Unlock()

	t.Run("hop limit reached returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://loop-test.lfr-demo.se/", nil)
		req.Host = "loop-test.lfr-demo.se"
		req.Header.Set("X-LFR-Cross-Node-Hop", "2")

		rec := httptest.NewRecorder()
		centralSrv.proxyHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when hop limit reached, got %d", rec.Code)
		}
	})

	t.Run("already visited current node returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://loop-test.lfr-demo.se/", nil)
		req.Host = "loop-test.lfr-demo.se"
		req.Header.Set("X-LFR-Cross-Node-Visited", "edge-a,control")

		rec := httptest.NewRecorder()
		centralSrv.proxyHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when current node already visited, got %d", rec.Code)
		}
	})

	t.Run("already visited target node returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://loop-test.lfr-demo.se/", nil)
		req.Host = "loop-test.lfr-demo.se"
		req.Header.Set("X-LFR-Cross-Node-Visited", "edge-in")

		rec := httptest.NewRecorder()
		centralSrv.proxyHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when target node already in visited list, got %d", rec.Code)
		}
	})
}

// TestCrossNodeProxy_TargetUnreachableBadGateway verifies that if the target gateway
// fails or is down, the proxy returns 502 Bad Gateway.
func TestCrossNodeProxy_TargetUnreachableBadGateway(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	// Port 59999 is closed
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{
			ID:  "edge-down",
			URL: "http://127.0.0.1:59999",
		},
	}

	centralSrv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create central server: %v", err)
	}

	centralSrv.edgeLeasesMu.Lock()
	centralSrv.edgeLeases["user-1"] = []EdgeLease{
		{
			NodeID:    "edge-down",
			Subdomain: "unreachable",
			UserID:    "user-1",
			FullHost:  "unreachable.lfr-demo.se",
			LocalPort: 8080,
			CreatedAt: time.Now(),
		},
	}
	centralSrv.edgeLeasesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "http://unreachable.lfr-demo.se/", nil)
	req.Host = "unreachable.lfr-demo.se"

	rec := httptest.NewRecorder()
	centralSrv.proxyHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 Bad Gateway when target is unreachable, got %d", rec.Code)
	}
}

// TestCrossNodeProxy_WAFEarlyBlock verifies that malicious requests are blocked with 403
// before attempting any cross-node proxying.
func TestCrossNodeProxy_WAFEarlyBlock(t *testing.T) {
	mockEdge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("mock edge should NEVER be reached for a malicious WAF-blocked request")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockEdge.Close()

	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.EnableWAF = true
	cfg.EdgeNodes = []config.EdgeNodeConfig{
		{
			ID:  "edge-waf",
			URL: mockEdge.URL,
		},
	}

	centralSrv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create central server: %v", err)
	}

	centralSrv.edgeLeasesMu.Lock()
	centralSrv.edgeLeases["user-1"] = []EdgeLease{
		{
			NodeID:    "edge-waf",
			Subdomain: "waf-target",
			UserID:    "user-1",
			FullHost:  "waf-target.lfr-demo.se",
			LocalPort: 8080,
			CreatedAt: time.Now(),
		},
	}
	centralSrv.edgeLeasesMu.Unlock()

	// Path traversal malicious request
	req := httptest.NewRequest(http.MethodGet, "http://waf-target.lfr-demo.se/../../etc/passwd", nil)
	req.Host = "waf-target.lfr-demo.se"

	rec := httptest.NewRecorder()
	centralSrv.proxyHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for malicious request, got %d", rec.Code)
	}
}
