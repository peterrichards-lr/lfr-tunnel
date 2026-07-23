package client

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallService(t *testing.T) {
	defer UninstallService()    //nolint:errcheck
	defer UninstallGUIService() //nolint:errcheck

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "lfr-tunnel")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatalf("Failed to create dummy binary: %v", err)
	}

	if runtime.GOOS == "darwin" {
		if err := installDarwin(binPath); err != nil {
			t.Errorf("installDarwin failed: %v", err)
		}

		prettyExe := filepath.Join(tmpDir, "Liferay Tunnel")
		if _, err := os.Stat(prettyExe); os.IsNotExist(err) {
			t.Errorf("Expected pretty executable link at %s, but it was not created", prettyExe)
		}

		homeDir, _ := os.UserHomeDir()
		plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.plist")
		content, err := os.ReadFile(plistPath)
		if err != nil {
			t.Errorf("Failed to read created plist: %v", err)
		} else if !strings.Contains(string(content), "Liferay Tunnel") {
			t.Errorf("Expected plist to reference 'Liferay Tunnel', got:\n%s", string(content))
		}
	}

	if runtime.GOOS == "linux" {
		if err := installLinux(binPath); err != nil {
			t.Errorf("installLinux failed: %v", err)
		}
	}

	if runtime.GOOS == "windows" {
		if err := installWindows(binPath); err != nil {
			t.Errorf("installWindows failed: %v", err)
		}
	}

	if err := UninstallService(); err != nil {
		t.Logf("UninstallService returned: %v", err)
	}
	if err := UninstallGUIService(); err != nil {
		t.Logf("UninstallGUIService returned: %v", err)
	}
}
