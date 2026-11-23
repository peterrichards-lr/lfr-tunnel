package client

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// The installers give LDM (liferay-docker-manager) a name for this client at ~/.ldm/bin, so LDM
// runs it instead of downloading an unexcluded copy of its own (#1311). It is a symlink on macOS
// and Linux and a HARDLINK on Windows (install.ps1), and docs/infosec.md §3 requests an EDR rule
// for it.
//
// An upgrade has to keep it pointing at the client (#2553):
//   - after a move to the gateway's install_dir, the symlink names a binary that was removed;
//   - on Windows, after ANY upgrade: the in-place upgrade renames the binary to .old and writes a
//     new file (#2505), and a hardlink is a second name for the old file's content, not for the
//     path, so LDM kept running the previous version.

// ldmLinkPath is where the installers put LDM's name for the client.
func ldmLinkPath(home string) string {
	return filepath.Join(home, ".ldm", "bin", canonicalExeName())
}

// repointLDMLink makes the LDM link at link name target, where it named from (the binary this
// upgrade replaced or moved away from). A link that does not exist is left absent: creating it is
// the installer's job, and a user who removed it chose to.
//
// On Unix a symlink is re-pointed only if it names from, or nothing at all (#2553 review): one
// pointing at another binary is the user's choice, and a regular file is not something the
// installers made. On Windows the installer's hardlink (or its copy fallback) cannot say what it
// was made from once the file it shared content with is replaced, so it is always re-created.
//
// hardlink selects the Windows form. If a hardlink cannot be made (another volume), the file is
// copied instead, as install.ps1 does.
func repointLDMLink(link, from, target string, hardlink bool) error {
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the LDM link %s: %w", link, err)
	}

	if hardlink {
		if targetInfo, err := os.Stat(target); err == nil && os.SameFile(info, targetInfo) {
			return nil
		}
	} else {
		if info.Mode()&fs.ModeSymlink == 0 {
			return nil
		}
		if current, err := os.Readlink(link); err == nil && current == target {
			return nil
		}
		if !linkNamesOrDangles(link, from) {
			return nil
		}
	}

	// A leftover from an interrupted run would make the link below fail; absent is the normal case.
	tmp := link + ".new"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clearing %s: %w", tmp, err)
	}
	if hardlink {
		if err := os.Link(target, tmp); err != nil {
			if cerr := copyFile(target, tmp); cerr != nil {
				return fmt.Errorf("re-creating the LDM link %s: %w (copy fallback: %v)", link, err, cerr)
			}
		}
		return replaceHardlink(link, tmp)
	}
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("re-pointing the LDM link %s: %w", link, err)
	}
	// A rename over a symlink is atomic, so LDM never finds the name missing.
	if err := os.Rename(tmp, link); err != nil {
		if rerr := os.Remove(tmp); rerr != nil {
			return fmt.Errorf("replacing the LDM link %s: %w (and %s is left behind: %v)", link, err, tmp, rerr)
		}
		return fmt.Errorf("replacing the LDM link %s: %w", link, err)
	}
	return nil
}

// linkNamesOrDangles reports whether the symlink at link resolves to from, or to nothing.
func linkNamesOrDangles(link, from string) bool {
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	if resolved == from {
		return true
	}
	a, errA := os.Stat(resolved)
	b, errB := os.Stat(from)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// replaceHardlink puts tmp in place of link on Windows. Windows refuses to replace or delete any
// name of a running image, and the hardlink usually IS one: it shares the file of the binary
// doing this upgrade (#2553 review). Renaming a running image is allowed, so the old name is moved
// aside first, the new one renamed into place, and the aside copy deleted if Windows lets it --
// otherwise it is cleared on the next upgrade. LDM can miss the name only between two renames.
func replaceHardlink(link, tmp string) error {
	aside := link + ".old"
	if err := os.Remove(aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clearing %s: %w", aside, err)
	}
	if err := os.Rename(link, aside); err != nil {
		return fmt.Errorf("moving the LDM link %s aside: %w", link, err)
	}
	if err := os.Rename(tmp, link); err != nil {
		if rerr := os.Rename(aside, link); rerr != nil {
			return fmt.Errorf("replacing the LDM link %s: %w; restoring it from %s also failed: %v", link, err, aside, rerr)
		}
		return fmt.Errorf("replacing the LDM link %s: %w", link, err)
	}
	if err := os.Remove(aside); err != nil {
		fmt.Printf("[Update] %s is still in use; it is removed on the next upgrade.\n", aside)
	}
	return nil
}

// repointLDMLinkForPlatform is repointLDMLink with this platform's link form. A variable so the
// upgrade's ordering can be tested without touching a home directory.
var repointLDMLinkForPlatform = func(home, from, target string) error {
	if home == "" {
		return errors.New("no home directory, so the LDM link could not be checked")
	}
	// The hardlink form is Windows', whose separator is the backslash.
	return repointLDMLink(ldmLinkPath(home), from, target, os.PathSeparator == '\\')
}

// copyFile copies src to dst with the executable bit, for the copy fallback above.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := in.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	// 0755 required (#1408): LDM executes this file.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // the client binary
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
