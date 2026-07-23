package client

import (
	"os"
	"path/filepath"

	"testing"
)

func TestInstallService(t *testing.T) {
	defer UninstallService()   //nolint:errcheck
	defer UninstallGUIService() //nolint:errcheck

	binPath := filepath.Join(t.TempDir(), "lfr-tunnel")
	f, _ := os.Create(binPath)
	_ = f.Close() //nolint:errcheck

	_ = InstallService() //nolint:errcheck

	_ = installDarwin(binPath)  //nolint:errcheck
	_ = installLinux(binPath)   //nolint:errcheck
	_ = installWindows(binPath) //nolint:errcheck

	_ = UninstallService()   //nolint:errcheck
	_ = UninstallGUIService() //nolint:errcheck
}
