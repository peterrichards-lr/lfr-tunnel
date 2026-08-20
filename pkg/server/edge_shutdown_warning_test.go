package server

import (
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func TestServerConfig_EdgeShutdownWarningDefaults(t *testing.T) {
	cfg := config.DefaultServerConfig()
	if cfg.EdgeShutdownWarningMinutes != 5 {
		t.Errorf("expected default EdgeShutdownWarningMinutes to be 5, got %d", cfg.EdgeShutdownWarningMinutes)
	}
}

func TestSecondsUntilScheduledStop(t *testing.T) {
	// Target stop time: 22:00 UTC
	stopHHMM := "22:00"
	tz := "UTC"

	// Test 1: 21:55 UTC -> 300 seconds until stop
	loc, _ := time.LoadLocation(tz)
	now := time.Date(2026, 8, 20, 21, 55, 0, 0, loc)
	sec, ok := secondsUntilScheduledStop(now, stopHHMM, tz)
	if !ok {
		t.Fatalf("expected ok to be true")
	}
	if sec != 300 {
		t.Errorf("expected 300 seconds until stop, got %d", sec)
	}

	// Test 2: Invalid timezone -> ok is false
	_, okBad := secondsUntilScheduledStop(now, stopHHMM, "Invalid/Timezone")
	if okBad {
		t.Errorf("expected ok to be false for invalid timezone")
	}
}

func TestServer_CheckEdgeShutdownWarnings_ScheduledWindow(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.EdgeShutdownWarningMinutes = 5

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	srv.edgeHealthMu.Lock()
	srv.edgeHealth["edge-us"] = EdgeHealthStatus{
		Status:           "Online",
		ScheduleEnabled:  true,
		ScheduleStopTime: "22:00",
		Timezone:         "UTC",
	}
	srv.edgeHealthMu.Unlock()

	// 21:56 UTC is within 5-minute warning window (240 seconds remaining)
	loc, _ := time.LoadLocation("UTC")
	nowInWindow := time.Date(2026, 8, 20, 21, 56, 0, 0, loc)
	srv.checkEdgeShutdownWarnings(nowInWindow)
}
