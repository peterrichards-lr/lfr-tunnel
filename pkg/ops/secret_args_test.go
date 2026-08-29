package ops

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// The signing password reached a terminal, a scrollback buffer and a log file before anyone
// noticed, because the command runner prints every argument it is handed and sign.go handed it
// `-pass <the password>` (#1555). These tests pin both halves of the fix: the value is masked
// when a command is echoed, and it is written to a 0600 file so it never reaches argv at all.

// Deliberately low-entropy and self-describing. An authentic-looking value trips the repo's
// own gitleaks hook, and a test fixture is not worth an entry in .gitleaksignore -- each one
// there is a place the scanner has been told to look away.
const probePassword = "example-not-a-real-password"

func TestRedactArgsMasksTheValueAfterASecretFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"osslsigncode -pass", []string{"sign", "-pass", probePassword, "-in", "a.exe"}},
		{"gpg --passphrase", []string{"--batch", "--passphrase", probePassword, "--detach-sign"}},
		{"short -p", []string{"-p", probePassword}},
		{"--password", []string{"--password", probePassword}},
		{"--token", []string{"--token", probePassword}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactArgs(tc.args)
			if strings.Contains(got, probePassword) {
				t.Fatalf("rendered command still contains the secret: %q", got)
			}
			if !strings.Contains(got, "<redacted>") {
				t.Fatalf("expected a redaction marker, got %q", got)
			}
		})
	}
}

// A password is only useful to an attacker in full, but a partial leak is still a leak worth
// failing on -- and a naive "replace the value" implementation could leave a prefix behind.
func TestRedactArgsLeaksNoFragmentOfTheSecret(t *testing.T) {
	got := redactArgs([]string{"sign", "-pass", probePassword, "-n", "Liferay Tunnel"})
	for size := 6; size < len(probePassword); size++ {
		fragment := probePassword[:size]
		if strings.Contains(got, fragment) {
			t.Fatalf("rendered command leaks the %d-char prefix %q: %q", size, fragment, got)
		}
	}
}

// Redaction must not swallow the parts of the command a reader needs in order to debug it.
func TestRedactArgsKeepsNonSecretArguments(t *testing.T) {
	got := redactArgs([]string{
		"sign", "-key", "/tmp/win.key", "-certs", "/tmp/win.crt",
		"-pass", probePassword, "-in", "lfr-tunnel-windows-amd64.exe",
	})

	for _, want := range []string{"-key", "/tmp/win.key", "-certs", "/tmp/win.crt", "-in", "lfr-tunnel-windows-amd64.exe"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction removed %q, which is not a secret: %q", want, got)
		}
	}
}

// Only the argument immediately following a secret flag is a credential. Masking further than
// that would hide the rest of the command, and masking a flag that merely looks secret-ish
// would hide a path.
func TestRedactArgsMasksExactlyOneArgument(t *testing.T) {
	got := redactArgs([]string{"-pass", probePassword, "-in", "binary.exe"})
	if strings.Count(got, "<redacted>") != 1 {
		t.Fatalf("expected exactly one redaction, got %q", got)
	}
	if !strings.Contains(got, "binary.exe") {
		t.Fatalf("redaction ran past the credential: %q", got)
	}
}

func TestRedactArgsIgnoresFlagsThatMerelyContainPass(t *testing.T) {
	got := redactArgs([]string{"--passphrase-file", "/tmp/gpgpass", "--bypass", "yes"})
	if strings.Contains(got, "<redacted>") {
		t.Fatalf("--passphrase-file takes a path, not a secret, and must not be masked: %q", got)
	}
}

// The file-based path is the part that actually keeps the secret off argv, so its permissions
// matter as much as its contents.
func TestWriteSecretFileIsNotReadableByOthers(t *testing.T) {
	path, err := writeSecretFile(probePassword, "probe-*")
	if err != nil {
		t.Fatalf("writeSecretFile: %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Unix permission bits do not survive the trip to Windows: os.Chmod there only toggles the
	// read-only attribute, so a file written as 0600 stats as 0666 and this assertion failed CI
	// on windows-latest while passing everywhere else.
	//
	// Skipping rather than loosening the check, because the property still holds on Windows --
	// it is just enforced by something this cannot see. os.CreateTemp writes into the per-user
	// temp directory, whose ACL already excludes other users; the mode bits were never the
	// mechanism there. Asserting 0666 "is fine on Windows" would encode the read as the rule and
	// hide a genuine regression on the platform where the bits do mean something.
	if runtime.GOOS == "windows" {
		t.Log("skipping the mode assertion: Windows does not model Unix permission bits; " +
			"the credential file is protected by the per-user temp directory ACL instead")
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credential file is mode %04o, want 0600", perm)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != probePassword {
		t.Fatalf("file contents round-tripped wrong: got %q", string(body))
	}
}
