package client

import (
	"strings"
	"testing"
)

// TestPinnedShutdownNotice covers the disclosure for a client pinned with -server (#1275).
//
// Such a client never fails over -- the failover path is gated on !isExplicitServer -- which
// is the correct contract for a flag meaning "use this and nothing else". The gap was that
// nobody was told. Measured against edge-in on 2026-08-23: the tunnel dropped at its
// scheduled stop and stayed down 24m36s, having been given no prior indication.
func TestPinnedShutdownNotice(t *testing.T) {
	scheduled := &RegisterResponse{
		NodeStopsInSeconds: 6*3600 + 12*60,
		NodeStopTime:       "00:00",
		NodeTimezone:       "Asia/Kolkata",
	}

	t.Run("a pinned client on a scheduled gateway is warned", func(t *testing.T) {
		notice := PinnedShutdownNotice(scheduled, true)
		if notice == "" {
			t.Fatal("expected a warning")
		}
		for _, want := range []string{"00:00", "Asia/Kolkata", "6h 12m", "-region"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice should mention %q, got: %s", want, notice)
			}
		}
	})

	t.Run("an unpinned client is not warned", func(t *testing.T) {
		// It will fail over on its own, so the warning would be untrue as well as noisy.
		if notice := PinnedShutdownNotice(scheduled, false); notice != "" {
			t.Errorf("expected silence for a client that can fail over, got: %s", notice)
		}
	})

	t.Run("a pinned client on an unscheduled gateway is not warned", func(t *testing.T) {
		// The control plane is deliberately never scheduled, so this is the common case for
		// anyone pinned to it.
		unscheduled := &RegisterResponse{}
		if notice := PinnedShutdownNotice(unscheduled, true); notice != "" {
			t.Errorf("expected silence when no stop is scheduled, got: %s", notice)
		}
	})

	t.Run("a nil response does not panic", func(t *testing.T) {
		if notice := PinnedShutdownNotice(nil, true); notice != "" {
			t.Errorf("expected silence, got: %s", notice)
		}
	})
}

// TestFormatTimeUntil pins the coarse rendering. tui.go's formatCountdown renders minutes and
// seconds because it ticks down a five-minute warning; reusing it here would print
// "372m 30s" for a stop six hours away.
func TestFormatTimeUntil(t *testing.T) {
	cases := []struct {
		seconds int
		want    string
	}{
		{6*3600 + 12*60, "6h 12m"},
		{3600, "1h 0m"},
		{45 * 60, "45m"},
		{30, "30s"},
	}
	for _, tc := range cases {
		if got := formatTimeUntil(tc.seconds); got != tc.want {
			t.Errorf("formatTimeUntil(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}
