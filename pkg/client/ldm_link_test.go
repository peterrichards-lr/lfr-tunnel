package client

import (
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

func TestRepointLDMLinkSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege Windows runners do not grant; Windows uses the hardlink form")
	}
	dir := t.TempDir()
	oldExe, newExe := filepath.Join(dir, "old", "lfr-tunnel"), filepath.Join(dir, "new", "lfr-tunnel")
	writeExe(t, oldExe, "old")
	writeExe(t, newExe, "new")
	link := filepath.Join(dir, ".ldm", "bin", "lfr-tunnel")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldExe, link); err != nil {
		t.Fatal(err)
	}

	if err := repointLDMLink(link, newExe, false); err != nil {
		t.Fatalf("repointLDMLink: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != newExe {
		t.Errorf("link names %q (%v), want %q", got, err, newExe)
	}
	if _, err := os.Lstat(link + ".new"); err == nil {
		t.Error("the temporary link was left behind")
	}
}

// The Windows form: a hardlink to the old binary's content is replaced by one to the new binary,
// which is what the installer would make. Runs on every OS, since os.Link makes hardlinks
// everywhere.
func TestRepointLDMLinkHardlink(t *testing.T) {
	dir := t.TempDir()
	oldExe, newExe := filepath.Join(dir, "old", "lfr-tunnel.exe"), filepath.Join(dir, "new", "lfr-tunnel.exe")
	writeExe(t, oldExe, "old")
	writeExe(t, newExe, "new")
	link := filepath.Join(dir, ".ldm", "bin", "lfr-tunnel.exe")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(oldExe, link); err != nil {
		t.Fatal(err)
	}

	if err := repointLDMLink(link, newExe, true); err != nil {
		t.Fatalf("repointLDMLink: %v", err)
	}
	li, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	ni, err := os.Stat(newExe)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(li, ni) {
		got, err := os.ReadFile(link) //nolint:gosec // test path
		t.Errorf("the LDM link is not the new binary; it holds %q (%v)", got, err)
	}
}

// The in-place case on Windows: the upgrade renamed the binary aside and wrote a new file at the
// same path, so the hardlink still holds the previous version's content.
func TestRepointLDMLinkHardlinkAfterAnInPlaceUpgrade(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "lfr-tunnel.exe")
	writeExe(t, exe, "old")
	link := filepath.Join(dir, ".ldm", "bin", "lfr-tunnel.exe")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(exe, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(exe, exe+".old"); err != nil {
		t.Fatal(err)
	}
	writeExe(t, exe, "new")
	if err := os.Remove(exe + ".old"); err != nil {
		t.Fatal(err)
	}

	if err := repointLDMLink(link, exe, true); err != nil {
		t.Fatalf("repointLDMLink: %v", err)
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "new" { //nolint:gosec // test path
		t.Errorf("the LDM link holds %q (%v), want the upgraded binary", got, err)
	}
}

func TestRepointLDMLinkLeavesWhatTheInstallerDidNotMake(t *testing.T) {
	dir := t.TempDir()
	newExe := filepath.Join(dir, "new", "lfr-tunnel")
	writeExe(t, newExe, "new")

	// No link: not created. The installer makes it; a user who removed it chose to.
	absent := filepath.Join(dir, "absent", "lfr-tunnel")
	if err := repointLDMLink(absent, newExe, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(absent); err == nil {
		t.Error("an absent LDM link was created")
	}

	// A regular file on Unix is not the installer's symlink: left as it is.
	plain := filepath.Join(dir, "plain", "lfr-tunnel")
	writeExe(t, plain, "users-own")
	if err := repointLDMLink(plain, newExe, false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(plain); err != nil || string(got) != "users-own" { //nolint:gosec // test path
		t.Errorf("a regular file at the LDM link was replaced: %q (%v)", got, err)
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
