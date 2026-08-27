package server

import (
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// TestTriggerEdgeHealthRecheck_ChecksPromptly is the regression test for the
// "table doesn't update without a manual page refresh" report: after a
// portal power action, status should reflect within seconds, not wait for
// monitorEdgeHealth's next scheduled pass (up to 60s away). This doesn't
// wait out the full 5s-tick/2min-deadline loop -- it only confirms the
// mechanism performs its first check immediately rather than only on the
// first tick.
func TestTriggerEdgeHealthRecheck_ChecksPromptly(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-test", URL: "http://127.0.0.1:1"}, // reserved port, connection always refused
	})
	srv.outboundMutex.Lock()
	srv.outboundConnected = true
	srv.outboundMutex.Unlock()

	srv.triggerEdgeHealthRecheck("edge-test")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.edgeHealthMu.RLock()
		h, ok := srv.edgeHealth["edge-test"]
		srv.edgeHealthMu.RUnlock()
		if ok && h.Status == "Offline" {
			return // recheck ran and recorded a result -- success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected triggerEdgeHealthRecheck to record a health result within 2s of being called")
}

func TestTriggerEdgeHealthRecheck_UnknownNodeIsNoOp(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Should not panic or block on a node ID that isn't in cfg.EdgeNodes.
	srv.triggerEdgeHealthRecheck("does-not-exist")
}
