package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The installers used to give LDM (liferay-docker-manager) a name for this client at ~/.ldm/bin,
// so LDM ran it instead of downloading an unexcluded copy of its own (#1311): a symlink on macOS
// and Linux, a HARDLINK (or a copy) on Windows. #2553 kept it pointing at the client across
// upgrades.
//
// It is retired (#2579). LDM 2.25.0 and later find the client through LDM_LFR_TUNNEL_BIN, which the
// installers still set and the upgrade keeps current, or through the install path itself, both
// ahead of ~/.ldm/bin; and LDM no longer downloads a client at all. The link only cost a second
// EDR rule per platform. On Windows it was worse than useless once stale: an executable outside
// the excluded path. So an upgrade now removes the link the installers made, and nothing creates it.

// ldmLinkPath is where the installers put LDM's name for the client.
func ldmLinkPath(home string) string {
	return filepath.Join(home, ".ldm", "bin", canonicalExeName())
}

// retireLDMLink removes the installer-made LDM link at link. from is the binary this upgrade
// replaced or moved away from, target the one it installed. A link that does not exist is fine.
//
// Ownership is judged as #2553 judged it when re-pointing. On Unix only a symlink that names from,
// target, or nothing at all is removed: one pointing at another binary is the user's choice, and a
// regular file is not something the installers made. On Windows the name was always the
// installer's (a hardlink, or the copy it fell back to), and a stale one is an executable outside
// the EDR exclusion, so it is removed whatever it holds.
func retireLDMLink(link, from, target string, windows bool) error {
	// A leftover from an earlier run that could not delete a running image (see below).
	if windows {
		if err := os.Remove(link + ".old"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Printf("[Update] %s.old is still in use; it is removed on the next upgrade.\n", link)
		}
	}

	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking the retired LDM link %s: %w", link, err)
	}

	if !windows {
		if info.Mode()&fs.ModeSymlink == 0 {
			return nil
		}
		if !linkNamesOrDangles(link, from) && !linkNamesOrDangles(link, target) {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("removing the retired LDM link %s: %w", link, err)
		}
		fmt.Printf("[Update] Removed the retired LDM link %s; LDM finds the client through LDM_LFR_TUNNEL_BIN.\n", link)
		return nil
	}

	// Windows refuses to delete any name of a running image, and the hardlink usually IS one: it
	// shares the file of the binary doing this upgrade. Renaming a running image is allowed, so the
	// name is moved aside, and the aside copy deleted now if Windows lets it, otherwise on the next
	// upgrade (above).
	if err := os.Remove(link); err != nil {
		aside := link + ".old"
		if rerr := os.Rename(link, aside); rerr != nil {
			return fmt.Errorf("removing the retired LDM link %s: %w (moving it aside: %v)", link, err, rerr)
		}
		if derr := os.Remove(aside); derr != nil {
			fmt.Printf("[Update] %s is still in use; it is removed on the next upgrade.\n", aside)
		}
	}
	fmt.Printf("[Update] Removed the retired LDM link %s; LDM finds the client through LDM_LFR_TUNNEL_BIN.\n", link)
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

// retireLDMLinkForPlatform is retireLDMLink with this platform's ownership rule. A variable so the
// upgrade's ordering can be tested without touching a home directory.
var retireLDMLinkForPlatform = func(home, from, target string) error {
	if home == "" {
		return errors.New("no home directory, so the retired LDM link could not be checked")
	}
	// The Windows rule is keyed on the separator, as the hardlink form was.
	return retireLDMLink(ldmLinkPath(home), from, target, os.PathSeparator == '\\')
}
