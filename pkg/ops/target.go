package ops

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployTarget is the fully-resolved "who am I SSHing into, as whom, with which key"
// for any lfr-tunnel-ops command that touches the central VPS.
type DeployTarget struct {
	User         string
	Host         string
	IdentityFile string
}

// NginxTarget is the fully-resolved input for reconcile-nginx beyond the DeployTarget
// above -- which domain groups this central serves and which local port lfr-tunneld binds.
type NginxTarget struct {
	Domains []string
	Port    string
}

// opsConfigFile is the schema of lfr-tunnel-ops.yaml (see lfr-tunnel-ops.yaml.example).
// This is a generic, reusable config file -- it carries no default values of its own, same
// as scripts/common/setup-central-vps.sh and scripts/common/lfr-vanity-hook.sh: every value
// must be supplied explicitly by the operator, which is the only place that actually knows
// the right values for a given deployment (#1015/#1016).
type opsConfigFile struct {
	Central struct {
		User         string `yaml:"user"`
		Host         string `yaml:"host"`
		IdentityFile string `yaml:"identity_file"`
	} `yaml:"central"`
	Nginx struct {
		Domains []string `yaml:"domains"`
		Port    string   `yaml:"port"`
	} `yaml:"nginx"`
}

// loadOpsConfigFile reads the path named by LFT_OPS_CONFIG, defaulting to
// ./lfr-tunnel-ops.yaml. Returns (nil, nil) -- not an error -- if the file doesn't exist,
// since the config file is entirely optional as long as env vars/flags cover whatever a
// given command actually needs.
func loadOpsConfigFile() (*opsConfigFile, error) {
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
	return &cfg, nil
}

// ResolveDeployTarget resolves the deployment target for any command that SSHes into the
// central VPS. Precedence per field, highest first: the flag value passed in (empty string
// means "not passed"), then the matching environment variable, then lfr-tunnel-ops.yaml.
// There is deliberately no hardcoded fallback for any field -- a past migration left three
// commands silently targeting a decommissioned VPS because their hardcoded defaults were
// never updated (#1015). Missing fields produce one combined, actionable error rather than
// silently deploying to the wrong place.
func ResolveDeployTarget(flagUser, flagHost, flagIdentity string) (DeployTarget, error) {
	cfg, err := loadOpsConfigFile()
	if err != nil {
		return DeployTarget{}, err
	}

	target := DeployTarget{
		User:         flagUser,
		Host:         flagHost,
		IdentityFile: flagIdentity,
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
func ResolveNginxTarget(flagDomains []string, flagPort string) (NginxTarget, error) {
	cfg, err := loadOpsConfigFile()
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

// parseTargetFlags parses the -i/-u/-s flags common to every lfr-tunnel-ops command that
// resolves a DeployTarget, using the standard library's flag.FlagSet rather than hand-rolled
// positional checks (the original `args[0] == "-i"` style required -i to be the very first
// argument and would have silently misparsed the moment a second flag like -u/-s was added
// in any order). name is used only for flag.FlagSet's own internal error-prefixing; callers
// print their own usage text on error, so the FlagSet's built-in usage output is discarded.
//
// Callers with a leading positional argument of their own (e.g. maintenance's
// enable/disable action) must slice that off args before calling this -- flag.Parse stops
// at the first non-flag argument, so a positional arg has to come first either way.
func parseTargetFlags(name string, args []string) (identityFile, user, host string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	i := fs.String("i", "", "path to SSH private key file")
	u := fs.String("u", "", "SSH username on the central VPS")
	s := fs.String("s", "", "SSH host (IP or hostname) of the central VPS")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	return *i, *u, *s, nil
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
