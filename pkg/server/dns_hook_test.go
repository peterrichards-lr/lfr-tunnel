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

// newTestPublisher wires a publisher to a recording hook. Publish runs the hook on a
// goroutine, so tests wait on `done` rather than sleeping.
func newTestPublisher(grace time.Duration) (*dnsPublisher, *recordingHook) {
	hook := &recordingHook{done: make(chan struct{}, 16)}
	p := newDNSPublisher("/nonexistent/hook.sh", grace)
	p.run = hook.run
	return p, hook
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
	p, hook := newTestPublisher(10 * time.Millisecond)

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
	p, hook := newTestPublisher(time.Millisecond)

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
	p, hook := newTestPublisher(time.Millisecond)

	p.Publish("peters.lfr-demo.se", "in.lfr-demo.se")
	waitForCall(t, hook)

	// The move: central publishes as the client registers there...
	p.Publish("peters.lfr-demo.se", "tunnel.lfr-demo.se")
	waitForCall(t, hook)

	// ...and only then does the edge report that it has torn its lease down.
	p.Withdraw("peters.lfr-demo.se", "in.lfr-demo.se")
	time.Sleep(80 * time.Millisecond)

	for _, call := range hook.seen() {
		if call == "delete peters.lfr-demo.se" {
			t.Fatalf("the old gateway deleted a record that now points at the new one: %v", hook.seen())
		}
	}
}

// And the same withdrawal must still work in the ordinary case, where the name does still
// point at the gateway giving it up -- otherwise nothing is ever cleaned up.
func TestDNSPublisher_WithdrawFromCurrentGatewayStillDeletes(t *testing.T) {
	p, hook := newTestPublisher(time.Millisecond)

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
