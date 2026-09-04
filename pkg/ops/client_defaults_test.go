package ops

import (
	"os"
	"path/filepath"
	"testing"
)

// A local build had no source at all for the URLs baked into client binaries (#1723). The
// release workflow supplies them from repository variables; a laptop supplied nothing, so
// `ops build` produced clients with no gateway -- the #1692 condition, which forces every user
// onto -server and therefore pins them and disables failover. These tests cover the config
// file that gives a local build the same source of truth.

func TestLoadClientDefaults_ReadsTheFileLevelBlock(t *testing.T) {
	// Declared alongside targets on purpose: client_defaults must be read from the file
	// level even when targets: is present, because `build` produces one dist/ that every
	// target publishes. Reading it per-target would inherit the trap documented on session:,
	// where a block at the top level of a multi-target file is silently ignored.
	writeOpsConfig(t, `
client_defaults:
  server_url: https://gw.example.com
  status_page_url: https://status.example.com
  portal_url: https://gw.example.com/portal
targets:
  central:
    user: ubuntu
    host: central.example.com
  edge:
    user: ubuntu
    host: edge.example.com
`)

	got, err := LoadClientDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ServerURL != "https://gw.example.com" {
		t.Errorf("ServerURL = %q, want the file-level value -- a multi-target file must not hide it", got.ServerURL)
	}
	if got.StatusPageURL != "https://status.example.com" {
		t.Errorf("StatusPageURL = %q", got.StatusPageURL)
	}
	if got.PortalURL != "https://gw.example.com/portal" {
		t.Errorf("PortalURL = %q", got.PortalURL)
	}
}

func TestLoadClientDefaults_AbsentBlockIsNotAnError(t *testing.T) {
	// A config file that predates this block is entirely normal, and must not stop a build
	// that supplies the values through the environment instead.
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
`)

	got, err := LoadClientDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ServerURL != "" || got.StatusPageURL != "" || got.PortalURL != "" {
		t.Errorf("expected a zero value from a file with no client_defaults, got %+v", got)
	}
}

func TestLoadClientDefaults_MissingFileIsNotAnError(t *testing.T) {
	// The config file is optional -- flags and env vars can cover everything -- so its
	// absence must not be fatal. The guard on an empty ServerURL is what catches the real
	// problem, and it lives at the call site.
	t.Setenv("LFT_OPS_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yaml"))

	got, err := LoadClientDefaults()
	if err != nil {
		t.Fatalf("a missing config file must not be an error: %v", err)
	}
	if got.ServerURL != "" {
		t.Errorf("expected a zero value, got %+v", got)
	}
}

func TestLoadClientDefaults_MalformedFileIsAnError(t *testing.T) {
	// Deliberately NOT lenient. Silently treating an unparseable file as "no defaults" is how
	// a typo becomes a shipped binary with no gateway -- the exact failure this exists to
	// prevent, arrived at by a different route.
	path := filepath.Join(t.TempDir(), "lfr-tunnel-ops.yaml")
	if err := os.WriteFile(path, []byte("client_defaults: [this is not a mapping\n"), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	t.Setenv("LFT_OPS_CONFIG", path)

	if _, err := LoadClientDefaults(); err == nil {
		t.Error("a malformed config file was accepted as 'no defaults', so a typo would build a gateway-less client")
	}
}

// The environment must win over the file, because that is how the release workflow supplies
// these from repository variables. A file that overrode it would make a local checkout able to
// change what CI ships.
func TestClientDefaults_EnvironmentWinsOverTheFile(t *testing.T) {
	writeOpsConfig(t, `
client_defaults:
  server_url: https://from-file.example.com
  status_page_url: https://status-from-file.example.com
`)
	t.Setenv("LFT_DEFAULT_SERVER_URL", "https://from-env.example.com")
	t.Setenv("LFT_DEFAULT_STATUS_PAGE_URL", "")

	fileDefaults, err := LoadClientDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The function BuildCommand actually calls, rather than a copy of its logic -- a mirrored
	// implementation would keep passing after the real one was changed.
	resolved := ResolveClientDefaults(fileDefaults)
	serverURL := resolved.ServerURL
	statusURL := resolved.StatusPageURL

	if serverURL != "https://from-env.example.com" {
		t.Errorf("server URL = %q, want the environment value -- release.yml must keep winning", serverURL)
	}
	// Per field, not all-or-nothing: an env var set for one URL must not blank the others.
	if statusURL != "https://status-from-file.example.com" {
		t.Errorf("status URL = %q, want the file value; precedence is per field", statusURL)
	}
}

func TestRequireBuildableDefaults_PassesWhenAGatewayIsSet(t *testing.T) {
	// Guards the non-fatal paths only. The refusal calls os.Exit, which cannot be exercised
	// in-process; it is covered by the CLI-level behaviour instead.
	RequireBuildableDefaults("https://gw.example.com", false)
	RequireBuildableDefaults("https://gw.example.com", true)
}

func TestRequireBuildableDefaults_AllowNoDefaultDoesNotExit(t *testing.T) {
	// If this ever starts exiting, a deployment that genuinely wants no baked-in gateway can
	// no longer build at all.
	RequireBuildableDefaults("", true)
}

// The file must be consulted at all. This is the regression that shipped: BuildCommand read
// only the environment, so a config file could declare a gateway and the build ignored it.
func TestResolveClientDefaults_FallsBackToTheFile(t *testing.T) {
	t.Setenv("LFT_DEFAULT_SERVER_URL", "")
	t.Setenv("LFT_DEFAULT_STATUS_PAGE_URL", "")
	t.Setenv("LFT_DEFAULT_PORTAL_URL", "")

	got := ResolveClientDefaults(ClientDefaults{
		ServerURL:     "https://from-file.example.com",
		StatusPageURL: "https://status-from-file.example.com",
		PortalURL:     "https://from-file.example.com/portal",
	})

	if got.ServerURL != "https://from-file.example.com" {
		t.Errorf("ServerURL = %q -- the config file was ignored, which is the #1723 regression", got.ServerURL)
	}
	if got.StatusPageURL != "https://status-from-file.example.com" {
		t.Errorf("StatusPageURL = %q, want the file value", got.StatusPageURL)
	}
	if got.PortalURL != "https://from-file.example.com/portal" {
		t.Errorf("PortalURL = %q, want the file value", got.PortalURL)
	}
}
