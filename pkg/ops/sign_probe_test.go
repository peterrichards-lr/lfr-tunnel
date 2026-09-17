package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dry run must be able to FAIL (#1978).
//
// It reported "Windows: will be signed" and the real run then failed on the password. These
// cases pin the probe that closes that gap, and the control below is what stops them passing on
// a probe that rejects everything.

func probeWith(t *testing.T, runErr error, hasOpenssl bool) (CredentialProbe, string) {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "win.key")
	if err := os.WriteFile(key, []byte("-----BEGIN ENCRYPTED PRIVATE KEY-----\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return CredentialProbe{
		LookPath: func(string) (string, error) {
			if hasOpenssl {
				return "/usr/bin/openssl", nil
			}
			return "", errors.New("not found")
		},
		Run:       func(string, ...string) error { return runErr },
		WriteTemp: writeSecretFile,
	}, key
}

// CONTROL. Without this every case below could pass on a probe that always reports a problem.
func TestProbeAcceptsAKeyThatOpens(t *testing.T) {
	p, key := probeWith(t, nil, true)
	if err := ProbeWindowsKey(p, key, "correct-horse"); err != nil {
		t.Fatalf("CONTROL: a working key was rejected (%v) -- the failure cases prove nothing", err)
	}
	if got := DescribeProbe(nil); !strings.Contains(got, "verified") {
		t.Fatalf("a passing probe does not read as verified: %q", got)
	}
}

// FIRING. The v1.48.35 failure: the key is present and readable, the password is wrong.
func TestProbeCatchesAWrongPasswordBeforeSigning(t *testing.T) {
	p, key := probeWith(t, errors.New("exit status 1"), true)
	err := ProbeWindowsKey(p, key, "wrong")
	if err == nil {
		t.Fatal("a key that cannot be opened was reported as fine -- this is the defect")
	}
	if errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("a real failure was reported as 'could not check': %v", err)
	}
	// The message has to connect to what the operator will otherwise see hours later.
	if !strings.Contains(err.Error(), "maybe wrong password") {
		t.Fatalf("the failure does not connect to osslsigncode's later error: %v", err)
	}
	if got := DescribeProbe(err); !strings.HasPrefix(got, "FAILED:") {
		t.Fatalf("a failing probe does not read as failed: %q", got)
	}
}

// BOUNDING. "Cannot check" must never render as a pass -- that is the whole bug class.
func TestAnUncheckableProbeIsNotAPass(t *testing.T) {
	p, key := probeWith(t, nil, false) // openssl missing
	err := ProbeWindowsKey(p, key, "x")
	if err == nil {
		t.Fatal("a probe that could not run reported success")
	}
	if !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("expected an unavailable probe, got %v", err)
	}
	got := DescribeProbe(err)
	if !strings.HasPrefix(got, "NOT CHECKED:") {
		t.Fatalf("an unrunnable probe must say so plainly, got %q", got)
	}
	if strings.Contains(got, "verified") {
		t.Fatalf("an unrunnable probe rendered as verified: %q", got)
	}
}

// An op:// reference reaching the probe means the re-exec did not happen. Reporting it as a
// credential failure would be wrong -- nothing has been tested.
func TestAnUnresolvedReferenceIsReportedAsUncheckedNotBroken(t *testing.T) {
	p, _ := probeWith(t, nil, true)
	err := ProbeWindowsKey(p, "op://Employee/item/key", "x")
	if !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("an unresolved op:// reference should be 'not checked', got %v", err)
	}
	if !strings.Contains(err.Error(), "op://") {
		t.Fatalf("the message does not say the reference was never resolved: %v", err)
	}
}

// A path that is neither a file nor inline PEM is a real configuration error, not "unavailable".
func TestAKeyThatIsNeitherFileNorPemIsAFailure(t *testing.T) {
	p, _ := probeWith(t, nil, true)
	err := ProbeWindowsKey(p, "/nonexistent/path/to/win.key", "x")
	if err == nil || errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("an unusable key path was not reported as a failure: %v", err)
	}
}
