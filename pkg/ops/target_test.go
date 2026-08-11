package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOpsConfig writes a temp lfr-tunnel-ops.yaml-shaped file and points LFT_OPS_CONFIG at
// it for the duration of the test.
func writeOpsConfig(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lfr-tunnel-ops.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}
	t.Setenv("LFT_OPS_CONFIG", path)
}

// clearTargetEnv resets every env var ResolveDeployTarget/ResolveNginxTarget consult, so
// each test starts from a clean slate regardless of what's ambient in the test runner's
// own environment.
func clearTargetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("VPS_USER", "")
	t.Setenv("VPS_IP", "")
	t.Setenv("LFT_IDENTITY_FILE", "")
	t.Setenv("LFT_OPS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
}

func TestResolveDeployTarget_ConfigFileOnly(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
`)

	target, err := ResolveDeployTarget("", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed from config file alone, got: %v", err)
	}
	if target.User != "ubuntu" || target.Host != "central.example.com" || target.IdentityFile != "/tmp/my-key.pem" {
		t.Errorf("unexpected target: %+v", target)
	}
}

func TestResolveDeployTarget_EnvOverridesConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: config-user
  host: config-host.example.com
  identity_file: /tmp/config-key.pem
`)
	t.Setenv("VPS_IP", "env-host.example.com")

	target, err := ResolveDeployTarget("", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.Host != "env-host.example.com" {
		t.Errorf("expected env var VPS_IP to override the config file, got host %q", target.Host)
	}
	// Fields not overridden by env still come from the config file.
	if target.User != "config-user" || target.IdentityFile != "/tmp/config-key.pem" {
		t.Errorf("expected non-overridden fields to still come from the config file, got: %+v", target)
	}
}

func TestResolveDeployTarget_FlagOverridesEnvAndConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: config-user
  host: config-host.example.com
  identity_file: /tmp/config-key.pem
`)
	t.Setenv("VPS_IP", "env-host.example.com")

	target, err := ResolveDeployTarget("", "flag-host.example.com", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.Host != "flag-host.example.com" {
		t.Errorf("expected the explicit flag value to win over both env var and config file, got host %q", target.Host)
	}
}

func TestResolveDeployTarget_NothingConfiguredErrorsClearly(t *testing.T) {
	clearTargetEnv(t)

	_, err := ResolveDeployTarget("", "", "")
	if err == nil {
		t.Fatal("expected an error when no target is configured at all")
	}
	for _, want := range []string{"user", "host", "identity file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention the missing %q field, got: %v", want, err)
		}
	}
}

func TestResolveDeployTarget_MissingConfigFileStillWorksViaEnv(t *testing.T) {
	clearTargetEnv(t)
	t.Setenv("VPS_USER", "ubuntu")
	t.Setenv("VPS_IP", "env-only.example.com")
	t.Setenv("LFT_IDENTITY_FILE", "/tmp/env-key.pem")

	target, err := ResolveDeployTarget("", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed from env vars alone with no config file present, got: %v", err)
	}
	if target.User != "ubuntu" || target.Host != "env-only.example.com" || target.IdentityFile != "/tmp/env-key.pem" {
		t.Errorf("unexpected target: %+v", target)
	}
}

func TestResolveDeployTarget_ExpandsHomeDir(t *testing.T) {
	clearTargetEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home directory in this environment")
	}

	target, err := ResolveDeployTarget("ubuntu", "example.com", "~/.ssh/my-key.pem")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.IdentityFile != home+"/.ssh/my-key.pem" {
		t.Errorf("expected ~/ to expand to the home directory, got %q", target.IdentityFile)
	}
}

func TestResolveNginxTarget_ConfigFileFallback(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
nginx:
  domains:
    - central.example.com
    - central.example.org
  port: "8080"
`)

	target, err := ResolveNginxTarget(nil, "")
	if err != nil {
		t.Fatalf("expected resolution to succeed from config file alone, got: %v", err)
	}
	if len(target.Domains) != 2 || target.Domains[0] != "central.example.com" || target.Domains[1] != "central.example.org" {
		t.Errorf("unexpected domains: %v", target.Domains)
	}
	if target.Port != "8080" {
		t.Errorf("expected port 8080, got %q", target.Port)
	}
}

func TestResolveNginxTarget_FlagsOverrideConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
nginx:
  domains:
    - config.example.com
  port: "8080"
`)

	target, err := ResolveNginxTarget([]string{"flag.example.com"}, "9090")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if len(target.Domains) != 1 || target.Domains[0] != "flag.example.com" {
		t.Errorf("expected the flag-provided domain to win over the config file, got: %v", target.Domains)
	}
	if target.Port != "9090" {
		t.Errorf("expected the flag-provided port to win over the config file, got %q", target.Port)
	}
}

func TestResolveNginxTarget_NothingConfiguredErrorsClearly(t *testing.T) {
	clearTargetEnv(t)

	_, err := ResolveNginxTarget(nil, "")
	if err == nil {
		t.Fatal("expected an error when no nginx target is configured at all")
	}
	for _, want := range []string{"domains", "port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention the missing %q field, got: %v", want, err)
		}
	}
}

// TestParseTargetFlags covers exactly the bug class the original hand-rolled
// `args[0] == "-i"` positional checks were exposed to: silently misparsing the moment more
// than one flag exists, or the caller passes them in a different order than the code
// happened to expect. flag.FlagSet (stdlib, well-tested) fixes this, but the wiring
// (mapping -i/-u/-s onto identityFile/user/host) is still worth covering directly.
func TestParseTargetFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantIdentity string
		wantUser     string
		wantHost     string
		wantErr      bool
	}{
		{
			name:         "all three flags in the documented order",
			args:         []string{"-i", "/tmp/key.pem", "-u", "ubuntu", "-s", "central.example.com"},
			wantIdentity: "/tmp/key.pem",
			wantUser:     "ubuntu",
			wantHost:     "central.example.com",
		},
		{
			name:         "flags in a different order still parse correctly",
			args:         []string{"-s", "central.example.com", "-i", "/tmp/key.pem", "-u", "ubuntu"},
			wantIdentity: "/tmp/key.pem",
			wantUser:     "ubuntu",
			wantHost:     "central.example.com",
		},
		{
			name:         "only -i, no -u/-s",
			args:         []string{"-i", "/tmp/key.pem"},
			wantIdentity: "/tmp/key.pem",
		},
		{
			name: "no flags at all",
			args: []string{},
		},
		{
			name:    "unrecognized flag errors instead of being silently ignored",
			args:    []string{"-bogus", "value"},
			wantErr: true,
		},
		{
			name:    "flag with no value errors instead of silently leaving it empty",
			args:    []string{"-i"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, user, host, err := parseTargetFlags("test", tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if identity != tt.wantIdentity || user != tt.wantUser || host != tt.wantHost {
				t.Errorf("got (identity=%q, user=%q, host=%q), want (identity=%q, user=%q, host=%q)",
					identity, user, host, tt.wantIdentity, tt.wantUser, tt.wantHost)
			}
		})
	}
}
