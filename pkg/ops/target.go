package ops

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployTarget is the fully-resolved "who am I SSHing into, as whom, with which key"
// for any lfr-tunnel-ops command that touches the central VPS.
type DeployTarget struct {
	User         string
	Host         string
	IdentityFile string
	// AWSRegion is optional and, unlike the three fields above, has no hardcoded
	// requirement -- most commands never need it. When set, `deploy` uses it to check
	// whether the target's EC2 instance is stopped (e.g. edge-us/edge-apac's
	// deliberately-wrong midnight-8am shutdown schedule kept as a live test case for
	// #885) and, if so, starts it for the duration of the deploy and restores its
	// previous power state afterward (#1050). Empty means "don't touch EC2 power state
	// at all", which is the same as before this field existed.
	AWSRegion string
}

// NginxTarget is the fully-resolved input for reconcile-nginx beyond the DeployTarget
// above -- which domain groups this central serves and which local port lfr-tunneld binds.
type NginxTarget struct {
	Domains []string
	Port    string
}

// opsConfigTarget is one deployment target's worth of config -- either the top-level
// central:/nginx: fields for a single-target file, or one entry under targets: for a
// multi-target file (#1028).
type opsConfigTarget struct {
	Central struct {
		User         string `yaml:"user"`
		Host         string `yaml:"host"`
		IdentityFile string `yaml:"identity_file"`
		AWSRegion    string `yaml:"aws_region"`
	} `yaml:"central"`
	Nginx struct {
		Domains []string `yaml:"domains"`
		Port    string   `yaml:"port"`
	} `yaml:"nginx"`
}

// opsConfigFile is the schema of lfr-tunnel-ops.yaml (see lfr-tunnel-ops.yaml.example).
// This is a generic, reusable config file -- it carries no default values of its own, same
// as scripts/common/setup-central-vps.sh and scripts/common/lfr-vanity-hook.sh: every value
// must be supplied explicitly by the operator, which is the only place that actually knows
// the right values for a given deployment (#1015/#1016).
//
// Supports two shapes, so a single-target file never has to adopt the more verbose one just
// to stay valid (#1028):
//   - Single-target (the original shape): central:/nginx: directly at the top level.
//   - Multi-target: a targets: map of named opsConfigTarget entries, e.g. for managing more
//     than one environment (staging/production) from the same checkout. Selected via -target/
//     LFT_OPS_TARGET; if there's exactly one entry, it's used implicitly without needing to
//     specify a name.
//
// A file is never expected to mix both shapes -- if targets: is present and non-empty, it
// takes precedence and the top-level central:/nginx: fields are ignored.
type opsConfigFile struct {
	opsConfigTarget `yaml:",inline"`
	Targets         map[string]opsConfigTarget `yaml:"targets"`
}

// loadOpsConfigFile reads the path named by LFT_OPS_CONFIG (defaulting to
// ./lfr-tunnel-ops.yaml) and resolves it down to the single opsConfigTarget the caller
// should actually use, given targetName (the -target flag value, or "" if not passed).
// Returns (nil, nil) -- not an error -- if the file doesn't exist, since the config file is
// entirely optional as long as env vars/flags cover whatever a given command actually needs.
func loadOpsConfigFile(targetName string) (*opsConfigTarget, error) {
	path := GetEnvOrDefault("LFT_OPS_CONFIG", "lfr-tunnel-ops.yaml")

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg opsConfigFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if len(cfg.Targets) == 0 {
		return &cfg.opsConfigTarget, nil
	}

	if targetName != "" {
		target, ok := cfg.Targets[targetName]
		if !ok {
			return nil, fmt.Errorf("%s has no target named %q -- available targets: %s", path, targetName, strings.Join(sortedTargetNames(cfg.Targets), ", "))
		}
		return &target, nil
	}

	if len(cfg.Targets) == 1 {
		for _, target := range cfg.Targets {
			return &target, nil
		}
	}

	return nil, fmt.Errorf("%s defines multiple targets (%s) -- specify which one with -target or LFT_OPS_TARGET", path, strings.Join(sortedTargetNames(cfg.Targets), ", "))
}

// sortedTargetNames returns targets' keys sorted, so error messages listing them are
// deterministic (map iteration order isn't) and therefore actually testable.
func sortedTargetNames(targets map[string]opsConfigTarget) []string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveDeployTarget resolves the deployment target for any command that SSHes into the
// central VPS. Precedence per field, highest first: the flag value passed in (empty string
// means "not passed"), then the matching environment variable, then lfr-tunnel-ops.yaml.
// There is deliberately no hardcoded fallback for any field -- a past migration left three
// commands silently targeting a decommissioned VPS because their hardcoded defaults were
// never updated (#1015). Missing fields produce one combined, actionable error rather than
// silently deploying to the wrong place.
//
// flagTarget selects which named target to use from a multi-target lfr-tunnel-ops.yaml
// (#1028) -- falls back to LFT_OPS_TARGET, then to the file's only target if it defines
// just one, then errors listing the available names if it defines more than one and neither
// selected. Single-target files (no targets: map at all) ignore this entirely.
func ResolveDeployTarget(flagUser, flagHost, flagIdentity, flagTarget string) (DeployTarget, error) {
	return ResolveDeployTargetWithRegion(flagUser, flagHost, flagIdentity, "", flagTarget)
}

// ResolveDeployTargetWithRegion is ResolveDeployTarget plus the optional AWSRegion field
// (#1050). A separate function rather than adding the parameter to ResolveDeployTarget
// itself, so every existing call site (most commands don't need EC2 power management)
// doesn't have to pass an empty string through for a field it doesn't care about.
func ResolveDeployTargetWithRegion(flagUser, flagHost, flagIdentity, flagAWSRegion, flagTarget string) (DeployTarget, error) {
	if flagTarget == "" {
		flagTarget = os.Getenv("LFT_OPS_TARGET")
	}
	cfg, err := loadOpsConfigFile(flagTarget)
	if err != nil {
		return DeployTarget{}, err
	}

	target := DeployTarget{
		User:         flagUser,
		Host:         flagHost,
		IdentityFile: flagIdentity,
		AWSRegion:    flagAWSRegion,
	}

	if target.User == "" {
		target.User = os.Getenv("VPS_USER")
	}
	if target.Host == "" {
		target.Host = os.Getenv("VPS_IP")
	}
	if target.IdentityFile == "" {
		target.IdentityFile = os.Getenv("LFT_IDENTITY_FILE")
	}
	if target.AWSRegion == "" {
		target.AWSRegion = os.Getenv("AWS_REGION")
	}

	if cfg != nil {
		if target.User == "" {
			target.User = cfg.Central.User
		}
		if target.Host == "" {
			target.Host = cfg.Central.Host
		}
		if target.IdentityFile == "" {
			target.IdentityFile = cfg.Central.IdentityFile
		}
		if target.AWSRegion == "" {
			target.AWSRegion = cfg.Central.AWSRegion
		}
	}

	var missing []string
	if target.User == "" {
		missing = append(missing, "user (-u flag, VPS_USER env var, or central.user in lfr-tunnel-ops.yaml)")
	}
	if target.Host == "" {
		missing = append(missing, "host (-s flag, VPS_IP env var, or central.host in lfr-tunnel-ops.yaml)")
	}
	if target.IdentityFile == "" {
		missing = append(missing, "identity file (-i flag, LFT_IDENTITY_FILE env var, or central.identity_file in lfr-tunnel-ops.yaml)")
	}
	if len(missing) > 0 {
		return DeployTarget{}, fmt.Errorf("no deployment target configured -- missing: %s\nSee lfr-tunnel-ops.yaml.example for how to set one up", strings.Join(missing, "; "))
	}

	target.IdentityFile = expandHomeDir(target.IdentityFile)
	return target, nil
}

// ResolveNginxTarget resolves reconcile-nginx's domains/port, falling back to
// lfr-tunnel-ops.yaml's nginx: section when the corresponding flag wasn't passed. Unlike
// DeployTarget's fields, these have no environment variable equivalent -- they aren't
// secrets or per-machine details, just avoids retyping the same domain list every run.
//
// flagTarget selects which named target to use, same as ResolveDeployTarget (#1028) --
// pass the same value used to resolve the DeployTarget for the same command invocation, so
// both resolve against the same target.
func ResolveNginxTarget(flagDomains []string, flagPort, flagTarget string) (NginxTarget, error) {
	if flagTarget == "" {
		flagTarget = os.Getenv("LFT_OPS_TARGET")
	}
	cfg, err := loadOpsConfigFile(flagTarget)
	if err != nil {
		return NginxTarget{}, err
	}

	target := NginxTarget{
		Domains: flagDomains,
		Port:    flagPort,
	}

	if len(target.Domains) == 0 && cfg != nil {
		target.Domains = cfg.Nginx.Domains
	}
	if target.Port == "" && cfg != nil {
		target.Port = cfg.Nginx.Port
	}

	var missing []string
	if len(target.Domains) == 0 {
		missing = append(missing, "domains (-domains flag or nginx.domains in lfr-tunnel-ops.yaml)")
	}
	if target.Port == "" {
		missing = append(missing, "port (-port flag or nginx.port in lfr-tunnel-ops.yaml)")
	}
	if len(missing) > 0 {
		return NginxTarget{}, fmt.Errorf("no nginx target configured -- missing: %s\nSee lfr-tunnel-ops.yaml.example for how to set one up", strings.Join(missing, "; "))
	}

	return target, nil
}

// parseTargetFlags parses the -i/-u/-s/-target flags common to every lfr-tunnel-ops command
// that resolves a DeployTarget, using the standard library's flag.FlagSet rather than
// hand-rolled positional checks (the original `args[0] == "-i"` style required -i to be the
// very first argument and would have silently misparsed the moment a second flag like -u/-s
// was added in any order). name is used only for flag.FlagSet's own internal
// error-prefixing; callers print their own usage text on error, so the FlagSet's built-in
// usage output is discarded.
//
// Callers with a leading positional argument of their own (e.g. maintenance's
// enable/disable action) must slice that off args before calling this -- flag.Parse stops
// at the first non-flag argument, so a positional arg has to come first either way.
func parseTargetFlags(name string, args []string) (identityFile, user, host, target string, err error) {
	identityFile, user, host, _, target, err = parseTargetFlagsWithRegion(name, args)
	return identityFile, user, host, target, err
}

// parseTargetFlagsWithRegion is parseTargetFlags plus the optional -aws-region flag
// (#1050) -- only DeployCommand needs it, so it's a separate function rather than
// changing parseTargetFlags's signature for every caller.
func parseTargetFlagsWithRegion(name string, args []string) (identityFile, user, host, awsRegion, target string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	i := fs.String("i", "", "path to SSH private key file")
	u := fs.String("u", "", "SSH username on the central VPS")
	s := fs.String("s", "", "SSH host (IP or hostname) of the central VPS")
	r := fs.String("aws-region", "", "AWS region the target's EC2 instance lives in -- if set, deploy will start it first if stopped and restore its previous power state afterward (#1050)")
	t := fs.String("target", "", "named target to use from a multi-target lfr-tunnel-ops.yaml (#1028)")
	if err := fs.Parse(args); err != nil {
		return "", "", "", "", "", err
	}
	return *i, *u, *s, *r, *t, nil
}

// expandHomeDir expands a leading ~ or ~/ the same way a shell would, since ssh/scp don't
// expand it themselves when passed as a literal -i argument from Go's exec.Command.
func expandHomeDir(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + "/" + strings.TrimPrefix(path, "~/")
		}
	}
	return path
}
