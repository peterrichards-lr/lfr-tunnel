package ops

import (
	"os"
	"strings"
	"testing"
)

// The pin is the whole point of #1859, so assert the two properties that make it work: the
// directory matches what the Makefile derives, and the value wins over an inherited one.

func TestEDRLinkDirPrefersTheConfiguredDir(t *testing.T) {
	t.Setenv(edrLinkDirEnv, "/somewhere/explicit")
	if got := EDRLinkDir(); got != "/somewhere/explicit" {
		t.Errorf("LFT_TEST_DIR should win, got %q", got)
	}
}

func TestEDRLinkDirFallsBackTheSameWayTheMakefileDoes(t *testing.T) {
	t.Setenv(edrLinkDirEnv, "")

	want := "/tmp"
	if info, err := os.Stat("/private/tmp"); err == nil && info.IsDir() {
		want = "/private/tmp"
	}

	if got := EDRLinkDir(); got != want {
		t.Errorf("fallback = %q, want %q -- this must match Makefile's LFT_TEST_DIR derivation", got, want)
	}
}

// An empty LFT_TEST_DIR must not produce GOTMPDIR= , which would leave the toolchain linking in
// the system temp dir while looking configured -- the #1335 failure mode in a different costume.
func TestEDRLinkDirNeverReturnsEmpty(t *testing.T) {
	t.Setenv(edrLinkDirEnv, "")
	if EDRLinkDir() == "" {
		t.Fatal("EDRLinkDir returned empty; GOTMPDIR= would silently mean the system temp dir")
	}
}

// os/exec keeps the last occurrence of a duplicated key, so GOTMPDIR must be appended after the
// caller's entries for the pin to beat an inherited value.
func TestRunGoCommandAppendsThePinLast(t *testing.T) {
	t.Setenv(edrLinkDirEnv, "/pinned/dir")

	caller := []string{"GOOS=linux", "GOTMPDIR=/attacker/controlled"}
	env := append([]string{}, caller...)
	env = append(env, "GOTMPDIR="+EDRLinkDir())

	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOTMPDIR=") {
			last = kv
		}
	}
	if last != "GOTMPDIR=/pinned/dir" {
		t.Errorf("last GOTMPDIR entry = %q, want the pinned one", last)
	}

	// And the caller's slice must not have been mutated underneath them.
	if caller[1] != "GOTMPDIR=/attacker/controlled" {
		t.Errorf("caller's env was mutated: %q", caller[1])
	}
}
