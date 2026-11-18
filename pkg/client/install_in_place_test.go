package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
)

// #2505: install and upgrade write the executable straight to the install path, on every OS. The
// current binary is renamed to .old and put back on any failure; it is deleted on success, or at
// the next start on Windows, where the running image is locked.

// recordExecutableWrites makes every executable an upgrade creates observable.
func recordExecutableWrites(t *testing.T) *[]string {
	t.Helper()
	var paths []string
	orig := createExecutable
	createExecutable = func(path string) (*os.File, error) {
		paths = append(paths, path)
		return orig(path)
	}
	t.Cleanup(func() { createExecutable = orig })
	return &paths
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func installedBinary(t *testing.T, content string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), canonicalExeName())
	if content != "" {
		if err := os.WriteFile(target, []byte(content), 0o755); err != nil { //nolint:gosec // a stand-in for the client binary
			t.Fatal(err)
		}
	}
	return target
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func assertAbsent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists: %s", path, why)
	}
}

func TestInstallInPlaceReplacesTheBinaryWhereItIs(t *testing.T) {
	target := installedBinary(t, "old")
	written := recordExecutableWrites(t)

	backup, err := installInPlace(target, strings.NewReader("new"), hashOf("new"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("target holds %q, want the new binary", got)
	}
	if len(*written) != 1 || (*written)[0] != target {
		t.Errorf("executables were created at %q, want only the install path %q", *written, target)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(target); err != nil || info.Mode()&0o111 == 0 {
			t.Errorf("the new binary is not executable (%v)", err)
		}
	}
	// The previous binary waits beside it until the upgrade has finished with it.
	if backup != target+upgradeBackupSuffix || readFile(t, backup) != "old" {
		t.Fatalf("backup = %q, want the previous binary at %s", backup, target+upgradeBackupSuffix)
	}
	removeUpgradeBackup(backup)
	assertAbsent(t, backup, "the previous binary survived a successful upgrade")
}

func TestInstallInPlaceOnAFreshMachine(t *testing.T) {
	target := installedBinary(t, "")
	backup, err := installInPlace(target, strings.NewReader("new"), hashOf("new"))
	if err != nil || backup != "" || readFile(t, target) != "new" {
		t.Errorf("fresh install: backup %q, err %v", backup, err)
	}
}

func TestInstallInPlaceKeepsThePreviousBinaryOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.Reader
		hash string
	}{
		{"truncated download", io.MultiReader(strings.NewReader("ne"), iotest.ErrReader(errors.New("connection reset"))), hashOf("new")},
		{"checksum mismatch", strings.NewReader("tampered"), hashOf("new")},
		{"empty download", strings.NewReader(""), hashOf("new")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := installedBinary(t, "old")
			if _, err := installInPlace(target, tc.body, tc.hash); err == nil {
				t.Fatal("a failed download was installed")
			}
			if got := readFile(t, target); got != "old" {
				t.Errorf("after a failed download the install path holds %q, want the previous binary", got)
			}
			assertAbsent(t, target+upgradeBackupSuffix, "the rollback left the previous binary aside")
		})
	}

	t.Run("fresh machine", func(t *testing.T) {
		target := installedBinary(t, "")
		if _, err := installInPlace(target, strings.NewReader("tampered"), hashOf("new")); err == nil {
			t.Fatal("a tampered download was installed")
		}
		assertAbsent(t, target, "a partial download was left at the install path")
	})
}

// On Windows the delete after an upgrade fails while the old image runs; the next start clears it.
func TestTheNextStartRemovesTheUpgradeBackup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	backup := exe + upgradeBackupSuffix
	if err := os.WriteFile(backup, []byte("old"), 0o600); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(backup) })
	RemoveUpgradeBackup()
	assertAbsent(t, backup, "the backup an upgrade left was not removed at the next start")
}

// No client code writes an executable to a temp directory. Behaviour is covered above; this keeps
// the old spellings from coming back, as the #1592 check does for the install paths.
func TestNoClientCodeStagesTheBinaryInATempDirectory(t *testing.T) {
	staging := regexp.MustCompile(`"/tmp/|"/private/tmp|os\.TempDir\(\)|update-tmp`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f) //nolint:gosec // this package's own source
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if staging.MatchString(line) {
				t.Errorf("%s:%d stages a file in a temp directory: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
