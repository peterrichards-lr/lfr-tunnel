package server

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingHook stands in for the operator's script, so the debouncing, the withdrawal grace
// and the migration ordering are all testable without executing anything.
type recordingHook struct {
	mu    sync.Mutex
	calls []string
	fail  bool
	done  chan struct{}
}

func (h *recordingHook) run(action string, args ...string) error {
	h.mu.Lock()
	call := action
	for _, a := range args {
		call += " " + a
	}
	h.calls = append(h.calls, call)
	fail := h.fail
	if h.done != nil {
		select {
		case h.done <- struct{}{}:
		default:
		}
	}
	h.mu.Unlock()
	if fail {
		return errors.New("hook failed")
	}
	return nil
}

func (h *recordingHook) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// testWithdrawGrace is the grace used by every test that actually lets the withdrawal timer
// fire. Windows' default timer granularity is ~15.6ms, so the 1ms and 10ms values used before
// fired at an unpredictable point relative to the surrounding calls -- which is how two
// different tests failed on two consecutive runs of an unchanged commit (#1362). Comfortably
// above that resolution on every platform, and still short enough not to slow the suite.
const testWithdrawGrace = 60 * time.Millisecond

// newTestPublisher wires a publisher to a recording hook. Publish runs the hook on a
// goroutine, so tests wait on `done` rather than sleeping.
func newTestPublisher(grace time.Duration) (*dnsPublisher, *recordingHook) {
	hook := &recordingHook{done: make(chan struct{}, 16)}
	p := newDNSPublisher("/nonexistent/hook.sh", grace)
	p.run = hook.run
	return p, hook
}

// waitForPublished waits until the publisher has *committed* a name's target, rather than until
// the hook was merely invoked. The hook signals when it is called; the record of where the name
// now points is written after it returns. Assertions that depend on that record have to wait for
// it, or they are reading state the test never synchronised on (#1362).
func waitForPublished(t *testing.T, p *dnsPublisher, fqdn, target string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := p.published[fqdn]
		p.mu.Unlock()
		if got == target {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s was never recorded as pointing at %s", fqdn, target)
}

// waitForIntentCleared waits until the publisher has recorded that a name is no longer on its
// way anywhere -- which is what a failed publish leaves behind. A test asserting "the next
// registration retries after a failure" has to wait for the failure to be *recorded*, not merely
// for the hook to have been *called*: publishing in between is inside the debounce window by
// design, and would be suppressed (#1362).
func waitForIntentCleared(t *testing.T, p *dnsPublisher, fqdn string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		_, still := p.desired[fqdn]
		p.mu.Unlock()
		if !still {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the failed publish for %s was never cleared, so a retry would stay debounced", fqdn)
}

func waitForCall(t *testing.T, hook *recordingHook) {
	t.Helper()
	select {
	case <-hook.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("hook was never called; calls so far: %v", hook.seen())
	}
}

func TestDNSPublisher_NotConfiguredDoesNothing(t *testing.T) {
	hook := &recordingHook{}
	p := newDNSPublisher("", 0)
	p.run = hook.run

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")

	if calls := hook.seen(); len(calls) != 0 {
		t.Fatalf("an unconfigured hook must leave DNS entirely alone, got %v", calls)
	}
}

func TestDNSPublisher_PublishesAndDebouncesUnchangedTarget(t *testing.T) {
	p, hook := newTestPublisher(time.Hour)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	// A client reconnecting to the same gateway must not cost a provider call.
	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	time.Sleep(50 * time.Millisecond)

	calls := hook.seen()
	if len(calls) != 1 || calls[0] != "upsert peters.lfr-demo.se in.lfr-demo.se" {
		t.Fatalf("expected exactly one upsert, got %v", calls)
	}
}

func TestDNSPublisher_MovingGatewaysRepublishes(t *testing.T) {
	p, hook := newTestPublisher(time.Hour)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)
	p.Publish("peters.lfr-demo.se", "tunnel.lfr-demo.se")
	waitForCall(t, hook)

	calls := hook.seen()
	if len(calls) != 2 || calls[1] != "upsert peters.lfr-demo.se tunnel.lfr-demo.se" {
		t.Fatalf("a move must republish at the new gateway, got %v", calls)
	}
}

// The case that makes a planned move safe: the old gateway's withdrawal is already waiting
// when the new gateway publishes. It must abandon itself rather than delete the record that
// just replaced it.
func TestDNSPublisher_WithdrawAbandonedWhenNameIsReclaimed(t *testing.T) {
	p, hook := newTestPublisher(time.Hour)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	p.mu.Lock()
	staleGen := p.gen["peters.lfr-demo.se"]
	p.mu.Unlock()

	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")
	p.Publish("peters.lfr-demo.se", "tunnel.lfr-demo.se")
	waitForCall(t, hook)

	// Fire the pending withdrawal by hand rather than waiting out a grace period.
	p.withdrawNow("peters.lfr-demo.se", "in.lfr-demo.se", staleGen)

	for _, call := range hook.seen() {
		if call == "delete peters.lfr-demo.se" {
			t.Fatalf("a stale withdrawal deleted a record that had been reclaimed: %v", hook.seen())
		}
	}
}

func TestDNSPublisher_WithdrawDeletesAfterGrace(t *testing.T) {
	p, hook := newTestPublisher(testWithdrawGrace)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	calls := hook.seen()
	if len(calls) != 2 || calls[1] != "delete peters.lfr-demo.se" {
		t.Fatalf("expected the record to be withdrawn after the grace period, got %v", calls)
	}

	// And the name is forgotten, so a later withdrawal for it does nothing.
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")
	time.Sleep(50 * time.Millisecond)
	if got := len(hook.seen()); got != 2 {
		t.Fatalf("withdrawing an already-withdrawn name called the hook again: %v", hook.seen())
	}
}

// Deleting a name this process never published would take out a static record an operator put
// there by hand -- the wildcard, or a fixed entry for the control plane itself.
func TestDNSPublisher_WithdrawIgnoresUnknownName(t *testing.T) {
	p, hook := newTestPublisher(testWithdrawGrace)

	p.Withdraw("not-ours.lfr-demo.se", "in.lfr-demo.se")
	time.Sleep(50 * time.Millisecond)

	if calls := hook.seen(); len(calls) != 0 {
		t.Fatalf("expected no call for a name we never published, got %v", calls)
	}
}

// A failed upsert must not be remembered as published, or the debounce would suppress the
// retry that a later registration would otherwise make.
func TestDNSPublisher_FailedPublishIsNotRecorded(t *testing.T) {
	p, hook := newTestPublisher(time.Hour)
	hook.fail = true

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)
	// Wait for the failure to be recorded, not just for the hook to have been entered.
	waitForIntentCleared(t, p, "peters.lfr-demo.se")

	hook.mu.Lock()
	hook.fail = false
	hook.mu.Unlock()

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	if got := len(hook.seen()); got != 2 {
		t.Fatalf("a failed publish should be retried by the next registration, got %v", hook.seen())
	}
}

// The ordering the multi-region E2E actually produced, and which the generation counter alone
// did not survive: the client registers on the new gateway first (make-before-break), and the
// OLD gateway's deregistration only reaches central afterwards. Its withdrawal is therefore
// the most recent event for the name, so nothing about "has anything happened since?" saves
// the record -- only knowing that the name no longer points at the gateway giving it up.
func TestDNSPublisher_LateWithdrawFromOldGatewayDoesNotDeleteNewRecord(t *testing.T) {
	// An hour's grace so the timer can never fire on its own: the withdrawal is driven
	// directly instead. Asserting "no delete happened" by sleeping means racing a timer to
	// prove a negative, which is what made this test flaky on Windows (#1362) -- and a sleep
	// long enough to be safe is a sleep long enough to be slow.
	p, hook := newTestPublisher(time.Hour)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	// The move: central publishes as the client registers there...
	p.Publish("peters.lfr-demo.se", "tunnel.lfr-demo.se")
	waitForCall(t, hook)
	waitForPublished(t, p, "peters.lfr-demo.se", "tunnel.lfr-demo.se")

	// ...and only then does the edge report that it has torn its lease down. Withdraw bumps
	// the generation and schedules the delete; running that scheduled work by hand is exactly
	// what the timer would have done, minus the timing.
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")

	p.mu.Lock()
	g := p.gen["peters.lfr-demo.se"]
	p.mu.Unlock()
	p.withdrawNow("peters.lfr-demo.se", "in.lfr-demo.se", g)

	for _, call := range hook.seen() {
		if call == "delete peters.lfr-demo.se" {
			t.Fatalf("the old gateway deleted a record that now points at the new one: %v", hook.seen())
		}
	}
}

// And the same withdrawal must still work in the ordinary case, where the name does still
// point at the gateway giving it up -- otherwise nothing is ever cleaned up.
func TestDNSPublisher_WithdrawFromCurrentGatewayStillDeletes(t *testing.T) {
	p, hook := newTestPublisher(testWithdrawGrace)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	calls := hook.seen()
	if calls[len(calls)-1] != "delete peters.lfr-demo.se" {
		t.Fatalf("expected the holding gateway to be able to withdraw its own record, got %v", calls)
	}
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://in.lfr-demo.se":       "in.lfr-demo.se",
		"http://tunnel.lfr-demo.local": "tunnel.lfr-demo.local",
		"https://in.lfr-demo.se:8443":  "in.lfr-demo.se",
		"in.lfr-demo.se":               "in.lfr-demo.se", // operators write it without a scheme
		"  https://in.lfr-demo.se  ":   "in.lfr-demo.se",
		"":                             "",
	}
	for in, want := range cases {
		if got := hostFromURL(in); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The bug behind the flake, as a property rather than a timing artefact (#1362).
//
// Publish used to debounce against `published`, which is only written once the hook returns. A
// second request arriving during a provider call therefore saw "nothing published yet" and
// called the provider again. On a real provider that window is the whole API call, not a
// microsecond -- so this was a duplicate write in production, not merely a noisy test.
func TestDNSPublisher_DebouncesWhileTheFirstCallIsStillRunning(t *testing.T) {
	hook := &recordingHook{done: make(chan struct{}, 16)}
	release := make(chan struct{})
	p := newDNSPublisher("/nonexistent/hook.sh", time.Hour)

	// Hold the first invocation open, so the second Publish lands squarely inside it.
	p.run = func(action string, args ...string) error {
		err := hook.run(action, args...)
		if action == "upsert" {
			<-release
		}
		return err
	}

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook) // the hook is now running and has not returned

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	close(release)

	// Give any erroneous second invocation time to appear.
	time.Sleep(50 * time.Millisecond)

	if calls := hook.seen(); len(calls) != 1 {
		t.Fatalf("the same target was published twice because the first call had not finished: %v", calls)
	}
}

// Ordering, which the generation counter alone did not provide: it decided which invocation got
// to *record* the result, not which one reached the provider first. Publish(A) then Publish(B)
// could land A after B, leaving DNS on the old gateway while the recorded state said the new
// one -- after which every later Publish(B) debounced and the name stayed wrong for good.
func TestDNSPublisher_SerialisesInvocationsForOneName(t *testing.T) {
	hook := &recordingHook{done: make(chan struct{}, 16)}
	p := newDNSPublisher("/nonexistent/hook.sh", time.Hour)

	var mu sync.Mutex
	concurrent := 0
	maxConcurrent := 0

	p.run = func(action string, args ...string) error {
		mu.Lock()
		concurrent++
		if concurrent > maxConcurrent {
			maxConcurrent = concurrent
		}
		mu.Unlock()

		time.Sleep(5 * time.Millisecond) // wide enough for an overlap to show
		err := hook.run(action, args...)

		mu.Lock()
		concurrent--
		mu.Unlock()
		return err
	}

	for _, target := range []string{"a.lfr-demo.se", "b.lfr-demo.se", "c.lfr-demo.se", "d.lfr-demo.se"} {
		p.Publish("peters.lfr-demo.se", target)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		idle := concurrent == 0
		mu.Unlock()
		if idle && len(hook.seen()) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	peak := maxConcurrent
	mu.Unlock()
	if peak > 1 {
		t.Fatalf("hook invocations for one name overlapped (peak %d); the provider can see them out of order", peak)
	}
}
