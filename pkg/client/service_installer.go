package client

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// legacyDarwinExeName is the second name macOS autostart used to run the client under (#799), so
// that Login Items showed a friendly name. It was a symlink to the client or, when that failed, a
// full copy of it -- either way an executable no EDR exclusion names, run at every login (#2504).
// Autostart now runs canonicalExeName() itself; this name survives only so convergeDarwinAutostart
// can find and remove what earlier versions left behind.
const legacyDarwinExeName = "Liferay Tunnel"

// launchctl and systemctlUser run the service managers. Variables so tests do not load agents
// into the real launchd or enable units in the real systemd user instance.
var launchctl = func(args ...string) error {
	return exec.Command("launchctl", args...).Run()
}

var systemctlUser = func(args ...string) error {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
}

func darwinPlistPaths(home string) []string {
	dir := filepath.Join(home, "Library", "LaunchAgents")
	return []string{filepath.Join(dir, daemonPlistName), filepath.Join(dir, guiPlistName)}
}

// convergeDarwinAutostart brings an install made by an earlier version into line (#2504): every
// LaunchAgent still running legacyDarwinExeName is repointed at exePath, and only then is the
// legacy file removed -- removing it first would leave an agent that fails at the next login. A
// plist that cannot be rewritten keeps the legacy file in place for the same reason. The plist is
// rewritten, not reloaded: a running agent keeps running, and the next load runs exePath.
func convergeDarwinAutostart(home, exePath string) {
	legacy := filepath.Join(filepath.Dir(exePath), legacyDarwinExeName)
	if legacy == exePath {
		return
	}
	oldArg := "<string>" + legacy + "</string>"
	for _, plist := range darwinPlistPaths(home) {
		data, err := os.ReadFile(plist)
		if err != nil || !strings.Contains(string(data), oldArg) {
			continue
		}
		updated := strings.ReplaceAll(string(data), oldArg, "<string>"+exePath+"</string>")
		if err := os.WriteFile(plist, []byte(updated), 0o644); err != nil { //nolint:gosec // see installDarwin
			slog.Warn(fmt.Sprintf("[Warning] Could not repoint %s at %s, so %q is kept: %v", plist, exePath, legacy, err))
			return
		}
		fmt.Printf("[Success] %s now runs %s\n", plist, exePath)
	}
	if _, err := os.Lstat(legacy); err != nil {
		return
	}
	if err := os.Remove(legacy); err != nil {
		slog.Warn(fmt.Sprintf("[Warning] Could not remove %q, left by an earlier version: %v", legacy, err))
		return
	}
	fmt.Printf("[Success] Removed %q, left by an earlier version; autostart runs %s\n", legacy, exePath)
}

// installedExeOrCanonical is the binary an uninstall should converge on: the resolved install
// when there is one, otherwise the canonical path.
func installedExeOrCanonical(home string) string {
	if exe, err := resolveInstallExe(""); err == nil {
		return exe
	}
	return filepath.Join(canonicalInstallDir(home), canonicalExeName())
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
//
// serverDir is the gateway's install_dir for this platform (#2552), or "" when the gateway has no
// opinion or could not be asked. Precedence: an override variable, then serverDir, then the
// built-in default. The gateway's value outranks the default because it is how a path change
// reaches clients without a release; the installers and --upgrade already follow it, and autostart
// was the one place that did not.
func resolveInstallExe(serverDir string) (string, error) {
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
		envDir = expandInstallDir(envDir, home)
		exe := filepath.Join(envDir, canonicalExeName())
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
		// An override that does not resolve is a mistake worth naming. Falling back to the
		// canonical path here would quietly ignore what the user asked for.
		return "", fmt.Errorf("%s is set to %q, but no %s exists there", envName, envDir, canonicalExeName())
	}

	if serverDir != "" {
		dir := expandInstallDir(serverDir, home)
		// A relative value would resolve against whatever directory this happens to run from,
		// and be recorded in an autostart entry as that. One YAML edit reaches every client, so
		// refuse it here rather than write a path that changes meaning at the next login.
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("the server sets the install directory for this platform to \"%s\", "+
				"which is not an absolute path or a ~ path; ask the server's operator to fix install_dir", serverDir)
		}
		exe := filepath.Join(dir, canonicalExeName())
		if _, err := os.Stat(exe); err == nil {
			return exe, nil
		}
		// Same reasoning as the override: the gateway named a directory, so a binary found at
		// the built-in default is in the wrong place, and on an EDR-managed machine possibly
		// outside the exclusion. Name it rather than record it.
		return "", fmt.Errorf(
			"the server sets the install directory for this platform to \"%s\", but no %s exists there\n\n"+
				"Reinstall with the installer from the server, or run `lfr-tunnel --upgrade` to move the client there",
			dir, canonicalExeName())
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

// expandInstallDir expands environment variables and a leading "~/" or "~\" the way the
// installers do, so a value from an override variable or the gateway lands in the home folder.
func expandInstallDir(dir, home string) string {
	dir = os.ExpandEnv(dir)
	if dir == "~" {
		return home
	}
	if len(dir) >= 2 && (dir[:2] == "~/" || dir[:2] == "~\\") {
		dir = filepath.Join(home, dir[2:])
	}
	return dir
}

// autostartServerDir asks the gateway for this platform's install_dir before an autostart entry
// is written (#2552).
//
// When the gateway cannot be asked, its setting is UNKNOWN, not absent. Refusing would make
// Launch on Login fail whenever the machine is offline, so this proceeds on the built-in default,
// and it says so on warn rather than letting an unknown answer pass for "the server has no
// opinion".
func autostartServerDir(serverURL string, warn io.Writer) string {
	dir, err := ServerInstallDir(serverURL)
	if err != nil {
		fmt.Fprintf(warn, "[Warning] Could not read the install directory from %s (%v).\n"+
			"[Warning] The server's setting is unknown, so autostart uses the local default.\n", serverURL, err)
		return ""
	}
	return dir
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

// uninstallDarwin removes both LaunchAgents, the service and the tray, and any legacy
// "Liferay Tunnel" file (#2504). It used to remove only the service plist, so an uninstall left
// the tray starting at every login and the extra executable beside the client.
func uninstallDarwin() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	for _, plistPath := range darwinPlistPaths(homeDir) {
		if _, err := os.Stat(plistPath); os.IsNotExist(err) {
			continue
		}
		_ = launchctl("unload", plistPath) //nolint:errcheck // not loaded is fine; the file still goes
		if err := os.Remove(plistPath); err != nil {
			return err
		}
		fmt.Printf("[Success] Uninstalled macOS LaunchAgent: %s\n", plistPath)
	}
	convergeDarwinAutostart(homeDir, installedExeOrCanonical(homeDir))
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

	_ = systemctlUser("stop", "lfr-tunnel.service")    //nolint:errcheck
	_ = systemctlUser("disable", "lfr-tunnel.service") //nolint:errcheck

	if err := os.Remove(servicePath); err != nil {
		return err
	}

	_ = systemctlUser("daemon-reload") //nolint:errcheck
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

// InstallService configures lfr-tunnel to start on login automatically. serverURL is the
// configured gateway, asked for its install_dir; "" skips the question.
func InstallService(serverURL string) error {
	exePath, err := resolveInstallExe(autostartServerDir(serverURL, os.Stdout))
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

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", daemonPlistName)
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
</plist>`, exePath, homeDir, homeDir)

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

	// The agent runs the client under its own name, as on every other OS (#2504): the EDR
	// exclusions are agreed per executable name. Login Items therefore shows lfr-tunnel; a custom
	// label needs an app bundle tied to a Developer ID team. Anything an earlier version left is
	// converged before the load, so neither agent can be left pointing at a removed file.
	convergeDarwinAutostart(homeDir, exePath)

	// Load the service
	if err := launchctl("load", "-w", plistPath); err != nil {
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
	_ = systemctlUser("daemon-reload")                //nolint:errcheck
	_ = systemctlUser("enable", "lfr-tunnel.service") //nolint:errcheck
	_ = systemctlUser("start", "lfr-tunnel.service")  //nolint:errcheck

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

// InstallGUIService configures the system tray GUI wrapper to start on login. serverURL is the
// configured gateway, asked for its install_dir; "" skips the question.
func InstallGUIService(serverURL string) error {
	exePath, err := resolveInstallExe(autostartServerDir(serverURL, os.Stdout))
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
</plist>`, exePath, homeDir, homeDir)

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

	// As installDarwin: the client's own name, and earlier installs converged first (#2504).
	convergeDarwinAutostart(homeDir, exePath)

	// Load the service
	if err := launchctl("load", "-w", plistPath); err != nil {
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
	_ = launchctl("unload", plistPath) //nolint:errcheck

	if err := os.Remove(plistPath); err != nil {
		return err
	}

	fmt.Printf("[Success] Uninstalled macOS LaunchAgent: %s\n", plistPath)
	// The service agent may still run the legacy name; repoint it before the file goes (#2504).
	convergeDarwinAutostart(homeDir, installedExeOrCanonical(homeDir))
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
