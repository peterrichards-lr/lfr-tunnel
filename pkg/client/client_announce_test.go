package client

import (
	"context"
	"testing"
	"time"
)

// TestWaitForConnected is the regression test for #1258. The "Tunnel is active and fully
// online!" banner used to fire on a 500ms timer guarded only by ctx.Err() == nil, which
// asks whether the context was cancelled rather than whether anything connected. Start()
// returns as soon as the chisel client has been started, so every failed reconnect attempt
// announced success -- observed live during a scheduled edge shutdown, where the log
// claimed "fully online" three times while the client was attached to nothing and the TUI
// correctly showed OFFLINE.
//
// The distinction that matters is "reconnecting" vs "connected": the first must never
// announce, however long it goes on for.
func TestWaitForConnected(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"connected announces", "connected", true},
		// Everything below is the #1258 bug: a retry loop sits in these states, and each
		// pass used to print a success banner.
		{"reconnecting must not announce", "reconnecting", false},
		{"disconnected must not announce", "disconnected", false},
		{"connecting must not announce", "connecting", false},
		{"unset state must not announce", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewInterceptorEngine("127.0.0.1", nil)
			e.mu.Lock()
			e.ConnState = tc.state
			e.mu.Unlock()

			// Short timeout: a false result is reached by exhausting it, and the test
			// should not pay connectAnnounceTimeout to find that out.
			if got := waitForConnected(context.Background(), e, 150*time.Millisecond); got != tc.want {
				t.Errorf("waitForConnected() with state %q = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

// TestWaitForConnectedLateConnection covers the ordinary case: the banner goroutine starts
// before chisel has logged "Connected (Latency ...)", so the state flips while it waits.
// Polling rather than sampling once is what makes this work.
func TestWaitForConnectedLateConnection(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.mu.Lock()
	e.ConnState = "connecting"
	e.mu.Unlock()

	go func() {
		time.Sleep(150 * time.Millisecond)
		e.mu.Lock()
		e.ConnState = "connected"
		e.mu.Unlock()
	}()

	if !waitForConnected(context.Background(), e, 5*time.Second) {
		t.Error("expected a connection established during the wait to be announced")
	}
}

// TestWaitForConnectedContextCancelled checks the shutdown path: a client stopped while
// still trying to connect must not print a success banner on its way out.
func TestWaitForConnectedContextCancelled(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.mu.Lock()
	e.ConnState = "reconnecting"
	e.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if waitForConnected(ctx, e, 10*time.Second) {
		t.Error("a cancelled context must not announce success")
	}
	// Returning on cancellation rather than waiting out the timeout is the point.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("expected prompt return on cancellation, took %v", elapsed)
	}
}
