package client

import (
	"os"
	"path/filepath"

	"testing"
)

func TestInstallService(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "lfr-tunnel")
	f, _ := os.Create(binPath)
	_ = f.Close() //nolint:errcheck

	_ = InstallService()

	_ = installDarwin(binPath)
	_ = installLinux(binPath)
	_ = installWindows(binPath)
}
