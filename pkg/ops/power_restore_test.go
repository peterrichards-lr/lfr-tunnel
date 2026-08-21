package ops

import (
	"strings"
	"testing"
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
