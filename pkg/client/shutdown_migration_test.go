package client

import (
	"context"
	"testing"
	"time"
)

// TestConsumeShutdownMigration covers the read-and-clear that tells the session loop a move
// was planned rather than forced (#1246).
//
// It has to fire exactly once. Consuming twice would make the loop treat an ordinary
// reconnect as a planned move and hold a healthy gateway out of the candidate set for an
// hour.
func TestConsumeShutdownMigration(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)

	if _, _, ok := e.ConsumeShutdownMigration(); ok {
		t.Error("a fresh engine must not report a pending move")
	}

	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
		SecondsRemaining: 300,
		Reason:           "Scheduled edge node shutdown",
	})

	at, reason, ok := e.ConsumeShutdownMigration()
	if !ok {
		t.Fatal("expected a pending move after a shutdown warning")
	}
	if at == 0 {
		t.Error("expected the announced shutdown time to be reported")
	}
	if reason != "Scheduled edge node shutdown" {
		t.Errorf("reason = %q, want the gateway's reason", reason)
	}

	if _, _, ok := e.ConsumeShutdownMigration(); ok {
		t.Error("expected the signal to be cleared, so one warning moves the client once")
	}
}

// TestRepeatedWarningDoesNotReMigrate is the important one. Central re-sends the warning on
// every health cycle inside the window -- roughly five times for a five-minute warning -- and
// the client sees it on every heartbeat. Only the first should move it; the rest must not
// keep tearing the new session down.
func TestRepeatedWarningDoesNotReMigrate(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	at := time.Now().Add(5 * time.Minute).Unix()

	warn := &NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		ShutdownAt:       at,
		SecondsRemaining: 300,
		Reason:           "Scheduled edge node shutdown",
	}

	e.noteShutdownWarning(warn)
	if _, _, ok := e.ConsumeShutdownMigration(); !ok {
		t.Fatal("expected the first warning to raise a move")
	}

	// The same shutdown, announced again on later heartbeats.
	for i := 0; i < 4; i++ {
		e.noteShutdownWarning(warn)
	}
	if _, _, ok := e.ConsumeShutdownMigration(); ok {
		t.Error("repeats of the same shutdown must not raise another move")
	}

	// A genuinely new shutdown -- the next night, or a different gateway -- must.
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		ShutdownAt:       time.Now().Add(24 * time.Hour).Unix(),
		SecondsRemaining: 300,
		Reason:           "Scheduled edge node shutdown",
	})
	if _, _, ok := e.ConsumeShutdownMigration(); !ok {
		t.Error("a different shutdown must raise a fresh move")
	}
}

// TestShutdownMigratorEndsTheSession checks the watcher actually cancels, which is what hands
// control back to the session loop's existing failover path rather than duplicating it.
func TestShutdownMigratorEndsTheSession(t *testing.T) {
	original := shutdownMigrationPollInterval
	shutdownMigrationPollInterval = 10 * time.Millisecond
	defer func() { shutdownMigrationPollInterval = original }()

	e := NewInterceptorEngine("127.0.0.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.StartShutdownMigrator(ctx, cancel)

	// Nothing announced yet: the session must be left alone.
	time.Sleep(50 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("the migrator must not end a session with no shutdown announced")
	}

	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
		SecondsRemaining: 300,
		Reason:           "Scheduled edge node shutdown",
	})

	deadline := time.After(2 * time.Second)
	for ctx.Err() == nil {
		select {
		case <-deadline:
			t.Fatal("expected the session to be ended once a shutdown was announced")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestShutdownMigratorStopsWithTheSession checks the watcher exits when its session does,
// rather than outliving it and cancelling a later one.
func TestShutdownMigratorStopsWithTheSession(t *testing.T) {
	original := shutdownMigrationPollInterval
	shutdownMigrationPollInterval = 10 * time.Millisecond
	defer func() { shutdownMigrationPollInterval = original }()

	e := NewInterceptorEngine("127.0.0.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	e.StartShutdownMigrator(ctx, cancel)
	cancel()

	time.Sleep(50 * time.Millisecond)

	// A warning arriving after the session ended must leave the signal for the next
	// session to consume, not be swallowed by a dead watcher.
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
		SecondsRemaining: 300,
		Reason:           "Scheduled edge node shutdown",
	})
	if !e.PendingShutdownMigration() {
		t.Error("the pending move should survive for the next session to act on")
	}
}
