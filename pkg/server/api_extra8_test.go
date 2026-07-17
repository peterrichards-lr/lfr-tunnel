package server

import (
	"testing"
	"time"
)

func TestServer_AuthRegistry(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// UpdateLeaseHeaders
	headers := map[string]string{"X-Test": "1"}
	_ = srv.registry.UpdateLeaseHeaders("test-sub.example.com", headers) //nolint:errcheck

	// GetActiveVisitorIPs
	lease := &TunnelLease{
		VisitorIPs: map[string]time.Time{
			"127.0.0.1":   time.Now(),
			"192.168.1.1": time.Now().Add(-2 * time.Hour),
		},
	}
	_ = lease.GetActiveVisitorIPs(1 * time.Hour) //nolint:errcheck

	// StartCleanupRoutine
	// This spawns a goroutine, so just call it and wait briefly
	srv.registry.StartCleanupRoutine(10 * time.Millisecond)

	time.Sleep(50 * time.Millisecond)
}
