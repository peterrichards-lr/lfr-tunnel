package client

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// #2504: autostart runs the client under its own name on every OS. macOS used to run it as
// "Liferay Tunnel" -- a symlink or, when that failed, a full copy -- which no EDR exclusion names.
//
// These run on every platform: the installers are path arithmetic and file I/O against the
// package's isolated HOME, with the service managers stubbed so nothing reaches the real launchd
// or systemd.

// stubServiceManagers replaces launchctl and systemctl for the test.
func stubServiceManagers(t *testing.T) {
	t.Helper()
	oldL, oldS := launchctl, systemctlUser
	launchctl = func(...string) error { return nil }
	systemctlUser = func(...string) error { return nil }
	t.Cleanup(func() { launchctl, systemctlUser = oldL, oldS })
}

// canonicalInstall puts a client at the canonical path under the isolated home and removes it,
// and every autostart file, when the test ends.
func canonicalInstall(t *testing.T) (home, exe string) {
	t.Helper()
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := canonicalInstallDir(home)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(dir, canonicalExeName())
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // a stand-in for the client binary
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, p := range []string{
			dir,
			filepath.Join(home, "Library", "LaunchAgents"),
			filepath.Join(home, ".config"),
			filepath.Join(home, "AppData"),
		} {
			_ = os.RemoveAll(p)
		}
	})
	return home, exe
}

var programArgRe = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)

func plistProgram(t *testing.T, plist string) string {
	t.Helper()
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("reading %s: %v", plist, err)
	}
	m := programArgRe.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("%s has no ProgramArguments:\n%s", plist, data)
	}
	return m[1]
}

func installDirEntries(t *testing.T, exe string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// autostartDirs are every directory an installer writes to, besides the install directory.
func autostartDirs(home string) []string {
	return []string{
		filepath.Join(home, "Library", "LaunchAgents"),
		filepath.Join(home, ".config", "systemd", "user"),
		filepath.Join(home, ".config", "autostart"),
		filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup"),
	}
}

// Every installer, on every OS: the install directory holds the client and nothing else it could
// run, and nothing an installer writes beside its autostart entry is executable or a link.
func TestNoAutostartInstallerWritesAnotherExecutable(t *testing.T) {
	stubServiceManagers(t)
	for _, tc := range []struct {
		name    string
		install func(string) error
	}{
		{"macOS service", installDarwin},
		{"macOS tray", installDarwinGUI},
		{"Linux service", installLinux},
		{"Linux tray", installLinuxGUI},
		{"Windows service", installWindows},
		{"Windows tray", installWindowsGUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, exe := canonicalInstall(t)
			if err := tc.install(exe); err != nil {
				t.Fatalf("install: %v", err)
			}
			if got := installDirEntries(t, exe); len(got) != 1 || got[0] != canonicalExeName() {
				t.Errorf("install directory holds %q, want only %q", got, canonicalExeName())
			}
			if runtime.GOOS == "windows" {
				return // no executable bit to inspect
			}
			for _, dir := range autostartDirs(home) {
				entries, err := os.ReadDir(dir)
				if err != nil {
					continue // this installer does not write there
				}
				for _, e := range entries {
					info, err := os.Lstat(filepath.Join(dir, e.Name()))
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode()&0o111 != 0 || info.Mode()&os.ModeSymlink != 0 {
						t.Errorf("the installer left an executable or link at %s (mode %v)", filepath.Join(dir, e.Name()), info.Mode())
					}
				}
			}
		})
	}
}

func TestMacOSAgentsRunTheClientItself(t *testing.T) {
	stubServiceManagers(t)
	home, exe := canonicalInstall(t)
	if err := installDarwin(exe); err != nil {
		t.Fatal(err)
	}
	if err := installDarwinGUI(exe); err != nil {
		t.Fatal(err)
	}
	for _, plist := range darwinPlistPaths(home) {
		if got := plistProgram(t, plist); got != exe {
			t.Errorf("%s runs %q, want the client at %q", filepath.Base(plist), got, exe)
		}
	}
}

// writeLegacyInstall reproduces what earlier versions left: the "Liferay Tunnel" file (a copy or a
// symlink) and both plists running it.
func writeLegacyInstall(t *testing.T, home, exe string, symlink bool) string {
	t.Helper()
	legacy := filepath.Join(filepath.Dir(exe), legacyDarwinExeName)
	if symlink {
		if err := os.Symlink(exe, legacy); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}
	} else if err := os.WriteFile(legacy, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // the copy earlier versions made
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, plist := range darwinPlistPaths(home) {
		body := "<plist><dict><key>ProgramArguments</key>\n<array>\n<string>" + legacy + "</string>\n<string>-background</string>\n</array></dict></plist>"
		if err := os.WriteFile(plist, []byte(body), 0o644); err != nil { //nolint:gosec // as the installer writes it
			t.Fatal(err)
		}
	}
	return legacy
}

func TestALegacyMacOSInstallConverges(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		kind := map[bool]string{false: "copy", true: "symlink"}[symlink]
		t.Run("install, "+kind, func(t *testing.T) {
			stubServiceManagers(t)
			home, exe := canonicalInstall(t)
			legacy := writeLegacyInstall(t, home, exe, symlink)
			// Installing ONE agent must repoint the other too, or removing the file breaks it.
			if err := installDarwinGUI(exe); err != nil {
				t.Fatal(err)
			}
			for _, plist := range darwinPlistPaths(home) {
				if got := plistProgram(t, plist); got != exe {
					t.Errorf("%s still runs %q", filepath.Base(plist), got)
				}
			}
			if _, err := os.Lstat(legacy); err == nil {
				t.Errorf("the legacy %s %q is still there", kind, legacy)
			}
		})
	}

	for _, symlink := range []bool{false, true} {
		kind := map[bool]string{false: "copy", true: "symlink"}[symlink]
		t.Run("upgrade, "+kind, func(t *testing.T) {
			home, exe := canonicalInstall(t)
			legacy := writeLegacyInstall(t, home, exe, symlink)
			convergeDarwinAutostart(home, exe) // what SelfUpgrade calls once the binary is replaced
			if _, err := os.Lstat(legacy); err == nil {
				t.Errorf("the legacy %s survived the upgrade -- a copy would run the OLD binary at the next login", kind)
			}
			for _, plist := range darwinPlistPaths(home) {
				if got := plistProgram(t, plist); got != exe {
					t.Errorf("%s still runs %q after the upgrade", filepath.Base(plist), got)
				}
			}
		})

		t.Run("uninstalling the tray repoints the service before removing the "+kind, func(t *testing.T) {
			stubServiceManagers(t)
			home, exe := canonicalInstall(t)
			legacy := writeLegacyInstall(t, home, exe, symlink)
			if err := uninstallDarwinGUI(); err != nil {
				t.Fatal(err)
			}
			service := darwinPlistPaths(home)[0]
			if got := plistProgram(t, service); got != exe {
				t.Errorf("the service agent runs %q, a file the uninstall removed", got)
			}
			if _, err := os.Lstat(legacy); err == nil {
				t.Errorf("the legacy %s survived", kind)
			}
		})
	}
}

func TestMacOSUninstallRemovesBothAgentsAndTheLegacyFile(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		kind := map[bool]string{false: "copy", true: "symlink"}[symlink]
		t.Run(kind, func(t *testing.T) {
			stubServiceManagers(t)
			home, exe := canonicalInstall(t)
			legacy := writeLegacyInstall(t, home, exe, symlink)
			if err := uninstallDarwin(); err != nil {
				t.Fatal(err)
			}
			for _, plist := range darwinPlistPaths(home) {
				if _, err := os.Stat(plist); err == nil {
					t.Errorf("%s survived the uninstall", filepath.Base(plist))
				}
			}
			if _, err := os.Lstat(legacy); err == nil {
				t.Errorf("the legacy %s survived the uninstall", kind)
			}
			if _, err := os.Stat(exe); err != nil {
				t.Errorf("the uninstall removed the client itself: %v", err)
			}
		})
	}
}
