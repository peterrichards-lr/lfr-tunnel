package server

import (
	"testing"

	"lfr-tunnel/pkg/config"
)

// TestEdgeVersionSurvivesGoingOffline is the regression test for the second half of
// #1176. The version arrives on the control-channel handshake, so only the online paths
// have one to pass; the offline path passes "" and used to erase it. A powered-down edge
// then showed no version at all, which is exactly when it is most useful -- and it made a
// deploy to a scheduled-off edge impossible to confirm afterwards.
func TestEdgeVersionSurvivesGoingOffline(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.DBPath = ""
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	srv.updateEdgeHealth("edge-apac", "Online", 12, "", "v1.47.0", false)

	srv.edgeHealthMu.RLock()
	online := srv.edgeHealth["edge-apac"]
	srv.edgeHealthMu.RUnlock()
	if online.Version != "v1.47.0" {
		t.Fatalf("expected the reported version to be recorded, got %q", online.Version)
	}

	// The offline path has no version to pass -- the control channel is gone.
	srv.updateEdgeHealth("edge-apac", "Offline", 0, "Edge control channel disconnected", "", false)

	srv.edgeHealthMu.RLock()
	offline := srv.edgeHealth["edge-apac"]
	srv.edgeHealthMu.RUnlock()

	if offline.Status != "Offline" {
		t.Errorf("expected Offline, got %q", offline.Status)
	}
	if offline.Version != "v1.47.0" {
		t.Errorf("expected the last known version to survive going offline, got %q", offline.Version)
	}
}

// TestEdgeVersionUpdatesOnReconnect confirms preserving the old value does not stop a new
// one replacing it -- otherwise an upgraded edge would report its previous version forever.
func TestEdgeVersionUpdatesOnReconnect(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.DBPath = ""
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	srv.updateEdgeHealth("edge-in", "Online", 5, "", "v1.46.0", false)
	srv.updateEdgeHealth("edge-in", "Offline", 0, "disconnected", "", false)
	srv.updateEdgeHealth("edge-in", "Online", 5, "", "v1.47.0", false)

	srv.edgeHealthMu.RLock()
	got := srv.edgeHealth["edge-in"].Version
	srv.edgeHealthMu.RUnlock()

	if got != "v1.47.0" {
		t.Errorf("expected the newly reported version to replace the old one, got %q", got)
	}
}
