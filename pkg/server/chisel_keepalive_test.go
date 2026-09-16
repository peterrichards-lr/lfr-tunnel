package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// The gateway never pinged its attached clients until #1946: chisel's ping loop is gated on
// KeepAlive > 0 and the config left it zero, so an idle control channel had nothing refreshing
// nginx's 60s proxy_read_timeout and a client that vanished without closing its socket was only
// noticed by the 10s orphan-lease dial.
func TestChiselServerConfigSetsKeepAlive(t *testing.T) {
	cfg := config.DefaultServerConfig()

	got := newChiselServerConfig(cfg)
	if got.KeepAlive != defaultChiselKeepAlive {
		t.Errorf("KeepAlive = %s, want %s -- zero means chisel never pings (#1946)", got.KeepAlive, defaultChiselKeepAlive)
	}
	if !got.Reverse {
		t.Error("Reverse must stay on; every tunnel this gateway serves is a reverse tunnel")
	}

	// Configurable rather than constant, because its counterpart is nginx's
	// proxy_read_timeout: whoever changes that has to be able to change this from the server
	// config, without a new gateway binary.
	cfg.TunnelKeepAlive = 10 * time.Second
	if got := newChiselServerConfig(cfg); got.KeepAlive != 10*time.Second {
		t.Errorf("configured tunnel_keepalive ignored: KeepAlive = %s, want 10s", got.KeepAlive)
	}
}

// The client-side reconnect window is the one number in #1946 that cannot be fixed from the
// server alone, so the gateway advertises it and the client clamps it. This covers the
// advertisement half.
func TestAPIVersionAdvertisesClientReconnectWindow(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.ClientReconnectWindow = 75 * time.Second

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Host = "tunnel.lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version: got %d, want 200", rec.Code)
	}

	var resp struct {
		ClientReconnectSeconds int `json:"client_reconnect_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}
	if resp.ClientReconnectSeconds != 75 {
		t.Errorf("client_reconnect_seconds = %d, want 75 -- without it the only way to correct a client's reconnect window is a client release (#1946)", resp.ClientReconnectSeconds)
	}
}
