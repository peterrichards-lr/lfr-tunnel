package provisioner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestGenerateOrLoadToken_GeneratesOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	token, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(token) != 64 { // 32 random bytes, hex-encoded
		t.Errorf("expected a 64-char hex token, got %d chars", len(token))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file was not created: %v", err)
	}
	// Windows has no POSIX permission bits -- os.WriteFile's mode argument is
	// only meaningfully honored on Unix-like systems there (see the same
	// pattern in pkg/config/config_test.go).
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("expected token file permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestGenerateOrLoadToken_LoadsExistingOnSecondRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	first, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := GenerateOrLoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Error("expected the same token to be loaded on the second call, got a different one")
	}
}

func TestGenerateOrLoadToken_RejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := GenerateOrLoadToken(path); err == nil {
		t.Fatal("expected error for empty token file")
	}
}

func TestValidToken(t *testing.T) {
	if !ValidToken("secret", "secret") {
		t.Error("expected matching tokens to be valid")
	}
	if ValidToken("secret", "wrong") {
		t.Error("expected mismatched tokens to be invalid")
	}
	if ValidToken("secret", "") {
		t.Error("expected empty presented token to be invalid")
	}
}

// TestLoadTokenSeparatesTheWaysItCanFail is the assertion that would have caught #1956.
//
// Every state below already returned a non-nil error, so a test asserting "an error
// occurred" passes against the defect -- that is exactly why the defect survived. What was
// missing is that the four errors were indistinguishable to the caller: pkg/server turned
// all of them, plus "no sidecar configured at all", into one nil client and one sentence
// telling the admin the feature was not configured on this server.
//
// So this asserts the error IDENTITY, and that the path is named where there is one to
// name. An operator's next action differs for each: set the setting, fix the typo, fix the
// permissions, wait for the sidecar to write the file.
func TestLoadTokenSeparatesTheWaysItCanFail(t *testing.T) {
	dir := t.TempDir()

	t.Run("path unset", func(t *testing.T) {
		_, err := LoadToken("")
		if !errors.Is(err, ErrTokenPathUnset) {
			t.Errorf("an unset edge_provisioner_token_file: got %v, want ErrTokenPathUnset", err)
		}
		// It must not look like a mistyped path: there is no path to check.
		if errors.Is(err, ErrTokenNotFound) {
			t.Errorf("an unset path reported ErrTokenNotFound (%v) -- nothing was configured to be missing", err)
		}
	})

	t.Run("mistyped path", func(t *testing.T) {
		typo := filepath.Join(dir, "edge-provisioner.tokne")
		_, err := LoadToken(typo)
		if !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("a configured path with no file at it: got %v, want ErrTokenNotFound", err)
		}
		// The admin panel names the path back so a typo is self-evident, and it reads it
		// from here. A sentinel that dropped the path would leave the message no better
		// than the one it replaced.
		if err == nil || !strings.Contains(err.Error(), typo) {
			t.Errorf("error %v does not name the configured path %q", err, typo)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty")
		if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_, err := LoadToken(path)
		if !errors.Is(err, ErrTokenEmpty) {
			t.Errorf("a whitespace-only token file: got %v, want ErrTokenEmpty", err)
		}
		// Distinct from a missing file on purpose: this one means the sidecar created the
		// file and has not written to it, which a path check will never explain.
		if errors.Is(err, ErrTokenNotFound) {
			t.Errorf("an empty file reported ErrTokenNotFound (%v) -- the file is right there", err)
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits are not honoured on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root reads a 0000 file regardless of its mode")
		}
		path := filepath.Join(dir, "unreadable")
		if err := os.WriteFile(path, []byte("deadbeef\n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("setup: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Logf("cleanup: restoring mode on %s: %v", path, err)
			}
		})

		_, err := LoadToken(path)
		if !errors.Is(err, ErrTokenUnreadable) {
			t.Errorf("a token file the process cannot open: got %v, want ErrTokenUnreadable", err)
		}
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("error %v does not name the configured path %q", err, path)
		}
	})

	t.Run("loads a good file", func(t *testing.T) {
		path := filepath.Join(dir, "good")
		if err := os.WriteFile(path, []byte("  abc123\n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		token, err := LoadToken(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "abc123" {
			t.Errorf("got token %q, want the trimmed file contents", token)
		}
	})
}

// TestLoadTokenErrorsNeverCarryTheToken guards the half of #1956 that must NOT improve.
//
// The diagnosis these errors feed reaches an admin's browser and this repo's journal, so
// saying WHY the load failed must never become saying WHAT the file contained. Length and a
// prefix are deliberately included in the check: either would narrow a shared secret, and
// "it is 64 characters and starts with a7" is a leak with extra steps.
func TestLoadTokenErrorsNeverCarryTheToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not honoured on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file regardless of its mode")
	}
	// Built rather than written out: a 64-char hex literal in a test file is what a real
	// token looks like, and the repo's own gitleaks pre-commit hook blocks one on sight --
	// correctly, since nothing about the file it is in makes it obviously fake.
	secret := strings.Repeat("fixture-not-a-real-token.", 3)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Logf("cleanup: restoring mode on %s: %v", path, err)
		}
	})

	_, err := LoadToken(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable token file")
	}
	// The configured path is expected in the message and is not the secret, so it is taken
	// out before the checks below. Without that, the length check would be comparing against
	// the random digits in t.TempDir()'s name and could fail for no reason at all.
	msg := strings.ReplaceAll(err.Error(), path, "<path>")
	if strings.Contains(msg, secret) {
		t.Fatal("the error carries the token itself")
	}
	if strings.Contains(msg, secret[:12]) {
		t.Errorf("the error carries a prefix of the token: %s", msg)
	}
	if strings.Contains(msg, strconv.Itoa(len(secret))) {
		t.Errorf("the error appears to carry the token's length: %s", msg)
	}
}
