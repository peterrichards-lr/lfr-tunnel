package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// settingsURL decides where "Settings..." points, and whether the item is offered at all
// (#2055). It is the whole of the decision the menu makes, so it is worth testing even though
// the menu itself cannot be driven here.
//
// WHAT THIS DOES NOT COVER, deliberately stated: whether systray actually greys a disabled item,
// and whether openBrowser opens anything. Those are the tray's own behaviour and need a running
// GUI, which the local EDR rules do not allow. What IS covered is every branch that chooses the
// URL -- which is where the shipped defect lived: the menu hardcoded 127.0.0.1:55556, a port
// that is closed in the ordinary connected case.

// liveTunnelFixture writes the pid/state pair the GUI reads, using THIS process's pid so
// client.IsPIDRunning agrees the tunnel is up. Without a live pid, checkSubdomainRunning
// returns false and the "running" branch is unreachable -- the fixture would describe a state
// production cannot produce, and the test would pass for the wrong reason.
func liveTunnelFixture(t *testing.T, sub string, inspectorPort int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	if inspectorPort == 0 {
		return // no tunnel: leave the directory empty
	}
	pid := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("lfr-tunnel-%s.pid", sub)),
		[]byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		t.Fatalf("writing the pid file: %v", err)
	}
	state := fmt.Sprintf(`{"pid":%d,"inspector_port":%d,"inspector_url":"http://127.0.0.1:%d","subdomain":%q}`,
		pid, inspectorPort, inspectorPort, sub)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("lfr-tunnel-%s.state", sub)),
		[]byte(state), 0o600); err != nil {
		t.Fatalf("writing the state file: %v", err)
	}
}

// TestSettingsURLPrefersTheInspectorWhileATunnelIsUp is the shipped defect, inverted.
//
// tempServer is stopped as soon as a tunnel connects, so in this state the old code's literal
// 55556 pointed at nothing while the Inspector served /settings perfectly well.
func TestSettingsURLPrefersTheInspectorWhileATunnelIsUp(t *testing.T) {
	liveTunnelFixture(t, "demo", 4310)

	cfg := &config.ClientConfig{Subdomain: "demo"}
	got := settingsURL(cfg)

	want := "http://127.0.0.1:4310/settings"
	if got != want {
		t.Errorf("settingsURL = %q, want %q -- with a tunnel up the Inspector serves the "+
			"settings page and tempServer is stopped", got, want)
	}
	if strings.Contains(got, "55556") {
		t.Errorf("settingsURL still points at the old hardcoded port: %q", got)
	}
}

// TestSettingsURLFallsBackToTheSettingsServerWhenNoTunnelIsUp covers the other live branch, and
// asserts it uses the port actually bound rather than the one requested.
func TestSettingsURLFallsBackToTheSettingsServerWhenNoTunnelIsUp(t *testing.T) {
	liveTunnelFixture(t, "demo", 0) // no tunnel

	if err := tempServer.Start(); err != nil {
		t.Fatalf("starting the settings server: %v", err)
	}
	defer tempServer.Stop()

	port := tempServer.Port()
	if port == 0 {
		t.Fatal("the settings server reports no port after a successful Start")
	}

	cfg := &config.ClientConfig{Subdomain: "demo"}
	want := fmt.Sprintf("http://127.0.0.1:%d/settings", port)
	if got := settingsURL(cfg); got != want {
		t.Errorf("settingsURL = %q, want %q -- it must use the port the server actually "+
			"bound, not the one it asked for", got, want)
	}
}

// TestSettingsURLIsEmptyWhenNothingServesIt is what the menu gates on: no URL means the item is
// disabled rather than opening a browser at a dead port.
func TestSettingsURLIsEmptyWhenNothingServesIt(t *testing.T) {
	liveTunnelFixture(t, "demo", 0) // no tunnel
	tempServer.Stop()               // and no settings server

	cfg := &config.ClientConfig{Subdomain: "demo"}
	if got := settingsURL(cfg); got != "" {
		t.Errorf("settingsURL = %q, want \"\" -- with neither server up there is nowhere to "+
			"send the user, and the menu item must be disabled instead", got)
	}
}
