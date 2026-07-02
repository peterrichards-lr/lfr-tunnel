package server

import (
	"context"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func TestServer_MiscCoverage8(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	srv.cfg.EdgeNodes = append(srv.cfg.EdgeNodes, config.EdgeNodeConfig{
		ID:  "edge-1",
		URL: "http://127.0.0.1:9999",
	})

	// monitorEdgeHealth
	ctx, cancel := context.WithCancel(context.Background())
	srv.ctx = ctx

	go func() {
		// give it some time
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	srv.monitorEdgeHealth()

	// startRateLimiterCleaner
	ctx2, cancel2 := context.WithCancel(context.Background())
	srv.ctx = ctx2
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel2()
	}()
	srv.startRateLimiterCleaner(ctx2)
}
