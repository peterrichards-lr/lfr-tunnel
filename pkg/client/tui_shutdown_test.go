package client

import (
	"strings"
	"testing"
	"time"
)

// The countdown is the visible half of #1239: the warning already reaches the client and
// the log, but a user watching the TUI would still be surprised by the drop.

func TestShutdownCountdownLine_EmptyWhenNoneAnnounced(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	if line := shutdownCountdownLine(e); line != "" {
		t.Errorf("expected nothing to render, got %q", line)
	}
}

func TestShutdownCountdownLine_ShowsTimeRemainingAndReason(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:       "node_shutdown_warning",
		ShutdownAt: time.Now().Add(4 * time.Minute).Unix(),
		Reason:     "Scheduled edge node shutdown",
	})

	line := shutdownCountdownLine(e)
	for _, want := range []string{"shutting down", "3m", "Scheduled edge node shutdown"} {
		if !strings.Contains(line, want) {
			t.Errorf("expected %q in %q", want, line)
		}
	}
}

// Once the moment passes the line stays, because the shutdown is still the most useful
// thing on screen -- a countdown that vanished at zero would leave the tunnel dropping
// with no explanation.
func TestShutdownCountdownLine_SaysNowOncePassed(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:       "node_shutdown_warning",
		ShutdownAt: time.Now().Add(-30 * time.Second).Unix(),
		Reason:     "deploy",
	})

	line := shutdownCountdownLine(e)
	if !strings.Contains(line, "now") {
		t.Errorf("expected an elapsed shutdown to read as now, got %q", line)
	}
	if strings.Contains(line, "-") {
		t.Errorf("a negative countdown should never render, got %q", line)
	}
}

// Recomputed from the announced time rather than the last reported figure, so it ticks
// smoothly between heartbeats instead of stalling and jumping.
func TestFormatCountdown(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:              "45s",
		90 * time.Second:              "1m 30s",
		5 * time.Minute:               "5m 00s",
		2*time.Minute + 5*time.Second: "2m 05s",
	}
	for d, want := range cases {
		if got := formatCountdown(d); got != want {
			t.Errorf("formatCountdown(%s) = %q, want %q", d, got, want)
		}
	}
}
