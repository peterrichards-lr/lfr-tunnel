package client

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// clientBinaryName is the client's basename, which is also the directory the installers put it
// in and the leaf of the EDR exclusion wildcard. Named once because those three have to agree:
// a rename that updated two of them would move the binary out of the exclusion silently.
const clientBinaryName = "lfr-tunnel"

// installDirEnvVars are the install-directory overrides, in precedence order. The
// platform-specific ones come first so a per-platform setting beats the generic one.
var installDirEnvVars = []string{
	"LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR",
	"LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR",
	"LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR",
	"LFR_TUNNEL_WINDOWS_AMD64_INSTALL_DIR",
	"LFR_TUNNEL_INSTALL_DIR",
	"LFT_INSTALL_DIR",
}

// canonicalExeName is the binary autostart is allowed to point at, under the install directory.
func canonicalExeName() string {
	if runtime.GOOS == "windows" {
		return clientBinaryName + ".exe"
	}
	return clientBinaryName
}

// canonicalInstallDir is where the installers put the client, and the only location the EDR
// exclusion covers.
func canonicalInstallDir(home string) string {
	return filepath.Join(home, "liferay", clientBinaryName)
}

// resolveInstallExe returns the binary path an autostart entry should record, or an error
// explaining where it looked.
//
// IT NO LONGER GUESSES, and that is the whole change (#2324). The previous version fell through
// to os.Executable() when neither an install-dir override nor the canonical location resolved --
// so running `install-service` from a working directory recorded that directory. A real shim
// from a reporting user:
//
//	WshShell.Run chr(34) & "C:\Users\...\runningpoc\bin\lfr-tunnel.exe" & chr(34) & " -background", 0
//
// `runningpoc\bin` is a checkout, not an install. When it was later removed, Windows threw a
// modal "cannot find the file specified" at every login -- and that is the HARMLESS end of it.
// The comment below has said since #1591 that the EDR exclusion is the wildcard
// */liferay/lfr-tunnel/lfr-tunnel, so until that directory vanished the machine was starting an
// UNEXCLUDED binary on every login. The function documented the hazard and then walked into it.
//
// The fall-through was never a safety net: an explicit install directory is honoured first, so
// it was only ever reached when the user had neither installed canonically nor said where they
// wanted it. There is no good guess to make there, and a refusal at install time is cheap where
// the consequence is silent and lasting.
//
// It also used to return defaultExe after os.Stat had already proved it absent, so the caller
// wrote a unit file pointing at nothing and printed [Success].
func resolveInstallExe() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve your home directory: %w", err)
	}

	var envDir, envName string
	for _, name := range installDirEnvVars {
		if v := os.Getenv(name); v != "" {
			envDir, envName = v, name
			break
		}
	}

	if envDir != "" {
		envDir = os.ExpandEnv(envDir)
		if len(envDir) >= 2 && (envDir[:2] == "~/" || envDir[:2] == "~\\") {
			envDir = filepath.Join(home, envDir[2:])
		}
		exe := filepath.Join(envDir, canonicalExeName())
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
		// An override that does not resolve is a mistake worth naming. Falling back to the
		// canonical path here would quietly ignore what the user asked for.
		return "", fmt.Errorf("%s is set to %q, but no %s exists there", envName, envDir, canonicalExeName())
	}

	// Matches the installers' default (#1591). The EDR exclusion is the wildcard
	// */liferay/lfr-tunnel/lfr-tunnel, so a service pointed anywhere else runs an unexcluded
	// binary even when the interactive client is fine.
	defaultExe := filepath.Join(canonicalInstallDir(home), canonicalExeName())
	if _, err := os.Stat(defaultExe); err == nil {
		return defaultExe, nil
	}

	return "", fmt.Errorf(
		"no installed client found at %s\n\n"+
			"Autostart records an absolute path, so it has to be one that will still be there "+
			"next time you log in -- and on an EDR-managed machine it has to be one the "+
			"exclusion covers, which is that directory and no other.\n\n"+
			"Either install the client there, or set %s to the directory holding it",
		defaultExe, installDirEnvVars[len(installDirEnvVars)-2])
}

// assertExecutableExists refuses to write an autostart entry for a binary that is not there.
//
// Belt to resolveInstallExe's braces: that function already stats the path it returns, so this
// only fires for a caller that resolved one some other way. A unit file pointing at a missing
// binary is never correct, and the check is one syscall against a failure mode whose symptom
// appears at the NEXT login, on someone else's machine (#2324).
func assertExecutableExists(exePath string) error {
	if _, err := os.Stat(exePath); err != nil {
		return fmt.Errorf("refusing to install autostart for %q, which does not exist: %w", exePath, err)
	}
	return nil
}

// UninstallService removes the CLI daemon service configuration.
func UninstallService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwin()
	case "linux":
		return uninstallLinux()
	case "windows":
		return uninstallWindows()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func uninstallDarwin() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.plist")
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return nil
	}

	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run() //nolint:errcheck

	if err := os.Remove(plistPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled macOS LaunchAgent: %s\n", plistPath)
	return nil
}

func uninstallLinux() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	servicePath := filepath.Join(homeDir, ".config", "systemd", "user", "lfr-tunnel.service")
	if _, err := os.Stat(servicePath); os.IsNotExist(err) {
		return nil
	}

	_ = exec.Command("systemctl", "--user", "stop", "lfr-tunnel.service").Run()    //nolint:errcheck
	_ = exec.Command("systemctl", "--user", "disable", "lfr-tunnel.service").Run() //nolint:errcheck

	if err := os.Remove(servicePath); err != nil {
		return err
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run() //nolint:errcheck
	fmt.Printf("[Success] Uninstalled Linux systemd user service: %s\n", servicePath)
	return nil
}

// uninstallWindows removes the CLI autostart shim.
//
// It was `return nil` -- advertised in the usage text, wired to a real branch, doing nothing and
// reporting success, beside a fully implemented uninstallWindowsGUI (#2324). That is why a shim
// pointing at a deleted directory survived on a user's machine: there was no way to clear it, so
// the only remedy was deleting the file by hand.
func uninstallWindows() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	vbsPath := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "lfr-tunnel.vbs")
	if _, err := os.Stat(vbsPath); os.IsNotExist(err) {
		return nil
	}

	if err := os.Remove(vbsPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled Windows startup script: %s\n", vbsPath)
	return nil
}

// InstallService configures lfr-tunnel to start on login automatically.
func InstallService() error {
	exePath, err := resolveInstallExe()
	if err != nil {
		return err
	}
	if err := assertExecutableExists(exePath); err != nil {
		return err
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

	prettyExe := filepath.Join(filepath.Dir(exePath), "Liferay Tunnel")
	if prettyExe != exePath {
		_ = os.Remove(prettyExe)
		if err := os.Symlink(exePath, prettyExe); err != nil {
			if input, err := os.ReadFile(exePath); err == nil {
				// 0755 required (#1408): this is a copy of the client binary under a friendly
				// name, and it has to be executable to be the thing the service launches.
				if werr := os.WriteFile(prettyExe, input, 0o755); werr != nil { //nolint:gosec
					// Cosmetic only: the friendly name is what the OS shows for the running
					// process. Without it the service still starts, under the real binary
					// name, so this is worth reporting rather than failing on.
					slog.Info(fmt.Sprintf("[Warning] Could not create %q: %v", prettyExe, werr))
				}
			}
		}
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.plist")
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.liferay.tunnel</string>
    <key>AssociatedBundleIdentifiers</key>
    <array>
        <string>com.liferay.tunnel</string>
    </array>
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
</plist>`, prettyExe, homeDir, homeDir)

	// 0750 (#1408): these live under the user's own home and every reader -- launchd,
	// systemd --user, Explorer -- runs as that same user. MkdirAll leaves an existing
	// directory's mode alone, so this only applies when creating one.
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o750); err != nil {
		return err
	}
	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(plistPath, []byte(plistContent), 0o644); err != nil { //nolint:gosec
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
	if err := os.MkdirAll(serviceDir, 0o750); err != nil {
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

	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0o644); err != nil { //nolint:gosec
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
	if err := os.MkdirAll(startupDir, 0o750); err != nil {
		return err
	}

	vbsPath := filepath.Join(startupDir, "lfr-tunnel.vbs")

	// Create a VBScript to run the executable silently without a cmd window
	vbsContent := fmt.Sprintf("Set WshShell = CreateObject(\"WScript.Shell\")\r\n"+
		"WshShell.Run chr(34) & \"%s\" & chr(34) & \" -background\", 0\r\n"+
		"Set WshShell = Nothing", exePath)

	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0o644); err != nil { //nolint:gosec
		return err
	}

	fmt.Printf("[Success] Installed Windows startup script to %s\n", vbsPath)
	fmt.Printf("[Success] lfr-tunnel will now start silently in the background on login.\n")
	return nil
}

// InstallGUIService configures the system tray GUI wrapper to start on login.
func InstallGUIService() error {
	exePath, err := resolveInstallExe()
	if err != nil {
		return err
	}
	if err := assertExecutableExists(exePath); err != nil {
		return err
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

	prettyExe := filepath.Join(filepath.Dir(exePath), "Liferay Tunnel")
	if prettyExe != exePath {
		_ = os.Remove(prettyExe)
		if err := os.Symlink(exePath, prettyExe); err != nil {
			if input, err := os.ReadFile(exePath); err == nil {
				// 0755 required (#1408): this is a copy of the client binary under a friendly
				// name, and it has to be executable to be the thing the service launches.
				if werr := os.WriteFile(prettyExe, input, 0o755); werr != nil { //nolint:gosec
					// Cosmetic only: the friendly name is what the OS shows for the running
					// process. Without it the service still starts, under the real binary
					// name, so this is worth reporting rather than failing on.
					slog.Info(fmt.Sprintf("[Warning] Could not create %q: %v", prettyExe, werr))
				}
			}
		}
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.liferay.tunnel.gui.plist")
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.liferay.tunnel.gui</string>
    <key>AssociatedBundleIdentifiers</key>
    <array>
        <string>com.liferay.tunnel</string>
    </array>
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
</plist>`, prettyExe, homeDir, homeDir)

	// 0750 (#1408): these live under the user's own home and every reader -- launchd,
	// systemd --user, Explorer -- runs as that same user. MkdirAll leaves an existing
	// directory's mode alone, so this only applies when creating one.
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o750); err != nil {
		return err
	}
	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(plistPath, []byte(plistContent), 0o644); err != nil { //nolint:gosec
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
	if err := os.MkdirAll(autostartDir, 0o750); err != nil {
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

	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(desktopPath, []byte(desktopContent), 0o644); err != nil { //nolint:gosec
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
	if err := os.MkdirAll(startupDir, 0o750); err != nil {
		return err
	}

	vbsPath := filepath.Join(startupDir, "lfr-tunnel-gui.vbs")
	vbsContent := fmt.Sprintf("Set WshShell = CreateObject(\"WScript.Shell\")\r\n"+
		"WshShell.Run chr(34) & \"%s\" & chr(34) & \" -gui\", 0\r\n"+
		"Set WshShell = Nothing", exePath)

	// 0644 left alone (#1408). Read by the platform service manager -- launchd,
	// systemd --user, Explorer -- which runs as this same user, so 0600 would very
	// likely work. "Very likely" is the problem: a unit file the manager cannot read
	// presents as autostart silently not happening, on three platforms that cannot be
	// tested from one machine. The content is a binary path, not a secret, so the
	// trade is a world-readable path against a silent breakage -- deliberately taking
	// the former until someone can verify all three.
	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0o644); err != nil { //nolint:gosec
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
