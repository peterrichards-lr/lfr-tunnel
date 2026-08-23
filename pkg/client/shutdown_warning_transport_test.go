package client

import (
	"testing"
)

// The warning arrives on the tunnel-status heartbeat, which is the one gateway-to-client
// channel the client already listens to (#1238). ParseNodeShutdownWarning existed with a
// test but no caller, because it had been written for a transport that did not exist.

func TestNoteShutdownWarning_RecordsAndExposes(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)

	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		SecondsRemaining: 240,
		ShutdownAt:       1893456000,
		Reason:           "Scheduled edge node shutdown",
	})

	at, secs, reason := e.ShutdownWarning()
	if at != 1893456000 || secs != 240 || reason != "Scheduled edge node shutdown" {
		t.Errorf("got at=%d secs=%d reason=%q", at, secs, reason)
	}
}

// Nothing announced means nothing reported -- callers use the zero time to decide whether
// there is a shutdown at all.
func TestShutdownWarning_ZeroWhenNoneAnnounced(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	if at, secs, reason := e.ShutdownWarning(); at != 0 || secs != 0 || reason != "" {
		t.Errorf("expected no warning, got at=%d secs=%d reason=%q", at, secs, reason)
	}
}

// A warning with no shutdown time is not a warning; recording it would leave the engine
// claiming a shutdown is pending with nothing to say about when.
func TestNoteShutdownWarning_IgnoresAnEmptyWarning(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.noteShutdownWarning(nil)
	e.noteShutdownWarning(&NodeShutdownWarning{Type: "node_shutdown_warning"})
	if at, _, _ := e.ShutdownWarning(); at != 0 {
		t.Errorf("expected an empty warning to be ignored, got at=%d", at)
	}
}

// The countdown updates on every heartbeat, so the recorded seconds must follow it -- a
// stale countdown is worse than none once it passes zero.
func TestNoteShutdownWarning_CountdownFollowsTheHeartbeat(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	for _, secs := range []int{300, 240, 180} {
		e.noteShutdownWarning(&NodeShutdownWarning{
			Type:             "node_shutdown_warning",
			SecondsRemaining: secs,
			ShutdownAt:       1893456000,
			Reason:           "deploy",
		})
	}
	if _, secs, _ := e.ShutdownWarning(); secs != 180 {
		t.Errorf("expected the latest countdown, got %d", secs)
	}
}

// The parser is what makes the heartbeat body usable, and it keys on the type field, so a
// plain acknowledgement must not be mistaken for a warning.
func TestParseNodeShutdownWarning_IgnoresAPlainAcknowledgement(t *testing.T) {
	if _, ok := ParseNodeShutdownWarning([]byte(`{"status":"ok"}`)); ok {
		t.Error("a plain ok body must not parse as a shutdown warning")
	}
	if _, ok := ParseNodeShutdownWarning([]byte("")); ok {
		t.Error("an empty body must not parse as a shutdown warning")
	}
}
