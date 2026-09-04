package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"

	chserver "github.com/jpillora/chisel/server"
)

// Characterisation tests for both reverse proxies in this file's production counterpart,
// written against the Director-based implementation before the migration to Rewrite
// (#1704) and expected to pass unchanged after it.
//
// The forwarded headers here are not cosmetic: the visitor IP they carry is what reaches
// the tunnel table, the WAF and the audit log, and httputil.ReverseProxy appends to
// X-Forwarded-For for a Director but not for a Rewrite. These assertions are deliberately
// exact strings rather than "is not empty", because a dropped hop still leaves a
// populated header.

// captureBackend starts a backend that records the request the proxy actually sent.
func captureBackend(t *testing.T) (*http.Request, *httptest.Server) {
	t.Helper()

	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		got.Host = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return &got, srv
}

// backendPort extracts the port a captured backend is listening on.
func backendPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse backend port: %v", err)
	}
	return port
}

func leaseHandler(t *testing.T, host string, backendPort int, cfg *config.ServerConfig) (*ProxyHandler, *TunnelLease) {
	t.Helper()

	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("chisel server: %v", err)
	}
	reg := NewRegistry(chiselServer)

	lease := &TunnelLease{
		SubdomainPrefix: "fwd",
		FullHost:        host,
		SessionToken:    "test-token",
		LocalPort:       backendPort,
		TargetPort:      8080,
		CreatedAt:       time.Now(),
	}
	reg.Lock()
	reg.leases[host] = lease
	reg.Unlock()

	return NewProxyHandler(reg, cfg), lease
}

func TestLeaseProxy_ForwardingHeaders(t *testing.T) {
	const host = "fwd-se.liferay.com"

	cases := []struct {
		name        string
		remoteAddr  string
		inbound     map[string]string
		wantXFF     string
		wantRealIP  string
		wantProto   string
		wantVisitor string
	}{
		{
			name:       "untrusted peer: inbound X-Forwarded-For is not believed",
			remoteAddr: "192.0.2.1:1234",
			inbound:    map[string]string{"X-Forwarded-For": "203.0.113.7"},
			// The Director overwrites X-Forwarded-For with the resolved visitor IP, and
			// ReverseProxy then appends the connecting peer to it.
			wantXFF:     "192.0.2.1, 192.0.2.1",
			wantRealIP:  "192.0.2.1",
			wantProto:   "http",
			wantVisitor: "192.0.2.1",
		},
		{
			name:        "trusted peer: inbound X-Forwarded-For resolves the visitor",
			remoteAddr:  "127.0.0.1:4444",
			inbound:     map[string]string{"X-Forwarded-For": "203.0.113.7"},
			wantXFF:     "203.0.113.7, 127.0.0.1",
			wantRealIP:  "203.0.113.7",
			wantProto:   "http",
			wantVisitor: "203.0.113.7",
		},
		{
			name:        "inbound X-Forwarded-Proto decides the forwarded scheme",
			remoteAddr:  "192.0.2.1:1234",
			inbound:     map[string]string{"X-Forwarded-Proto": "https"},
			wantXFF:     "192.0.2.1, 192.0.2.1",
			wantRealIP:  "192.0.2.1",
			wantProto:   "https",
			wantVisitor: "192.0.2.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, backend := captureBackend(t)
			handler, lease := leaseHandler(t, host, backendPort(t, backend), config.DefaultServerConfig())

			req := httptest.NewRequest(http.MethodGet, "http://"+host+"/web/guest", nil)
			req.Host = host
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.inbound {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
			}
			if v := got.Header.Get("X-Forwarded-For"); v != tc.wantXFF {
				t.Errorf("X-Forwarded-For: got %q, want %q", v, tc.wantXFF)
			}
			if v := got.Header.Get("X-Real-IP"); v != tc.wantRealIP {
				t.Errorf("X-Real-IP: got %q, want %q", v, tc.wantRealIP)
			}
			if v := got.Header.Get("X-Forwarded-Host"); v != host {
				t.Errorf("X-Forwarded-Host: got %q, want %q", v, host)
			}
			if v := got.Header.Get("X-Forwarded-Proto"); v != tc.wantProto {
				t.Errorf("X-Forwarded-Proto: got %q, want %q", v, tc.wantProto)
			}
			if got.Host != host {
				t.Errorf("Host: got %q, want %q", got.Host, host)
			}

			// The visitor IP recorded on the lease is what the portal's tunnel table and
			// the audit log show.
			lease.VisitorIPsMu.Lock()
			_, recorded := lease.VisitorIPs[tc.wantVisitor]
			total := len(lease.VisitorIPs)
			lease.VisitorIPsMu.Unlock()
			if !recorded {
				t.Errorf("visitor IP %q was not recorded on the lease (%d entries)", tc.wantVisitor, total)
			}
		})
	}
}

// TestLeaseProxy_ConfiguredProxyHeaders pins the branch that replaces the standard
// forwarded headers with operator-configured ones, including the interpolation variables.
func TestLeaseProxy_ConfiguredProxyHeaders(t *testing.T) {
	const host = "fwd-custom.liferay.com"

	got, backend := captureBackend(t)

	cfg := config.DefaultServerConfig()
	cfg.ProxyHeaders = map[string]string{
		"X-Client-IP":       "$client_ip",
		"X-Forwarded-Host":  "$host",
		"X-Forwarded-Proto": "$proto",
		"X-Liferay-Custom":  "my-custom-value",
	}

	handler, _ := leaseHandler(t, host, backendPort(t, backend), cfg)

	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/web/guest", nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if v := got.Header.Get("X-Client-IP"); v != "192.0.2.1" {
		t.Errorf("X-Client-IP: got %q, want %q", v, "192.0.2.1")
	}
	if v := got.Header.Get("X-Forwarded-Host"); v != host {
		t.Errorf("X-Forwarded-Host: got %q, want %q", v, host)
	}
	if v := got.Header.Get("X-Forwarded-Proto"); v != "http" {
		t.Errorf("X-Forwarded-Proto: got %q, want %q", v, "http")
	}
	if v := got.Header.Get("X-Liferay-Custom"); v != "my-custom-value" {
		t.Errorf("X-Liferay-Custom: got %q, want %q", v, "my-custom-value")
	}
	// This branch sets no X-Forwarded-For of its own, so the header the backend sees is
	// whatever ReverseProxy itself built from the inbound chain plus the peer.
	if v := got.Header.Get("X-Forwarded-For"); v != "203.0.113.7, 192.0.2.1" {
		t.Errorf("X-Forwarded-For: got %q, want %q", v, "203.0.113.7, 192.0.2.1")
	}
	// No X-Real-IP is configured, so none is injected.
	if v := got.Header.Get("X-Real-IP"); v != "" {
		t.Errorf("X-Real-IP: got %q, want it unset", v)
	}
}

// TestCrossNodeProxy_ForwardingHeaders pins the cross-node hop, which differs from the
// lease proxy in that it *extends* an existing forwarded chain rather than replacing it,
// and leaves headers a previous gateway already set alone.
func TestCrossNodeProxy_ForwardingHeaders(t *testing.T) {
	got, mockEdge := captureBackend(t)

	cases := []struct {
		name      string
		inbound   map[string]string
		wantXFF   string
		wantReal  string
		wantHost  string
		wantProto string
	}{
		{
			name:    "first hop populates the chain",
			inbound: map[string]string{},
			// The Director appends the resolved visitor IP, then ReverseProxy appends the
			// peer on top of that.
			wantXFF:   "198.51.100.25, 198.51.100.25",
			wantReal:  "198.51.100.25",
			wantHost:  "demo.lfr-demo.se",
			wantProto: "http",
		},
		{
			name: "an upstream gateway's headers are extended, not overwritten",
			inbound: map[string]string{
				"X-Forwarded-For": "203.0.113.7",
				"X-Real-IP":       "203.0.113.7",
				// Deliberately not the request Host: a vanity domain the first gateway
				// recorded. If this hop overwrites it with its own view the difference
				// shows, where an identical value would hide it.
				"X-Forwarded-Host":  "vanity.example.com",
				"X-Forwarded-Proto": "https",
			},
			wantXFF:   "203.0.113.7, 198.51.100.25, 198.51.100.25",
			wantReal:  "203.0.113.7",
			wantHost:  "vanity.example.com",
			wantProto: "https",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultServerConfig()
			cfg.Domains = []string{"lfr-demo.se"}
			cfg.EdgeNodes = []config.EdgeNodeConfig{{ID: "edge-in", URL: mockEdge.URL}}

			centralSrv, err := NewServer(cfg)
			if err != nil {
				t.Fatalf("create central server: %v", err)
			}
			centralSrv.edgeLeasesMu.Lock()
			centralSrv.edgeLeases["user-123"] = []EdgeLease{{
				NodeID:    "edge-in",
				Subdomain: "demo",
				UserID:    "user-123",
				FullHost:  "demo.lfr-demo.se",
				LocalPort: 8080,
				CreatedAt: time.Now(),
			}}
			centralSrv.edgeLeasesMu.Unlock()

			req := httptest.NewRequest(http.MethodGet, "http://demo.lfr-demo.se/api/test?q=search", nil)
			req.Host = "demo.lfr-demo.se"
			req.RemoteAddr = "198.51.100.25:54321"
			for k, v := range tc.inbound {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			centralSrv.proxyHandler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
			}
			if v := got.Header.Get("X-Forwarded-For"); v != tc.wantXFF {
				t.Errorf("X-Forwarded-For: got %q, want %q", v, tc.wantXFF)
			}
			if v := got.Header.Get("X-Real-IP"); v != tc.wantReal {
				t.Errorf("X-Real-IP: got %q, want %q", v, tc.wantReal)
			}
			if v := got.Header.Get("X-Forwarded-Host"); v != tc.wantHost {
				t.Errorf("X-Forwarded-Host: got %q, want %q", v, tc.wantHost)
			}
			if v := got.Header.Get("X-Forwarded-Proto"); v != tc.wantProto {
				t.Errorf("X-Forwarded-Proto: got %q, want %q", v, tc.wantProto)
			}
			// The downstream gateway identifies the lease by Host, so it must survive.
			if got.Host != "demo.lfr-demo.se" {
				t.Errorf("Host: got %q, want %q", got.Host, "demo.lfr-demo.se")
			}
			if got.URL.RawQuery != "q=search" {
				t.Errorf("query: got %q, want %q", got.URL.RawQuery, "q=search")
			}
			if v := got.Header.Get("X-LFR-Cross-Node-Hop"); v != "1" {
				t.Errorf("X-LFR-Cross-Node-Hop: got %q, want %q", v, "1")
			}
			if v := got.Header.Get("X-LFR-Cross-Node-Visited"); v != "control" {
				t.Errorf("X-LFR-Cross-Node-Visited: got %q, want %q", v, "control")
			}
		})
	}
}
