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
	t.Setenv("AWS_REGION", "")
	t.Setenv("LFT_INSTANCE_TAG", "")
	t.Setenv("LFT_POWER_HOOK", "")
	t.Setenv("LFT_OPS_TARGET", "")
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

	target, err := ResolveDeployTarget("", "", "", "")
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

	target, err := ResolveDeployTarget("", "", "", "")
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

	target, err := ResolveDeployTarget("", "flag-host.example.com", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.Host != "flag-host.example.com" {
		t.Errorf("expected the explicit flag value to win over both env var and config file, got host %q", target.Host)
	}
}

func TestResolveDeployTarget_NothingConfiguredErrorsClearly(t *testing.T) {
	clearTargetEnv(t)

	_, err := ResolveDeployTarget("", "", "", "")
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

	target, err := ResolveDeployTarget("", "", "", "")
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

	target, err := ResolveDeployTarget("ubuntu", "example.com", "~/.ssh/my-key.pem", "")
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

	target, err := ResolveNginxTarget(nil, "", "")
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

	target, err := ResolveNginxTarget([]string{"flag.example.com"}, "9090", "")
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

	_, err := ResolveNginxTarget(nil, "", "")
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
// (mapping -i/-u/-s/-target onto identityFile/user/host/target) is still worth covering
// directly.
func TestParseTargetFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantIdentity string
		wantUser     string
		wantHost     string
		wantTarget   string
		wantErr      bool
	}{
		{
			name:         "all four flags in the documented order",
			args:         []string{"-i", "/tmp/key.pem", "-u", "ubuntu", "-s", "central.example.com", "-target", "production"},
			wantIdentity: "/tmp/key.pem",
			wantUser:     "ubuntu",
			wantHost:     "central.example.com",
			wantTarget:   "production",
		},
		{
			name:         "flags in a different order still parse correctly",
			args:         []string{"-s", "central.example.com", "-i", "/tmp/key.pem", "-u", "ubuntu"},
			wantIdentity: "/tmp/key.pem",
			wantUser:     "ubuntu",
			wantHost:     "central.example.com",
		},
		{
			name:         "only -i, no -u/-s/-target",
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
			identity, user, host, target, err := parseTargetFlags("test", tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if identity != tt.wantIdentity || user != tt.wantUser || host != tt.wantHost || target != tt.wantTarget {
				t.Errorf("got (identity=%q, user=%q, host=%q, target=%q), want (identity=%q, user=%q, host=%q, target=%q)",
					identity, user, host, target, tt.wantIdentity, tt.wantUser, tt.wantHost, tt.wantTarget)
			}
		})
	}
}

// -- Multi-target config file tests (#1028) --

func TestResolveDeployTarget_MultiTarget_SingleEntryUsedImplicitly(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: central.example.com
      identity_file: /tmp/prod-key.pem
`)

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected the file's only target to be used implicitly, got error: %v", err)
	}
	if target.Host != "central.example.com" {
		t.Errorf("unexpected target: %+v", target)
	}
}

func TestResolveDeployTarget_MultiTarget_SelectedByFlag(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
  staging:
    central:
      user: ubuntu
      host: staging.example.com
      identity_file: /tmp/staging-key.pem
`)

	target, err := ResolveDeployTarget("", "", "", "staging")
	if err != nil {
		t.Fatalf("expected resolution to succeed with an explicit -target, got: %v", err)
	}
	if target.Host != "staging.example.com" {
		t.Errorf("expected the staging target to be selected, got: %+v", target)
	}
}

func TestResolveDeployTarget_MultiTarget_SelectedByEnvVar(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
  staging:
    central:
      user: ubuntu
      host: staging.example.com
      identity_file: /tmp/staging-key.pem
`)
	t.Setenv("LFT_OPS_TARGET", "production")

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed via LFT_OPS_TARGET, got: %v", err)
	}
	if target.Host != "prod.example.com" {
		t.Errorf("expected the production target to be selected via env var, got: %+v", target)
	}
}

func TestResolveDeployTarget_MultiTarget_FlagOverridesEnvVar(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
  staging:
    central:
      user: ubuntu
      host: staging.example.com
      identity_file: /tmp/staging-key.pem
`)
	t.Setenv("LFT_OPS_TARGET", "production")

	target, err := ResolveDeployTarget("", "", "", "staging")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.Host != "staging.example.com" {
		t.Errorf("expected the -target flag to win over LFT_OPS_TARGET, got: %+v", target)
	}
}

func TestResolveDeployTarget_MultiTarget_AmbiguousWithoutSelectionErrorsWithNames(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
  staging:
    central:
      user: ubuntu
      host: staging.example.com
      identity_file: /tmp/staging-key.pem
`)

	_, err := ResolveDeployTarget("", "", "", "")
	if err == nil {
		t.Fatal("expected an error when the file defines multiple targets and none is selected")
	}
	for _, want := range []string{"production", "staging", "-target", "LFT_OPS_TARGET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
}

func TestResolveDeployTarget_MultiTarget_UnknownNameErrorsWithAvailableNames(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
`)

	_, err := ResolveDeployTarget("", "", "", "nonexistent")
	if err == nil {
		t.Fatal("expected an error when the requested target doesn't exist")
	}
	if !strings.Contains(err.Error(), "nonexistent") || !strings.Contains(err.Error(), "production") {
		t.Errorf("expected error to name both the requested and available targets, got: %v", err)
	}
}

func TestResolveNginxTarget_MultiTarget_SelectedByFlag(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    nginx:
      domains:
        - prod.example.com
      port: "8080"
  staging:
    nginx:
      domains:
        - staging.example.com
      port: "9090"
`)

	target, err := ResolveNginxTarget(nil, "", "staging")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if len(target.Domains) != 1 || target.Domains[0] != "staging.example.com" || target.Port != "9090" {
		t.Errorf("expected the staging target's nginx config, got: %+v", target)
	}
}

// --- AWSRegion resolution (#1050) -- deliberately optional, unlike user/host/identity_file:
// missing it is never an error, it just means ensureInstanceRunning becomes a no-op.

func TestResolveDeployTargetWithRegion_Unset_IsNotAnError(t *testing.T) {
	clearTargetEnv(t)

	target, err := ResolveDeployTargetWithRegion("ubuntu", "host.example.com", "/tmp/key.pem", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed without an AWS region, got: %v", err)
	}
	if target.AWSRegion != "" {
		t.Errorf("expected empty AWSRegion, got %q", target.AWSRegion)
	}
}

func TestResolveDeployTargetWithRegion_FromFlag(t *testing.T) {
	clearTargetEnv(t)

	target, err := ResolveDeployTargetWithRegion("ubuntu", "host.example.com", "/tmp/key.pem", "ap-northeast-1", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.AWSRegion != "ap-northeast-1" {
		t.Errorf("expected flag value to win, got %q", target.AWSRegion)
	}
}

func TestResolveDeployTargetWithRegion_FromEnv(t *testing.T) {
	clearTargetEnv(t)
	t.Setenv("AWS_REGION", "sa-east-1")

	target, err := ResolveDeployTargetWithRegion("ubuntu", "host.example.com", "/tmp/key.pem", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.AWSRegion != "sa-east-1" {
		t.Errorf("expected env var value, got %q", target.AWSRegion)
	}
}

func TestResolveDeployTargetWithRegion_FromConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  aws_region: eu-west-1
`)

	target, err := ResolveDeployTargetWithRegion("", "", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.AWSRegion != "eu-west-1" {
		t.Errorf("expected config file value, got %q", target.AWSRegion)
	}
}

func TestResolveDeployTargetWithRegion_FlagWinsOverEnvAndConfig(t *testing.T) {
	clearTargetEnv(t)
	t.Setenv("AWS_REGION", "env-region")
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  aws_region: config-region
`)

	target, err := ResolveDeployTargetWithRegion("", "", "", "flag-region", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.AWSRegion != "flag-region" {
		t.Errorf("expected flag to win over env and config, got %q", target.AWSRegion)
	}
}

// The instance tag obeys the same precedence as every other field in
// ResolveDeployTarget's documented contract -- env var first, then the yaml. It has no
// flag, so those are the only two sources.
//
// Regression tests: the field was first written checking the config file *before* the
// env var, which silently inverted that order for this one field while every sibling
// obeyed it.

func TestResolveDeployTarget_InstanceTagFromConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  instance_tag: Project=from-yaml
`)

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.InstanceTag != "Project=from-yaml" {
		t.Errorf("expected the instance tag to come from the config file, got %q", target.InstanceTag)
	}
}

func TestResolveDeployTarget_InstanceTagEnvOverridesConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  instance_tag: Project=from-yaml
`)
	t.Setenv("LFT_INSTANCE_TAG", "Project=from-env")

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.InstanceTag != "Project=from-env" {
		t.Errorf("expected LFT_INSTANCE_TAG to override the config file, got %q", target.InstanceTag)
	}
}

// The tag must also reach a named target, since opsConfigTarget backs both the
// single-target and multi-target file shapes.
func TestResolveDeployTarget_InstanceTagFromNamedTarget(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
targets:
  production:
    central:
      user: ubuntu
      host: prod.example.com
      identity_file: /tmp/prod-key.pem
      instance_tag: Project=prod-tag
`)

	target, err := ResolveDeployTarget("", "", "", "production")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.InstanceTag != "Project=prod-tag" {
		t.Errorf("expected the named target's instance tag, got %q", target.InstanceTag)
	}
}

// Nothing configured must mean no tag filter at all. A default here would name one
// organisation's resources and make the lookup silently wrong for everyone else --
// the regression this field exists to fix.
func TestResolveDeployTarget_InstanceTagHasNoDefault(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
`)

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.InstanceTag != "" {
		t.Errorf("expected no instance tag when none is configured, got %q", target.InstanceTag)
	}
	// What an empty tag then means for the lookup is the hook's business, not this
	// package's -- the filter-building moved to scripts/common/lfr-power-hook-aws.sh
	// with #1187. All that matters here is that nothing is invented on the way through.
}

// power_hook resolves like every other field: env var first, then the yaml (#1187).

func TestResolveDeployTarget_PowerHookFromConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  power_hook: scripts/common/lfr-power-hook-aws.sh
`)

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.PowerHook != "scripts/common/lfr-power-hook-aws.sh" {
		t.Errorf("expected the hook from the config file, got %q", target.PowerHook)
	}
}

func TestResolveDeployTarget_PowerHookEnvOverridesConfigFile(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
  power_hook: /from/yaml.sh
`)
	t.Setenv("LFT_POWER_HOOK", "/from/env.sh")

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed, got: %v", err)
	}
	if target.PowerHook != "/from/env.sh" {
		t.Errorf("expected LFT_POWER_HOOK to override the config file, got %q", target.PowerHook)
	}
}

// Power management stays opt-in: no hook, no error, deploys simply don't touch power.
func TestResolveDeployTarget_NoPowerHookIsFine(t *testing.T) {
	clearTargetEnv(t)
	writeOpsConfig(t, `
central:
  user: ubuntu
  host: central.example.com
  identity_file: /tmp/my-key.pem
`)

	target, err := ResolveDeployTarget("", "", "", "")
	if err != nil {
		t.Fatalf("expected resolution to succeed with no power hook, got: %v", err)
	}
	if target.PowerHook != "" {
		t.Errorf("expected no power hook, got %q", target.PowerHook)
	}
}
