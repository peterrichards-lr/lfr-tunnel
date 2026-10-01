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

// Autostart records an absolute path, so the resolver must not guess one (#2324).
//
// The reported failure: `install-service` run from a working directory recorded that directory,
// via an os.Executable() fall-through. When the directory was later removed, Windows threw a
// modal "cannot find the file specified" at every login -- and that was the harmless end of it.
// The EDR exclusion is the wildcard */liferay/lfr-tunnel/lfr-tunnel, so until then the machine
// had been starting an UNEXCLUDED binary on every login.
//
// HOME is isolated for this package (testmain_home_test.go), so these build a canonical install
// under the temporary home rather than touching the real one.
func TestResolveInstallExeRefusesToGuess(t *testing.T) {
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving the isolated home: %v", err)
	}

	t.Run("no canonical binary and no override is a refusal", func(t *testing.T) {
		got, err := resolveInstallExe()
		if err == nil {
			t.Fatalf("resolved %q with nothing installed -- that is the guess this exists to stop", got)
		}
		// The message has to be actionable: where it looked, and what to set instead. An
		// unactionable refusal is a worse experience than the bug.
		if !strings.Contains(err.Error(), filepath.Join(home, "liferay")) {
			t.Errorf("the refusal does not name the canonical path: %v", err)
		}
		if !strings.Contains(err.Error(), "LFR_TUNNEL_INSTALL_DIR") {
			t.Errorf("the refusal does not name the override to set: %v", err)
		}
	})

	t.Run("the canonical install resolves", func(t *testing.T) {
		dir := canonicalInstallDir(home)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		exe := filepath.Join(dir, canonicalExeName())
		if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		defer func() { _ = os.Remove(exe) }()

		got, err := resolveInstallExe()
		if err != nil {
			t.Fatalf("a canonical install did not resolve: %v", err)
		}
		if got != exe {
			t.Errorf("resolved %q, want %q", got, exe)
		}
	})

	// CONTROL for the case above: with the binary gone again, the same call must refuse. Without
	// this, "the canonical install resolves" would also pass against a resolver that returned
	// that path unconditionally -- which is precisely the old behaviour at line 62.
	t.Run("and stops resolving when the binary goes", func(t *testing.T) {
		if got, err := resolveInstallExe(); err == nil {
			t.Errorf("resolved %q after the binary was removed", got)
		}
	})
}

// An override that does not resolve is named, not silently replaced by the canonical path.
//
// Falling back would ignore what the user asked for and install autostart somewhere they did not
// choose -- the same class of silent substitution as the guess this change removes.
func TestResolveInstallExeRefusesAnOverrideThatDoesNotResolve(t *testing.T) {
	for _, name := range installDirEnvVars {
		t.Setenv(name, "")
	}
	home, _ := os.UserHomeDir()

	// A canonical install EXISTS, so a fallback would succeed and hide the mistake.
	dir := canonicalInstallDir(home)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, canonicalExeName()), []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	t.Setenv("LFR_TUNNEL_INSTALL_DIR", filepath.Join(home, "nowhere"))
	got, err := resolveInstallExe()
	if err == nil {
		t.Fatalf("an unresolvable override silently resolved to %q", got)
	}
	if !strings.Contains(err.Error(), "LFR_TUNNEL_INSTALL_DIR") {
		t.Errorf("the refusal does not name the variable that is wrong: %v", err)
	}
}

// No installer writes a unit file for a binary that is not there.
func TestInstallRefusesAMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if err := assertExecutableExists(missing); err == nil {
		t.Error("a missing binary was accepted -- the symptom of that appears at the next login")
	}

	present := filepath.Join(t.TempDir(), "there")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := assertExecutableExists(present); err != nil {
		t.Errorf("a binary that exists was refused: %v", err)
	}
}

// uninstallWindows actually removes the shim (#2324).
//
// It was `return nil`: advertised in the usage text, wired to a real branch, doing nothing and
// reporting success beside a fully implemented GUI sibling. That is why a shim pointing at a
// deleted directory survived on a user's machine -- there was no way to clear it.
//
// Asserts the FILE IS GONE, not that the call returned nil. The no-op returned nil, so a test
// reading only the error would have passed against it for the whole time it was broken: section
// 5c, an assertion satisfied by something other than the thing it names.
//
// Runs on every platform. It is pure path arithmetic and file I/O against an isolated HOME, so
// the behaviour under test does not need the OS it ships to -- and waiting for a Windows runner
// is how it went unnoticed.
func TestUninstallWindowsRemovesTheShim(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving the isolated home: %v", err)
	}
	startup := filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startup, 0o750); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	vbs := filepath.Join(startup, "lfr-tunnel.vbs")
	if err := os.WriteFile(vbs, []byte("Set WshShell = Nothing\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if err := uninstallWindows(); err != nil {
		t.Fatalf("uninstallWindows: %v", err)
	}
	if _, err := os.Stat(vbs); !os.IsNotExist(err) {
		t.Errorf("the shim is still at %s -- uninstall-service reports success and leaves it", vbs)
	}

	// BOUNDING: removing one that is not there is not an error. `uninstall-service` on a machine
	// that never installed must be a no-op, not a failure.
	if err := uninstallWindows(); err != nil {
		t.Errorf("uninstalling an absent shim errored: %v", err)
	}
}
