package server

import (
	"testing"
	"time"
)

// The auto-ban threshold is meant to describe a burst, not a lifetime total (#1327). These tests
// pin that distinction, because the difference between the two only shows up after the process
// has been running long enough for nobody to be watching.

func newViolationTestServer() *Server {
	return &Server{violations: make(map[string]*ipViolations)}
}

// The case the change exists for: violations spread out over a long period must not accumulate
// into a ban. A shared corporate NAT egress in front of a team is exactly this shape.
func TestViolations_DoNotAccumulateAcrossTheWindow(t *testing.T) {
	s := newViolationTestServer()

	original := violationWindow
	violationWindow = 50 * time.Millisecond
	t.Cleanup(func() { violationWindow = original })

	for i := 0; i < 49; i++ {
		s.recordViolation("203.0.113.9")
	}

	// Long enough that the earlier run has expired.
	time.Sleep(70 * time.Millisecond)

	if got := s.recordViolation("203.0.113.9"); got != 1 {
		t.Errorf("count after the window elapsed = %d, want 1 -- old violations were carried forward, so a single request today would ban an address that misbehaved months ago", got)
	}
}

// The protection still has to work: a genuine burst inside the window reaches the threshold.
func TestViolations_AccumulateWithinTheWindow(t *testing.T) {
	s := newViolationTestServer()

	var last int
	for i := 0; i < 50; i++ {
		last = s.recordViolation("203.0.113.9")
	}

	if last != 50 {
		t.Errorf("count within the window = %d, want 50 -- a real burst must still reach the ban threshold", last)
	}
}

func TestViolations_AreCountedPerAddress(t *testing.T) {
	s := newViolationTestServer()

	s.recordViolation("203.0.113.9")
	s.recordViolation("203.0.113.9")
	other := s.recordViolation("198.51.100.4")

	if other != 1 {
		t.Errorf("second address started at %d, want 1 -- counts must not be shared between addresses", other)
	}
}

// Decay is applied when the count is read, not only by the ten-minute cleaner. Between ticks a
// stale count would otherwise still be live, and the ban it triggers is permanent.
func TestViolations_DecayWithoutWaitingForTheCleaner(t *testing.T) {
	s := newViolationTestServer()

	original := violationWindow
	violationWindow = 20 * time.Millisecond
	t.Cleanup(func() { violationWindow = original })

	s.recordViolation("203.0.113.9")
	time.Sleep(40 * time.Millisecond)

	// No cleaner is running in this test at all.
	if got := s.recordViolation("203.0.113.9"); got != 1 {
		t.Errorf("count = %d, want 1 -- decay must not depend on the cleaner having run", got)
	}
}

// The map held one entry per address that had ever tripped the limiter, for the life of the
// process. On an internet-facing gateway under routine scanning that only grows.
func TestViolations_StaleEntriesArePruned(t *testing.T) {
	s := newViolationTestServer()

	original := violationWindow
	violationWindow = 20 * time.Millisecond
	t.Cleanup(func() { violationWindow = original })

	s.recordViolation("203.0.113.9")
	s.recordViolation("198.51.100.4")
	time.Sleep(40 * time.Millisecond)
	s.recordViolation("203.0.113.77") // recent, must survive

	// Exercise the same pruning the cleaner performs on its ticker.
	now := time.Now()
	s.vMutex.Lock()
	for ip, entry := range s.violations {
		if now.Sub(entry.lastSeen) > violationWindow {
			delete(s.violations, ip)
		}
	}
	remaining := len(s.violations)
	s.vMutex.Unlock()

	if remaining != 1 {
		t.Errorf("entries remaining = %d, want 1 -- stale addresses are retained for the life of the process", remaining)
	}
}
