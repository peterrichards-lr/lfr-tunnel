package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeExe(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // a fake client binary
		t.Fatal(err)
	}
}

func mkLinkDir(t *testing.T, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
}

func gone(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// The installer's symlink, naming the binary an upgrade replaced or the one it installed, is
// removed; the binaries are untouched (#2579).
func TestRetireLDMLinkRemovesTheInstallersSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege Windows runners do not grant; Windows uses the hardlink form")
	}
	dir := t.TempDir()
	oldExe, newExe := filepath.Join(dir, "old", "lfr-tunnel"), filepath.Join(dir, "new", "lfr-tunnel")
	writeExe(t, oldExe, "old")
	writeExe(t, newExe, "new")
	for name, points := range map[string]string{"names the old binary": oldExe, "names the new binary": newExe} {
		link := filepath.Join(dir, name, ".ldm", "bin", "lfr-tunnel")
		mkLinkDir(t, link)
		if err := os.Symlink(points, link); err != nil {
			t.Fatal(err)
		}
		if err := retireLDMLink(link, oldExe, newExe, false); err != nil {
			t.Fatalf("%s: retireLDMLink: %v", name, err)
		}
		if !gone(t, link) {
			t.Errorf("a link that %s was left in place", name)
		}
	}
	if gone(t, oldExe) || gone(t, newExe) {
		t.Error("retiring the link removed a binary")
	}
}

// A link that names nothing is broken whoever made it, so it goes too.
func TestRetireLDMLinkRemovesADanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege Windows runners do not grant")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, ".ldm", "bin", "lfr-tunnel")
	mkLinkDir(t, link)
	if err := os.Symlink(filepath.Join(dir, "gone", "lfr-tunnel"), link); err != nil {
		t.Fatal(err)
	}
	if err := retireLDMLink(link, filepath.Join(dir, "a"), filepath.Join(dir, "b"), false); err != nil {
		t.Fatal(err)
	}
	if !gone(t, link) {
		t.Error("a dangling LDM link was left in place")
	}
}

// What the installers did not make is the user's: a symlink to their own build and a regular file
// both stay, and an absent link is not an error.
func TestRetireLDMLinkLeavesWhatTheInstallerDidNotMake(t *testing.T) {
	dir := t.TempDir()
	oldExe, newExe := filepath.Join(dir, "old", "lfr-tunnel"), filepath.Join(dir, "new", "lfr-tunnel")
	writeExe(t, oldExe, "old")
	writeExe(t, newExe, "new")

	if err := retireLDMLink(filepath.Join(dir, "absent", "lfr-tunnel"), oldExe, newExe, false); err != nil {
		t.Errorf("an absent link: %v", err)
	}

	plain := filepath.Join(dir, "plain", "lfr-tunnel")
	writeExe(t, plain, "users-own")
	if err := retireLDMLink(plain, oldExe, newExe, false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(plain); err != nil || string(got) != "users-own" { //nolint:gosec // test path
		t.Errorf("a regular file at the LDM link was removed or changed: %q (%v)", got, err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	mine := filepath.Join(dir, "src", "lfr-tunnel")
	writeExe(t, mine, "mine")
	users := filepath.Join(dir, "users", "lfr-tunnel")
	mkLinkDir(t, users)
	if err := os.Symlink(mine, users); err != nil {
		t.Fatal(err)
	}
	if err := retireLDMLink(users, oldExe, newExe, false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(users); err != nil || got != mine {
		t.Errorf("a link to the user's own build was removed or changed: %q (%v)", got, err)
	}
}

// The Windows form: the hardlink, and the copy the installer fell back to, are both removed, even
// stale (the in-place case, where the hardlink still holds the previous version). Runs on every
// OS, since os.Link makes hardlinks everywhere.
func TestRetireLDMLinkRemovesTheWindowsHardlinkOrCopy(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "lfr-tunnel.exe")
	writeExe(t, exe, "old")

	hard := filepath.Join(dir, "hard", ".ldm", "bin", "lfr-tunnel.exe")
	mkLinkDir(t, hard)
	if err := os.Link(exe, hard); err != nil {
		t.Fatal(err)
	}
	// The in-place upgrade: the binary renamed aside and a new file written at the same path.
	if err := os.Rename(exe, exe+".old"); err != nil {
		t.Fatal(err)
	}
	writeExe(t, exe, "new")

	copied := filepath.Join(dir, "copy", ".ldm", "bin", "lfr-tunnel.exe")
	writeExe(t, copied, "old")

	for _, link := range []string{hard, copied} {
		if err := retireLDMLink(link, exe, exe, true); err != nil {
			t.Fatalf("retireLDMLink(%s): %v", link, err)
		}
		if !gone(t, link) || !gone(t, link+".old") {
			t.Errorf("%s (or its aside copy) was left behind", link)
		}
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "new" { //nolint:gosec // test path
		t.Errorf("the installed binary was touched: %q (%v)", got, err)
	}
}

// An aside copy a previous run could not delete (a running image) is cleared by the next one,
// even once the link itself is gone.
func TestRetireLDMLinkClearsALeftoverAsideCopy(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, ".ldm", "bin", "lfr-tunnel.exe")
	writeExe(t, link+".old", "stale")
	if err := retireLDMLink(link, "x", "y", true); err != nil {
		t.Fatal(err)
	}
	if !gone(t, link+".old") {
		t.Error("the leftover aside copy was not cleared")
	}
}

func TestRepointRCLine(t *testing.T) {
	dir := t.TempDir()
	oldExe, newExe := "/home/u/liferay/lfr-tunnel/lfr-tunnel", "/home/u/elsewhere/lfr-tunnel"
	rc := filepath.Join(dir, ".zshrc")
	// The user's own line is longer than the installer's, so a rewrite keyed on the prefix alone
	// would reach it.
	users := `export LDM_LFR_TUNNEL_BIN="/home/u/src/lfr-tunnel/bin/my-own-build/lfr-tunnel"`
	original := "alias ll='ls -l'\n" + ldmRCLine(oldExe) + "\n" + users + "\n"
	if err := os.WriteFile(rc, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := repointRCLine(rc, oldExe, newExe); err != nil {
		t.Fatalf("repointRCLine: %v", err)
	}
	got, err := os.ReadFile(rc) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	want := "alias ll='ls -l'\n" + ldmRCLine(newExe) + "\n" + users + "\n"
	if string(got) != want {
		t.Errorf("rc file =\n%s\nwant\n%s", got, want)
	}
	if info, err := os.Stat(rc); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("rc mode changed to %v", info.Mode().Perm())
	}

	// A missing rc file is not an error.
	if err := repointRCLine(filepath.Join(dir, ".bashrc"), oldExe, newExe); err != nil {
		t.Errorf("missing rc: %v", err)
	}
}

// install.sh writes the path as given; the upgrade knows the binary by its resolved path. A
// symlinked install directory spells them differently, and the line must still be found, or the
// old binary is removed under a variable that still names it (#2553 review).
func TestRepointRCLineMatchesThroughASymlinkedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh's rc files are a macOS and Linux concern")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "volume", "liferay")
	writeExe(t, filepath.Join(real, "lfr-tunnel"), "old")
	linked := filepath.Join(dir, "liferay")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	oldExe, err := filepath.EvalSymlinks(filepath.Join(linked, "lfr-tunnel"))
	if err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(dir, ".zshrc")
	if err := os.WriteFile(rc, []byte(ldmRCLine(filepath.Join(linked, "lfr-tunnel"))+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := repointRCLine(rc, oldExe, "/new/lfr-tunnel"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(rc); err != nil || string(got) != ldmRCLine("/new/lfr-tunnel")+"\n" { //nolint:gosec // test path
		t.Errorf("rc = %q (%v)", got, err)
	}
}

// An rc file kept in a dotfiles repository and symlinked in stays a symlink: the file it names is
// rewritten, not replaced by a copy.
func TestRepointRCLineThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh's rc files are a macOS and Linux concern")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "zshrc")
	writeExe(t, real, ldmRCLine("/a/lfr-tunnel")+"\n")
	rc := filepath.Join(dir, ".zshrc")
	if err := os.Symlink(real, rc); err != nil {
		t.Fatal(err)
	}
	if err := repointRCLine(rc, "/a/lfr-tunnel", "/b/lfr-tunnel"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(rc); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the rc symlink was replaced by a file")
	}
	if got, err := os.ReadFile(real); err != nil || !strings.Contains(string(got), `"/b/lfr-tunnel"`) { //nolint:gosec // test path
		t.Errorf("the dotfiles copy was not updated: %q (%v)", got, err)
	}
}

func TestReplacePathEntry(t *testing.T) {
	got, ok := replacePathEntry(`C:\a;C:\Users\u\Liferay\LFR-Tunnel\;C:\b`, `C:\Users\u\liferay\lfr-tunnel`, `C:\Users\u\new`, ";", true)
	if !ok || got != `C:\a;C:\Users\u\new;C:\b` {
		t.Errorf("Windows replace = %q, %v", got, ok)
	}
	if _, ok := replacePathEntry("/usr/bin:/home/u/Liferay/lfr-tunnel", "/home/u/liferay/lfr-tunnel", "/x", ":", false); ok {
		t.Error("a case-sensitive PATH matched a differently cased directory")
	}
	if got, ok := replacePathEntry("/usr/bin::/old", "/old", "/new", ":", false); !ok || got != "/usr/bin::/new" {
		t.Errorf("an empty entry was disturbed: %q, %v", got, ok)
	}
}
