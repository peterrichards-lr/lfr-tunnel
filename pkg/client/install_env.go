package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Besides the LDM link, the installers write the client's location into the user's environment
// (#1311): LDM_LFR_TUNNEL_BIN, in the shell rc files on macOS and Linux and as a user variable on
// Windows, where the install directory is also added to the user PATH. After a move to the
// gateway's install_dir (#2553) each of these names the old location, so LDM would look for a
// binary that is gone and fall back to downloading an unexcluded copy of its own.

// ldmBinEnvVar is the variable LDM reads for this client's location.
const ldmBinEnvVar = "LDM_LFR_TUNNEL_BIN"

// repointInstallEnv re-points what the installers wrote naming oldExe at newExe. Only values the
// installers would have written are changed; anything else is the user's. A variable so the
// upgrade's ordering can be tested without touching a home directory or the registry.
var repointInstallEnv = func(home, oldExe, newExe string) error {
	return repointInstallEnvPlatform(home, oldExe, newExe)
}

// ldmRCLine is the line install.sh appends to a shell rc file.
func ldmRCLine(exe string) string {
	return `export ` + ldmBinEnvVar + `="` + exe + `"`
}

// ldmRCValue returns the path in a line of exactly install.sh's form, and whether it is one.
func ldmRCValue(line string) (string, bool) {
	const prefix = `export ` + ldmBinEnvVar + `="`
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `"`) || len(line) <= len(prefix) {
		return "", false
	}
	return line[len(prefix) : len(line)-1], true
}

// namesFile reports whether path is exe: equal, or the same once symlinks are resolved. install.sh
// writes the path as it was given, while the upgrade knows the binary by its resolved path, so a
// symlinked home or install directory spells them differently (#2553 review).
func namesFile(path, exe string) bool {
	if path == exe {
		return true
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == exe
}

// repointRCLine replaces install.sh's LDM_LFR_TUNNEL_BIN line naming oldExe in rc with one for
// newExe. Only a line in install.sh's exact form whose path is oldExe (directly or through a
// symlink) is changed; any other line, including one the user edited, is theirs.
//
// The rc file may be a symlink into a dotfiles repository, so the file it resolves to is
// rewritten, through a temporary file beside it and a rename, keeping its mode.
func repointRCLine(rc, oldExe, newExe string) error {
	path, err := filepath.EvalSymlinks(rc)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolving %s: %w", rc, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own shell rc file
	if err != nil {
		return fmt.Errorf("reading %s: %w", rc, err)
	}
	newLine := ldmRCLine(newExe)
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, l := range lines {
		body := strings.TrimRight(l, "\r")
		if value, ok := ldmRCValue(body); ok && namesFile(value, oldExe) {
			lines[i] = newLine + l[len(body):]
			changed = true
		}
	}
	if !changed {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rc, err)
	}
	tmp := path + ".lfr-tunnel.new"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
		return fmt.Errorf("rewriting %s: %w", rc, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		if rerr := os.Remove(tmp); rerr != nil {
			return fmt.Errorf("rewriting %s: %w (and %s is left behind: %v)", rc, err, tmp, rerr)
		}
		return fmt.Errorf("rewriting %s: %w", rc, err)
	}
	fmt.Printf("[Update] %s in %s now names %s.\n", ldmBinEnvVar, rc, newExe)
	return nil
}

// replacePathEntry replaces every entry of a PATH-style list equal to oldDir with newDir, keeping
// the order. Entries compare without a trailing separator, and case-insensitively when fold is
// set (Windows). It reports whether anything changed.
func replacePathEntry(list, oldDir, newDir, sep string, fold bool) (string, bool) {
	trim := func(s string) string { return strings.TrimRight(s, `/\`) }
	same := func(a, b string) bool {
		if fold {
			return strings.EqualFold(trim(a), trim(b))
		}
		return trim(a) == trim(b)
	}
	entries := strings.Split(list, sep)
	changed := false
	for i, e := range entries {
		if e != "" && same(e, oldDir) {
			entries[i] = newDir
			changed = true
		}
	}
	return strings.Join(entries, sep), changed
}
