package client

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		slog.Info(fmt.Sprintf("[Warning] Failed to load launchctl, you may need to run: launchctl load -w %s\n", plistPath))
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
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()                //nolint:errcheck
	_ = exec.Command("systemctl", "--user", "enable", "lfr-tunnel.service").Run() //nolint:errcheck
	_ = exec.Command("systemctl", "--user", "start", "lfr-tunnel.service").Run()  //nolint:errcheck

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
	vbsContent := fmt.Sprintf("Set WshShell = CreateObject(\"WScript.Shell\")\r\n"+
		"WshShell.Run chr(34) & \"%s\" & chr(34) & \" -background\", 0\r\n"+
		"Set WshShell = Nothing", exePath)

	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0644); err != nil {
		return err
	}

	fmt.Printf("[Success] Installed Windows startup script to %s\n", vbsPath)
	fmt.Printf("[Success] lfr-tunnel will now start silently in the background on login.\n")
	return nil
}

// InstallGUIService configures lfr-tunnel -gui to start on login automatically.
func InstallGUIService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return installDarwinGUI(exePath)
	case "linux":
		return installLinuxGUI(exePath)
	case "windows":
		return installWindowsGUI(exePath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// IsGUIServiceInstalled checks if the GUI autostart configuration exists.
func IsGUIServiceInstalled() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.gui.plist")
		_, err := os.Stat(plistPath)
		return err == nil
	case "linux":
		desktopPath := filepath.Join(homeDir, ".config", "autostart", "lfr-tunnel-gui.desktop")
		_, err := os.Stat(desktopPath)
		return err == nil
	case "windows":
		vbsPath := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "lfr-tunnel-gui.vbs")
		_, err := os.Stat(vbsPath)
		return err == nil
	default:
		return false
	}
}

// UninstallGUIService removes the GUI autostart configuration.
func UninstallGUIService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwinGUI()
	case "linux":
		return uninstallLinuxGUI()
	case "windows":
		return uninstallWindowsGUI()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func installDarwinGUI(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.gui.plist")
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.liferay.tunnel.gui</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>-gui</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/.lfr-tunnel/gui_service.log</string>
    <key>StandardErrorPath</key>
    <string>%s/.lfr-tunnel/gui_service.err</string>
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
		slog.Info(fmt.Sprintf("[Warning] Failed to load launchctl, you may need to run: launchctl load -w %s\n", plistPath))
	}

	fmt.Printf("[Success] Installed macOS LaunchAgent to %s\n", plistPath)
	fmt.Printf("[Success] lfr-tunnel (GUI Wrapper) will now start automatically on login.\n")
	return nil
}

func uninstallDarwinGUI() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.gui.plist")
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return nil
	}

	// Unload the service
	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run() //nolint:errcheck

	if err := os.Remove(plistPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled macOS LaunchAgent: %s\n", plistPath)
	return nil
}

func installLinuxGUI(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	autostartDir := filepath.Join(homeDir, ".config", "autostart")
	if err := os.MkdirAll(autostartDir, 0755); err != nil {
		return err
	}

	desktopPath := filepath.Join(autostartDir, "lfr-tunnel-gui.desktop")
	desktopContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Liferay Tunnel GUI
Comment=Liferay Tunnel System Tray Menu
Exec=%s -gui
Terminal=false
Categories=Network;
`, exePath)

	if err := os.WriteFile(desktopPath, []byte(desktopContent), 0644); err != nil {
		return err
	}

	fmt.Printf("[Success] Installed Linux autostart desktop entry to %s\n", desktopPath)
	fmt.Printf("[Success] lfr-tunnel (GUI Wrapper) will now start automatically on login.\n")
	return nil
}

func uninstallLinuxGUI() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	desktopPath := filepath.Join(homeDir, ".config", "autostart", "lfr-tunnel-gui.desktop")
	if _, err := os.Stat(desktopPath); os.IsNotExist(err) {
		return nil
	}

	if err := os.Remove(desktopPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled Linux autostart desktop entry: %s\n", desktopPath)
	return nil
}

func installWindowsGUI(exePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	startupDir := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startupDir, 0755); err != nil {
		return err
	}

	vbsPath := filepath.Join(startupDir, "lfr-tunnel-gui.vbs")
	vbsContent := fmt.Sprintf("Set WshShell = CreateObject(\"WScript.Shell\")\r\n"+
		"WshShell.Run chr(34) & \"%s\" & chr(34) & \" -gui\", 0\r\n"+
		"Set WshShell = Nothing", exePath)

	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0644); err != nil {
		return err
	}

	fmt.Printf("[Success] Installed Windows startup script to %s\n", vbsPath)
	fmt.Printf("[Success] lfr-tunnel (GUI Wrapper) will now start automatically on login.\n")
	return nil
}

func uninstallWindowsGUI() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	vbsPath := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "lfr-tunnel-gui.vbs")
	if _, err := os.Stat(vbsPath); os.IsNotExist(err) {
		return nil
	}

	if err := os.Remove(vbsPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled Windows startup script: %s\n", vbsPath)
	return nil
}
