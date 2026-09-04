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
	// SessionDuration and SessionMaxLifetime are the portal session policy this deployment
	// declares, empty when it declares none (#1681).
	SessionDuration    string
	SessionMaxLifetime string
	// AWSRegion is optional and, unlike the three fields above, has no hardcoded
	// requirement -- most commands never need it. When set, `deploy` uses it to check
	// whether the target's EC2 instance is stopped (e.g. edge-us/edge-apac's
	// deliberately-wrong midnight-8am shutdown schedule kept as a live test case for
	// #885) and, if so, starts it for the duration of the deploy and restores its
	// previous power state afterward (#1050). Empty means "don't touch EC2 power state
	// at all", which is the same as before this field existed.
	AWSRegion string
	// InstanceTag optionally narrows the instance lookup to resources carrying a given
	// tag, written "Key=Value" (e.g. "Project=my-tunnel"). Empty means match on address
	// alone.
	//
	// Operator-supplied on purpose. A tag value is deployment-specific by definition, and
	// this file carries no defaults of its own (#1015/#1016) -- briefly hardcoding one
	// project's tag here put a single deployment's identity into MIT, provider-neutral
	// code that other people are meant to run.
	InstanceTag string
	// PowerHook is the script that reads and changes the target's power state. Empty
	// means power management is not configured and deploys never touch it.
	//
	// This is what makes the feature provider-agnostic (#1187): lfr-tunnel itself knows
	// only the hook's contract, so running on something other than AWS means writing a
	// script, not patching Go. scripts/common/lfr-power-hook-aws.sh is the bundled
	// reference implementation. AWSRegion and InstanceTag above are no longer interpreted
	// here -- they are passed to the hook as AWS_REGION and LFT_INSTANCE_TAG, for whatever
	// the operator's chosen script makes of them.
	PowerHook string
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
		InstanceTag  string `yaml:"instance_tag"`
		PowerHook    string `yaml:"power_hook"`
	} `yaml:"central"`
	Nginx struct {
		Domains []string `yaml:"domains"`
		Port    string   `yaml:"port"`
	} `yaml:"nginx"`
	// Session is the portal session policy this deployment intends (#1681).
	//
	// Declared here, in the operator's own file, rather than committed to the repo: these are
	// deployment decisions, not project ones -- a staging gateway may reasonably want shorter
	// sessions than production, and the repo has no business asserting one answer for every
	// installation. It sits beside central: and nginx: because that is already where a
	// deployment's own truth lives.
	//
	// Both fields are optional. An empty value means "this deployment does not manage that
	// setting", and check-config and reconcile-server-config both leave it alone -- so an
	// existing lfr-tunnel-ops.yaml keeps working untouched.
	Session struct {
		Duration    string `yaml:"portal_session_duration"`
		MaxLifetime string `yaml:"portal_session_max_lifetime"`
	} `yaml:"session"`
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
	// ClientDefaults is file-level rather than per-target, and deliberately so: `build`
	// produces one dist/ that every target publishes, so there is no per-target answer to
	// give. Putting it inside a target would also inherit the trap documented on session:,
	// where a block at the top level of a multi-target file is silently ignored.
	ClientDefaults ClientDefaults `yaml:"client_defaults"`
}

// ClientDefaults are the URLs compiled into client binaries at build time.
//
// These exist because a local `build` previously had no source for them at all (#1723). The
// release workflow supplies them from repository variables; a laptop supplied nothing, so
// `ops build` produced clients with no gateway -- the #1692 condition, which forces every user
// onto -server and therefore pins them. Declaring them here gives a local build the same source
// of truth CI has, alongside every other deployment setting.
type ClientDefaults struct {
	ServerURL     string `yaml:"server_url"`
	StatusPageURL string `yaml:"status_page_url"`
	PortalURL     string `yaml:"portal_url"`
}

// ResolveClientDefaults returns the URLs to compile into client binaries.
//
// Environment first, then the config file, PER FIELD. Per field matters: setting only
// LFT_DEFAULT_SERVER_URL must not blank the status page URL the file supplies, which an
// all-or-nothing choice between the two sources would do.
//
// The environment wins because that is how .github/workflows/release.yml supplies these from
// repository variables. If the file won, a local checkout could change what CI ships.
func ResolveClientDefaults(file ClientDefaults) ClientDefaults {
	return ClientDefaults{
		ServerURL:     GetEnvOrDefault("LFT_DEFAULT_SERVER_URL", file.ServerURL),
		StatusPageURL: GetEnvOrDefault("LFT_DEFAULT_STATUS_PAGE_URL", file.StatusPageURL),
		PortalURL:     GetEnvOrDefault("LFT_DEFAULT_PORTAL_URL", file.PortalURL),
	}
}

// LoadClientDefaults reads the file-level client_defaults block, independently of any target.
//
// Absent file or absent block yields a zero value and no error: the config file is optional,
// and callers decide whether an empty ServerURL is acceptable. A malformed file IS an error --
// silently building with no defaults is the failure this exists to prevent.
func LoadClientDefaults() (ClientDefaults, error) {
	path := GetEnvOrDefault("LFT_OPS_CONFIG", "lfr-tunnel-ops.yaml")

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ClientDefaults{}, nil
		}
		return ClientDefaults{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg opsConfigFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ClientDefaults{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg.ClientDefaults, nil
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
	if target.InstanceTag == "" {
		target.InstanceTag = os.Getenv("LFT_INSTANCE_TAG")
	}
	if target.PowerHook == "" {
		target.PowerHook = os.Getenv("LFT_POWER_HOOK")
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
		if target.InstanceTag == "" {
			target.InstanceTag = cfg.Central.InstanceTag
		}
		if target.PowerHook == "" {
			target.PowerHook = cfg.Central.PowerHook
		}
		// No flag or env override: session policy is a deliberate, recorded decision, not
		// something to set for one invocation. Anything else would make the declared value and
		// the applied value differ with nothing to show for it (#1681).
		target.SessionDuration = cfg.Session.Duration
		target.SessionMaxLifetime = cfg.Session.MaxLifetime
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

// deployClientsFlags is what deploy-clients accepts beyond a deployment target: three overrides
// for the three checks it makes before and after uploading (#1279, #1692).
//
// A struct rather than more return values: the third override tipped the tuple past the point
// where a caller can be trusted to keep the booleans in the right order, and two of the three
// mean "publish something the guard says not to".
type deployClientsFlags struct {
	identityFile string
	user         string
	host         string
	target       string

	// allowStale publishes dist/ even if it was not built from the current source (#1279).
	allowStale bool

	// allowNoDefault publishes clients with no default gateway compiled in (#1692). Separate
	// from allowStale because they answer different questions -- artefacts can be current and
	// still be built from a shell with no LFT_DEFAULT_SERVER_URL, which is exactly what
	// happened.
	allowNoDefault bool

	// skipVerify skips the post-upload check that the gateway serves the uploaded bytes.
	skipVerify bool
}

// parseDeployClientsFlags is parseTargetFlags plus the overrides only deploy-clients has
// (#1279). A separate function for the same reason parseTargetFlagsWithRegion is one: adding
// these to the shared parser would make `deploy` accept flags it silently ignores, which is its
// own small version of a command reporting something it did not do.
func parseDeployClientsFlags(name string, args []string) (deployClientsFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	i := fs.String("i", "", "path to SSH private key file")
	u := fs.String("u", "", "SSH username on the central VPS")
	s := fs.String("s", "", "SSH host (IP or hostname) of the central VPS")
	t := fs.String("target", "", "named target to use from a multi-target lfr-tunnel-ops.yaml (#1028)")
	stale := fs.Bool("allow-stale", false, "publish dist/ even if it was not built from the current source")
	noDefault := fs.Bool("allow-no-default", false, "publish clients with no default gateway compiled in (#1692)")
	noVerify := fs.Bool("skip-verify", false, "skip the post-upload check that the gateway is serving the uploaded bytes")
	if err := fs.Parse(args); err != nil {
		return deployClientsFlags{}, err
	}
	return deployClientsFlags{
		identityFile:   *i,
		user:           *u,
		host:           *s,
		target:         *t,
		allowStale:     *stale,
		allowNoDefault: *noDefault,
		skipVerify:     *noVerify,
	}, nil
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
