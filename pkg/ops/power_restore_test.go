package ops

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestPowerRestoreFailureIsRecorded covers the reporting side of #1183. The old behaviour
// was a lone Printf at the tail of a long deploy that affected nothing -- an unattended run
// exited 0 having left a node running outside its schedule.
func TestPowerRestoreFailureIsRecorded(t *testing.T) {
	t.Cleanup(func() { powerRestoreFailure = "" })

	powerRestoreFailure = ""
	if PowerRestoreFailure() != "" {
		t.Fatal("expected no failure recorded initially")
	}

	powerRestoreFailed("i-0123456789abcdef0", "apac.lfr-demo.se", "ap-northeast-1", "credentials expired")

	got := PowerRestoreFailure()
	if got == "" {
		t.Fatal("expected the failure to be recorded so deploy can fail on it")
	}
	// The operator has to be able to act on this without going digging.
	for _, want := range []string{"i-0123456789abcdef0", "apac.lfr-demo.se", "credentials expired", "RUNNING"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the recorded failure, got: %s", want, got)
		}
	}
}

// TestPowerRestoreFailureStartsClear guards against a stale failure from an earlier run
// bleeding into a later one within the same process.
func TestPowerRestoreFailureStartsClear(t *testing.T) {
	t.Cleanup(func() { powerRestoreFailure = "" })

	powerRestoreFailed("i-aaa", "one.example", "eu-west-1", "boom")
	if PowerRestoreFailure() == "" {
		t.Fatal("fixture failed")
	}

	powerRestoreFailure = ""
	if PowerRestoreFailure() != "" {
		t.Error("expected the failure to be clearable")
	}
}

// The bug this covers (#1191): describe-instances is eventually consistent, so the first
// read after a stop can still say "running". The old code took that single read as proof
// the instance was left up, which since #1184 fails the whole deploy -- and worse, teaches
// operators to ignore the one warning that means a node is genuinely stranded.
func TestConfirmStopped_ToleratesAStaleFirstRead(t *testing.T) {
	states := []string{"running", "running", "stopping"}
	var calls int
	readState := func() (string, error) {
		s := states[calls]
		calls++
		return s, nil
	}

	state, err := confirmStopped(readState, 5, 0)
	if err != nil {
		t.Fatalf("expected the stop to be confirmed once the state became visible, got: %v", err)
	}
	if state != "stopping" {
		t.Errorf("expected state %q, got %q", "stopping", state)
	}
	if calls != 3 {
		t.Errorf("expected it to stop re-reading as soon as the state proved the stop, got %d calls", calls)
	}
}

// The tests above pass attempts explicitly, so they would still pass if the value actually
// wired into the restore were 1 -- which is exactly the single-read behaviour #1191 is
// about. Pin the constants themselves.
func TestStopConfirmConstantsAllowForAStaleRead(t *testing.T) {
	if stopConfirmAttempts < 2 {
		t.Errorf("stopConfirmAttempts must allow at least one re-read, got %d", stopConfirmAttempts)
	}
	if stopConfirmDelay <= 0 {
		t.Errorf("stopConfirmDelay must give the state time to become visible, got %s", stopConfirmDelay)
	}
	// The whole point is to stay well short of aws ec2 wait instance-stopped's 30-90s.
	if total := time.Duration(stopConfirmAttempts-1) * stopConfirmDelay; total > 15*time.Second {
		t.Errorf("worst-case confirmation wait of %s is too long to add to every deploy teardown", total)
	}
}

// A read that succeeds immediately must not sleep or re-read -- the retry is for the stale
// case only, and this is the common path on every deploy that started an instance.
func TestConfirmStopped_ReturnsOnFirstProofWithoutRetrying(t *testing.T) {
	var calls int
	readState := func() (string, error) {
		calls++
		return "stopped", nil
	}

	state, err := confirmStopped(readState, 5, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "stopped" || calls != 1 {
		t.Errorf("expected a single read returning %q, got %q after %d calls", "stopped", state, calls)
	}
}

// Genuinely still running must still be reported. The retry must not turn a real stranded
// instance into a pass -- that would defeat the whole point of #1183.
func TestConfirmStopped_ReportsAnInstanceThatNeverStops(t *testing.T) {
	var calls int
	readState := func() (string, error) {
		calls++
		return "running", nil
	}

	state, err := confirmStopped(readState, 3, 0)
	if err != nil {
		t.Fatalf("expected no read error, got: %v", err)
	}
	if state != "running" {
		t.Errorf("expected the last observed state to be reported, got %q", state)
	}
	if calls != 3 {
		t.Errorf("expected all %d attempts to be used, got %d", 3, calls)
	}
}

// A read that never succeeds is a different outcome from "still running": the caller
// reports it as "accepted but could not be confirmed" rather than asserting either way.
func TestConfirmStopped_SurfacesAPersistentReadError(t *testing.T) {
	wantErr := errors.New("expired token")
	readState := func() (string, error) { return "", wantErr }

	_, err := confirmStopped(readState, 3, 0)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the read error to be surfaced so it is not mistaken for a running instance, got: %v", err)
	}
}

// A transient read failure that later succeeds should confirm, not fail. Credentials on
// this account are short-lived, so a single failed call mid-restore is expected.
func TestConfirmStopped_RecoversFromATransientReadError(t *testing.T) {
	var calls int
	readState := func() (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("temporary failure")
		}
		return "stopping", nil
	}

	state, err := confirmStopped(readState, 5, 0)
	if err != nil {
		t.Fatalf("expected recovery from a transient read error, got: %v", err)
	}
	if state != "stopping" {
		t.Errorf("expected state %q, got %q", "stopping", state)
	}
}
