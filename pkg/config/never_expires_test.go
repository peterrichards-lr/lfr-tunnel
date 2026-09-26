package config

import (
	"strings"
	"testing"
)

// never_expires is three independent operator policies, and the whole point of the setting is
// that a gateway grants permanence only where it has been told to (#2264).
//
// The cases that matter are not "does `allowed` allow". They are:
//   - the DEFAULT, because an upgrade that widens what a gateway grants is the failure this
//     setting exists to prevent, and a zero value that reads as permissive would be exactly that;
//   - INDEPENDENCE, because the owner asked for three settings and one struct with three fields
//     satisfies that only if changing one leaves the others alone;
//   - a mistyped value, because falling back to the default is how a policy silently stops being
//     the policy the operator believes is in force.

func TestTheDefaultGrantsNothingPermanently(t *testing.T) {
	cfg := DefaultServerConfig()

	for name, got := range map[string]NeverExpiresPolicy{
		"tokens":         cfg.NeverExpiresTokens(),
		"subdomains":     cfg.NeverExpiresSubdomains(),
		"custom_domains": cfg.NeverExpiresCustomDomains(),
	} {
		if got != NeverExpiresDisabled {
			t.Errorf("never_expires.%s defaults to %q; a fresh gateway must grant nothing permanently", name, got)
		}
		if got.Available() {
			t.Errorf("never_expires.%s defaults to a state that offers the option", name)
		}
	}
}

// An empty policy reaches the accessors from every ServerConfig built as a literal -- which is
// most of the test suite, and any caller that skips DefaultServerConfig. It must read as the
// documented default and not as a fourth, permissive state.
func TestAnUnsetPolicyReadsAsDisabled(t *testing.T) {
	cfg := &ServerConfig{}

	if cfg.NeverExpiresTokens().Available() ||
		cfg.NeverExpiresSubdomains().Available() ||
		cfg.NeverExpiresCustomDomains().Available() {
		t.Fatal("a ServerConfig that says nothing about never_expires offers permanence")
	}
	if got := cfg.NeverExpiresTokens().String(); got != "disabled" {
		t.Errorf("an unset policy renders as %q; it is advertised to both portals and the client, so it must name the state it is in", got)
	}
}

// A nil *ServerConfig reaches these through helpers that hold one. Denying is the only safe
// answer; panicking or permitting are both worse.
func TestANilConfigDeniesRatherThanPanics(t *testing.T) {
	var cfg *ServerConfig

	if cfg.NeverExpiresTokens().Available() ||
		cfg.NeverExpiresSubdomains().Available() ||
		cfg.NeverExpiresCustomDomains().Available() {
		t.Fatal("a nil config offers permanence")
	}
}

// The owner's requirement in one test: "They need to be separated so they can be set
// independently." Each policy is set alone and the other two must stay where they were.
func TestEachPolicyIsIndependentOfTheOtherTwo(t *testing.T) {
	cases := []struct {
		yaml  string
		check func(*ServerConfig) (set NeverExpiresPolicy, others []NeverExpiresPolicy)
		name  string
	}{
		{
			name: "tokens",
			yaml: "never_expires:\n  tokens: allowed\n",
			check: func(c *ServerConfig) (NeverExpiresPolicy, []NeverExpiresPolicy) {
				return c.NeverExpiresTokens(), []NeverExpiresPolicy{c.NeverExpiresSubdomains(), c.NeverExpiresCustomDomains()}
			},
		},
		{
			name: "subdomains",
			yaml: "never_expires:\n  subdomains: approval\n",
			check: func(c *ServerConfig) (NeverExpiresPolicy, []NeverExpiresPolicy) {
				return c.NeverExpiresSubdomains(), []NeverExpiresPolicy{c.NeverExpiresTokens(), c.NeverExpiresCustomDomains()}
			},
		},
		{
			name: "custom_domains",
			yaml: "never_expires:\n  custom_domains: allowed\n",
			check: func(c *ServerConfig) (NeverExpiresPolicy, []NeverExpiresPolicy) {
				return c.NeverExpiresCustomDomains(), []NeverExpiresPolicy{c.NeverExpiresTokens(), c.NeverExpiresSubdomains()}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\n"+tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			set, others := tc.check(cfg)
			if !set.Available() {
				t.Errorf("never_expires.%s was set in the file and did not take effect (got %q)", tc.name, set)
			}
			for _, other := range others {
				if other != NeverExpiresDisabled {
					t.Errorf("setting never_expires.%s moved another policy to %q; the three must be independent", tc.name, other)
				}
			}
		})
	}
}

// The Liferay gateway's own posture, as a single load: three different values at once, each
// landing where it was written. A per-field test passes on a struct that reads the same key three
// times; this does not.
func TestThreeDifferentPoliciesLoadTogether(t *testing.T) {
	cfg, err := LoadServerConfig(writeCfg(t, `domains:
  - example.com
never_expires:
  tokens: allowed
  subdomains: approval
  custom_domains: allowed
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !cfg.NeverExpiresTokens().GrantsImmediately() {
		t.Errorf("tokens: want an immediate grant, got %q", cfg.NeverExpiresTokens())
	}
	if !cfg.NeverExpiresSubdomains().RequiresApproval() {
		t.Errorf("subdomains: want approval, got %q", cfg.NeverExpiresSubdomains())
	}
	if cfg.NeverExpiresSubdomains().GrantsImmediately() {
		t.Error("subdomains: approval granted immediately, which skips the admin entirely")
	}
	if !cfg.NeverExpiresCustomDomains().GrantsImmediately() {
		t.Errorf("custom_domains: want an immediate grant, got %q", cfg.NeverExpiresCustomDomains())
	}
}

// FIRING. A value that is not one of the three must stop the gateway, not quietly become one.
func TestAMistypedPolicyRefusesToStart(t *testing.T) {
	_, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\nnever_expires:\n  tokens: allwoed\n"))
	if err == nil {
		t.Fatal("a mistyped policy was accepted; the operator would believe the setting is in force")
	}
	if !strings.Contains(err.Error(), "tokens") {
		t.Errorf("the error does not say WHICH policy is wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "allwoed") {
		t.Errorf("the error does not quote what was written: %v", err)
	}
	for _, valid := range []string{"disabled", "approval", "allowed"} {
		if !strings.Contains(err.Error(), valid) {
			t.Errorf("the error does not offer %q as an alternative: %v", valid, err)
		}
	}
}

// Every bad value, not just the first: an operator fixing a config file should need one restart.
func TestEveryBadPolicyIsReportedAtOnce(t *testing.T) {
	_, err := LoadServerConfig(writeCfg(t, `domains:
  - example.com
never_expires:
  tokens: sometimes
  subdomains: maybe
  custom_domains: allowed
`))
	if err == nil {
		t.Fatal("two bad values were accepted")
	}
	for _, want := range []string{"tokens", "sometimes", "subdomains", "maybe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error omits %q, so fixing the file takes two restarts: %v", want, err)
		}
	}
}

// CONTROL / anti-vacuity. If a valid config failed to load, every FIRING case above would pass
// for the wrong reason.
func TestEveryValidPolicyValueLoads(t *testing.T) {
	for _, value := range []string{"disabled", "approval", "allowed"} {
		cfg, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\nnever_expires:\n  tokens: "+value+"\n"))
		if err != nil {
			t.Fatalf("%q is a documented value and did not load: %v", value, err)
		}
		if got := cfg.NeverExpiresTokens().String(); got != value {
			t.Errorf("wrote %q, read back %q", value, got)
		}
	}

	// And a config with no never_expires block at all still loads -- the upgrade path for every
	// gateway that exists today.
	if _, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\n")); err != nil {
		t.Fatalf("a config predating never_expires must still load: %v", err)
	}
}

// Env overrides are held to the same three names as the file. An override that silently becomes
// a fourth state is the same defect as a mistyped key, reached by a different route.
func TestAnEnvOverrideIsHeldToTheSameThreeNames(t *testing.T) {
	t.Setenv("LFT_NEVER_EXPIRES_SUBDOMAINS", "approval")
	cfg, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\n"))
	if err != nil {
		t.Fatalf("a valid override did not load: %v", err)
	}
	if !cfg.NeverExpiresSubdomains().RequiresApproval() {
		t.Errorf("the override did not take effect: %q", cfg.NeverExpiresSubdomains())
	}
	if cfg.NeverExpiresTokens().Available() {
		t.Error("overriding subdomains moved the tokens policy")
	}

	t.Setenv("LFT_NEVER_EXPIRES_SUBDOMAINS", "yes-please")
	if _, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\n")); err == nil {
		t.Fatal("an env override naming a policy that does not exist was accepted")
	}
}

// An override must beat the file, or it is not an override.
func TestAnEnvOverrideBeatsTheFile(t *testing.T) {
	t.Setenv("LFT_NEVER_EXPIRES_TOKENS", "disabled")
	cfg, err := LoadServerConfig(writeCfg(t, "domains:\n  - example.com\nnever_expires:\n  tokens: allowed\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NeverExpiresTokens().Available() {
		t.Error("the file's `allowed` beat an env override of `disabled`")
	}
}

// A role whose subdomain_expiry_days is <= 0 is permanent without anybody asking. The server
// clamps and warns about it; this is the half that has to find them.
func TestRolesConfiguredPermanentFindsEveryRoleAndSortsThem(t *testing.T) {
	zero, negative, week := 0, -1, 7
	cfg := &ServerConfig{RoleSettings: map[string]RoleSetting{
		"owner":     {SubdomainExpiryDays: &zero},
		"admin":     {SubdomainExpiryDays: &negative},
		"developer": {SubdomainExpiryDays: &week},
		"guest":     {},
	}}

	got := cfg.RolesConfiguredPermanent()
	if len(got) != 2 || got[0] != "admin" || got[1] != "owner" {
		t.Errorf("want [admin owner] (sorted, so a boot log diffs against the last one), got %v", got)
	}
}

// CONTROL. A config where no role is permanent must report none, or the warning fires on every
// gateway and stops being read.
func TestRolesConfiguredPermanentIsEmptyWhenNoRoleIsPermanent(t *testing.T) {
	week := 7
	cfg := &ServerConfig{RoleSettings: map[string]RoleSetting{"developer": {SubdomainExpiryDays: &week}}}
	if got := cfg.RolesConfiguredPermanent(); len(got) != 0 {
		t.Errorf("want none, got %v", got)
	}
	if got := (&ServerConfig{}).RolesConfiguredPermanent(); len(got) != 0 {
		t.Errorf("a config with no role_settings reports %v", got)
	}
}
