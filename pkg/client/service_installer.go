package client

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallService configures lfr-tunnel to start on login automatically.
func InstallService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return installDarwin(exePath)
	case "linux":
		return installLinux(exePath)
	case "windows":
		return installWindows(exePath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func installDarwin(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.plist")
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.liferay.tunnel</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>-background</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/.lfr-tunnel/service.log</string>
    <key>StandardErrorPath</key>
    <string>%s/.lfr-tunnel/service.err</string>
</dict>
</plist>`, exePath, homeDir, homeDir)

	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return err
	}

	// Load the service
	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if err := cmd.Run(); err != nil {
		log.Printf("[Warning] Failed to load launchctl, you may need to run: launchctl load -w %s\n", plistPath)
	}

	fmt.Printf("[Success] Installed macOS LaunchAgent to %s\n", plistPath)
	fmt.Printf("[Success] lfr-tunnel will now start automatically in the background on login.\n")
	return nil
}

func installLinux(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	serviceDir := filepath.Join(homeDir, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return err
	}

	servicePath := filepath.Join(serviceDir, "lfr-tunnel.service")
	serviceContent := fmt.Sprintf(`[Unit]
Description=Liferay Tunnel Client
After=network.target

[Service]
ExecStart=%s -background
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, exePath)

	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return err
	}

	// Enable and start
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	_ = exec.Command("systemctl", "--user", "enable", "lfr-tunnel.service").Run()
	_ = exec.Command("systemctl", "--user", "start", "lfr-tunnel.service").Run()

	fmt.Printf("[Success] Installed Linux systemd user service to %s\n", servicePath)
	fmt.Printf("[Success] lfr-tunnel will now start automatically in the background on login.\n")
	return nil
}

func installWindows(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup
	startupDir := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startupDir, 0755); err != nil {
		return err
	}

	vbsPath := filepath.Join(startupDir, "lfr-tunnel.vbs")

	// Create a VBScript to run the executable silently without a cmd window
	vbsContent := fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
WshShell.Run chr(34) & "%s" & chr(34) & " -background", 0
Set WshShell = Nothing`, strings.ReplaceAll(exePath, "\\", "\\\\"))

	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0644); err != nil {
		return err
	}

	fmt.Printf("[Success] Installed Windows startup script to %s\n", vbsPath)
	fmt.Printf("[Success] lfr-tunnel will now start silently in the background on login.\n")
	return nil
}
