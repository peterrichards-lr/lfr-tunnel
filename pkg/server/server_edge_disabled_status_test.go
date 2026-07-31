package server

import (
	"testing"
	"time"
)

// TestIsWithinScheduledDowntime covers the modular-arithmetic window check
// (#887): same-day windows, overnight-wrap windows, the grace period just
// inside/outside its boundary, and malformed input.
func TestIsWithinScheduledDowntime(t *testing.T) {
	const tz = "UTC"

	mustTime := func(hh, mm, ss int) time.Time {
		return time.Date(2026, 1, 15, hh, mm, ss, 0, time.UTC)
	}

	tests := []struct {
		name             string
		now              time.Time
		stopHHMM         string
		startHHMM        string
		tz               string
		wantWithinWindow bool
	}{
		{"same-day window, well inside", mustTime(2, 0, 0), "00:00", "08:00", tz, true},
		{"same-day window, before stop", mustTime(23, 0, 0), "00:00", "08:00", tz, false},
		{"same-day window, just inside grace after start", mustTime(8, 4, 0), "00:00", "08:00", tz, true},
		{"same-day window, past grace after start", mustTime(8, 6, 0), "00:00", "08:00", tz, false},
		{"same-day window, exactly at start", mustTime(8, 0, 0), "00:00", "08:00", tz, true},
		{"overnight-wrap window, before midnight", mustTime(23, 0, 0), "22:00", "06:00", tz, true},
		{"overnight-wrap window, after midnight, before start", mustTime(2, 0, 0), "22:00", "06:00", tz, true},
		{"overnight-wrap window, just inside grace after start", mustTime(6, 4, 30), "22:00", "06:00", tz, true},
		{"overnight-wrap window, past grace after start", mustTime(6, 10, 0), "22:00", "06:00", tz, false},
		{"overnight-wrap window, outside entirely (afternoon)", mustTime(14, 0, 0), "22:00", "06:00", tz, false},
		{"equal stop/start is invalid, never in window", mustTime(2, 0, 0), "08:00", "08:00", tz, false},
		{"empty stop time", mustTime(2, 0, 0), "", "08:00", tz, false},
		{"empty start time", mustTime(2, 0, 0), "00:00", "", tz, false},
		{"empty timezone", mustTime(2, 0, 0), "00:00", "08:00", "", false},
		{"malformed stop time", mustTime(2, 0, 0), "not-a-time", "08:00", tz, false},
		{"invalid hour", mustTime(2, 0, 0), "25:00", "08:00", tz, false},
		{"unknown timezone", mustTime(2, 0, 0), "00:00", "08:00", "Not/AZone", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinScheduledDowntime(tt.now, tt.stopHHMM, tt.startHHMM, tt.tz)
			if got != tt.wantWithinWindow {
				t.Errorf("isWithinScheduledDowntime(%v, %q, %q, %q) = %v, want %v",
					tt.now, tt.stopHHMM, tt.startHHMM, tt.tz, got, tt.wantWithinWindow)
			}
		})
	}
}

// TestUpdateEdgeHealth_AdminDisabledShowsAsDisabledNotOffline is the
// regression test for the portal-stop case (#887): a node stopped via the
// portal must show "Disabled", not the alarming "Offline", and the raw
// connection error should be suppressed.
func TestUpdateEdgeHealth_AdminDisabledShowsAsDisabledNotOffline(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.setEdgeAdminDisabled("edge-test", true)
	srv.updateEdgeHealth("edge-test", "Offline", 0, "dial tcp: connection refused", "", false)

	h := srv.edgeHealth["edge-test"]
	if h.Status != "Disabled" {
		t.Errorf("expected Status = Disabled, got %q", h.Status)
	}
	if h.ErrorMessage != "" {
		t.Errorf("expected ErrorMessage to be suppressed for a Disabled node, got %q", h.ErrorMessage)
	}
}

// TestUpdateEdgeHealth_OutOfPortalStopIsStillOffline is the regression test
// for the explicit product requirement in #887: a node stopped some other
// way (AWS console/CLI, bypassing the portal) has no AdminDisabled signal
// and no active schedule window, so it must report as a genuine "Offline"
// outage, not "Disabled".
func TestUpdateEdgeHealth_OutOfPortalStopIsStillOffline(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// No setEdgeAdminDisabled call, no schedule configured (provisionerClient
	// is nil in the test server) -- nothing accounts for this being down on
	// purpose.
	srv.updateEdgeHealth("edge-test", "Offline", 0, "dial tcp: connection refused", "", false)

	h := srv.edgeHealth["edge-test"]
	if h.Status != "Offline" {
		t.Errorf("expected Status = Offline for an out-of-portal stop, got %q", h.Status)
	}
	if h.ErrorMessage == "" {
		t.Error("expected the real connection error to be preserved for a genuine Offline outage")
	}
}

// TestUpdateEdgeHealth_MaintenanceActiveShowsAsDisabled covers the soft-
// maintenance case (#887): the node is fully reachable (health check
// succeeds) but is running in maintenance mode, which should still surface
// as "Disabled" rather than plain "Online".
func TestUpdateEdgeHealth_MaintenanceActiveShowsAsDisabled(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.updateEdgeHealth("edge-test", "Online", 42, "", "v1.0.0", true)

	h := srv.edgeHealth["edge-test"]
	if h.Status != "Disabled" {
		t.Errorf("expected Status = Disabled while maintenance is active, got %q", h.Status)
	}
}

// TestUpdateEdgeHealth_AdminDisabledClearedByOnlineCheck confirms the flag
// doesn't stick forever: once a health check finds the node genuinely
// Online again, status reports Online regardless of any stale AdminDisabled
// state (e.g. someone started it back up outside the portal).
func TestUpdateEdgeHealth_AdminDisabledClearedByOnlineCheck(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	srv.setEdgeAdminDisabled("edge-test", true)
	srv.updateEdgeHealth("edge-test", "Online", 10, "", "v1.0.0", false)

	h := srv.edgeHealth["edge-test"]
	if h.Status != "Online" {
		t.Errorf("expected Status = Online once the health check succeeds, got %q", h.Status)
	}
}
