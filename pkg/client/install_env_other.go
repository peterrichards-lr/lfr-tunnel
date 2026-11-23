//go:build !windows

package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// repointInstallEnvPlatform re-points install.sh's LDM_LFR_TUNNEL_BIN line in each rc file it
// writes to. install.sh does not edit PATH, only advises, so a PATH naming the old directory is
// reported rather than changed.
func repointInstallEnvPlatform(home, oldExe, newExe string) error {
	var errs []error
	for _, rc := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		if err := repointRCLine(filepath.Join(home, rc), oldExe, newExe); err != nil {
			errs = append(errs, err)
		}
	}
	oldDir, newDir := filepath.Dir(oldExe), filepath.Dir(newExe)
	if _, onPath := replacePathEntry(os.Getenv("PATH"), oldDir, newDir, string(os.PathListSeparator), false); onPath {
		fmt.Printf("[Notice] Your PATH names %s, where lfr-tunnel used to be. Replace it with %s in your shell profile.\n",
			oldDir, newDir)
	}
	return errors.Join(errs...)
}
