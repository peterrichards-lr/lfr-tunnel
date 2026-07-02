package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestMetricsCollector_QueueAndForward(t *testing.T) {
	// Dummy server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.ServerConfig{
		ControlPlaneURL: srv.URL,
		EdgeToken:       "dummy",
	}

	registry := NewRegistry(nil)
	mc := NewMetricsCollector(nil, cfg, registry)

	// Test Queue
	m := &db.TunnelMetric{FullHost: "test.com"}
	mc.Queue(m)

	// Test forwardToControlPlane
	metrics := []*db.TunnelMetric{m}
	mc.forwardToControlPlane(metrics)
}
