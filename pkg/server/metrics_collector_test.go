package server

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// TestMetricsCollectorRecordsQueuedMetrics covers the queue path the control plane uses
// when a lease is cleaned up.
//
// It replaces a test that constructed a collector, called Queue and forwardToControlPlane,
// and asserted nothing whatsoever -- it could not have failed, and did not notice that the
// forwarding it was named after delivered no bytes in production (#1958).
func TestMetricsCollectorRecordsQueuedMetrics(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close() //nolint:errcheck

	cfg := config.DefaultServerConfig()
	collector := NewMetricsCollector(database, cfg, NewRegistry(nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go collector.Start(ctx)

	collector.Queue(&db.TunnelMetric{
		UserID:          "user-1",
		SubdomainPrefix: "demo",
		FullHost:        "demo.example.se",
		BytesIn:         11,
		BytesOut:        22,
		ConnectedAt:     time.Now().UTC(),
		RecordedAt:      time.Now().UTC(),
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		analytics, err := database.GetUserAnalytics("user-1", 30)
		if err != nil {
			t.Fatalf("failed to read analytics: %v", err)
		}
		if len(analytics.Tunnels) == 1 {
			if analytics.Tunnels[0].BytesIn != 11 || analytics.Tunnels[0].BytesOut != 22 {
				t.Fatalf("queued metric recorded with wrong figures: %+v", analytics.Tunnels[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a queued metric never reached the database")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestMetricsCollectorLeavesEdgeDeltasForTheReporter pins the division of labour introduced
// by #1958. On an edge (no database) the periodic sweep must not consume the leases' byte
// deltas: taking a delta clears it, and only edgeMetricsReporter can actually deliver it.
// Consuming them here would silently destroy exactly the measurement this issue restores.
func TestMetricsCollectorLeavesEdgeDeltasForTheReporter(t *testing.T) {
	registry := NewRegistry(nil)
	lease := &TunnelLease{
		UserID:          "user-1",
		SubdomainPrefix: "demo",
		FullHost:        "demo.example.se",
		CreatedAt:       time.Now().UTC(),
	}
	atomic.StoreUint64(&lease.BytesOut, 512)
	registry.Lock()
	registry.leases[lease.FullHost] = lease
	registry.Unlock()

	cfg := config.DefaultServerConfig()
	cfg.ControlPlaneURL = "http://control.example.se"
	cfg.EdgeToken = "usedge-token"
	collector := NewMetricsCollector(nil, cfg, registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go collector.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	deltas := registry.TakeByteDeltas()
	if len(deltas) != 1 || deltas[0].BytesOut != 512 {
		t.Fatalf("the collector consumed an edge's deltas instead of leaving them to be reported: %+v", deltas)
	}
}
