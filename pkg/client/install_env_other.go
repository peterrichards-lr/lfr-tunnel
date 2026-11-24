//go:build !windows

package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// repointInstallEnvPlatform re-points install.sh's LDM_LFR_TUNNEL_BIN and PATH lines (#2584) in
// each rc file it writes to. This process's own PATH, and any PATH line the user wrote themselves,
// cannot be changed from here, so a PATH still naming the old directory is reported.
func repointInstallEnvPlatform(home, oldExe, newExe string) error {
	var errs []error
	for _, rc := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		if err := repointRCLine(filepath.Join(home, rc), oldExe, newExe); err != nil {
			errs = append(errs, err)
		}
	}
	oldDir, newDir := filepath.Dir(oldExe), filepath.Dir(newExe)
	if _, onPath := replacePathEntry(os.Getenv("PATH"), oldDir, newDir, string(os.PathListSeparator), false); onPath {
		fmt.Printf("[Notice] This terminal's PATH names %s, where lfr-tunnel used to be. A line the installer wrote has been "+
			"updated and new terminals use %s; if you added %s yourself, replace it in your shell profile.\n",
			oldDir, newDir, oldDir)
	}
	return errors.Join(errs...)
}
