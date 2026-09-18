package server

import (
	"testing"
	"time"
)

// waitUntil polls until cond holds, or fails naming what never happened.
//
// Polled rather than slept: the paths these tests drive cross servers, websockets, timers and
// goroutines, and a fixed sleep is either slow on every run or flaky on a loaded one. Polling to
// a generous deadline fails safe in the direction #1390 established -- a slow machine polls for
// longer rather than reporting a defect that is not there.
//
// It lives in its own file, rather than beside the first test that needed it, so that the next
// test with a "wait for this to happen" is more likely to find it than to grow another one
// (#2026). Written for the edge node-set tests (#1960); now also used by the diagnostics and
// maintenance-countdown tests.
func waitUntil(t *testing.T, whatShouldHappen func() string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after 10s waiting for: %s", whatShouldHappen())
}

// stated is the fixed-string form of waitUntil's message, for the waits whose failure has
// only one possible cause.
func stated(msg string) func() string { return func() string { return msg } }
