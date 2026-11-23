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

// repointLDMLink makes the LDM link at link name target. A link that does not exist is left
// absent: creating it is the installer's job, and a user who removed it chose to. On Unix, a
// regular file there is not one the installers made, so it is left alone too.
//
// hardlink selects the Windows form. If a hardlink cannot be made (another volume), the file is
// copied instead, as install.ps1 does.
//
// The new link is made beside the old one and renamed over it, so LDM never finds the name
// missing.
func repointLDMLink(link, target string, hardlink bool) error {
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the LDM link %s: %w", link, err)
	}

	// A leftover from an interrupted run would make the link below fail; absent is the normal case.
	tmp := link + ".new"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clearing %s: %w", tmp, err)
	}

	if hardlink {
		if targetInfo, err := os.Stat(target); err == nil && os.SameFile(info, targetInfo) {
			return nil
		}
		if err := os.Link(target, tmp); err != nil {
			if cerr := copyFile(target, tmp); cerr != nil {
				return fmt.Errorf("re-creating the LDM link %s: %w (copy fallback: %v)", link, err, cerr)
			}
		}
	} else {
		if info.Mode()&fs.ModeSymlink == 0 {
			return nil
		}
		if current, err := os.Readlink(link); err == nil && current == target {
			return nil
		}
		if err := os.Symlink(target, tmp); err != nil {
			return fmt.Errorf("re-pointing the LDM link %s: %w", link, err)
		}
	}
	if err := os.Rename(tmp, link); err != nil {
		if rerr := os.Remove(tmp); rerr != nil {
			return fmt.Errorf("replacing the LDM link %s: %w (and %s is left behind: %v)", link, err, tmp, rerr)
		}
		return fmt.Errorf("replacing the LDM link %s: %w", link, err)
	}
	return nil
}

// repointLDMLinkForPlatform is repointLDMLink with this platform's link form. A variable so the
// upgrade's ordering can be tested without touching a home directory.
var repointLDMLinkForPlatform = func(home, target string) error {
	if home == "" {
		return errors.New("no home directory, so the LDM link could not be checked")
	}
	// The hardlink form is Windows', whose separator is the backslash.
	return repointLDMLink(ldmLinkPath(home), target, os.PathSeparator == '\\')
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
