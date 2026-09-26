package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// NeverExpiresPolicy says whether a resource may be granted permanently -- an expiry of never --
// and, if so, on whose say-so.
//
// Three resources can be permanent on this gateway: a Personal Access Token, a subdomain
// reservation and a custom domain. Before #2264 each answered the question differently and only
// one of the three had a server-side rule at all:
//
//   - A PAT with expires_at omitted was permanent for anybody who asked. Both portal arms hid the
//     option from non-admins and neither gate was worth anything, because CreateToken never
//     checked the role -- a request that skipped the portal skipped the policy with it.
//   - A subdomain reservation needed an admin to approve an extension, which is the behaviour
//     this type calls "approval".
//   - A custom domain was unconditionally permanent (#1009), with nothing to configure.
//
// The operator now sets each one independently, because they are not the same risk: a
// never-expiring credential is not a never-expiring DNS name, and an operator who wants one may
// well refuse the other.
type NeverExpiresPolicy string

const (
	// NeverExpiresDisabled refuses permanence by every route: the portals do not offer it, the
	// client does not offer it, and the server declines it however it arrives -- including via
	// an admin action and including via a role whose configured expiry is itself "never".
	//
	// The default, on purpose. An upgrade must not widen what a gateway grants, and an operator
	// who wants permanence should have said so.
	NeverExpiresDisabled NeverExpiresPolicy = "disabled"
	// NeverExpiresApproval offers permanence as a request. The resource is created with its
	// ordinary expiry and a request is raised against it; an admin's approval is what sets the
	// expiry to never. A role configured permanent in the config file stands under this policy:
	// writing it there IS an operator approving it, in advance.
	NeverExpiresApproval NeverExpiresPolicy = "approval"
	// NeverExpiresAllowed offers permanence plainly, to every role, granted on the spot.
	NeverExpiresAllowed NeverExpiresPolicy = "allowed"
)

// neverExpiresPolicies is the complete set, used to validate and to spell the alternatives in an
// error. Ordered from strictest to loosest so the message reads as a scale.
var neverExpiresPolicies = []NeverExpiresPolicy{
	NeverExpiresDisabled,
	NeverExpiresApproval,
	NeverExpiresAllowed,
}

// Available reports whether the option should appear at all -- in either portal arm, or anywhere
// the client offers a choice. False only under NeverExpiresDisabled.
func (p NeverExpiresPolicy) Available() bool {
	return p.resolve() != NeverExpiresDisabled
}

// GrantsImmediately reports whether asking for permanence is the same as receiving it.
func (p NeverExpiresPolicy) GrantsImmediately() bool {
	return p.resolve() == NeverExpiresAllowed
}

// RequiresApproval reports whether asking raises a request for an admin to act on.
func (p NeverExpiresPolicy) RequiresApproval() bool {
	return p.resolve() == NeverExpiresApproval
}

// resolve maps the empty string onto the default rather than onto a fourth, nameless state.
//
// The empty value reaches here from a ServerConfig built as a literal -- every test that
// constructs one, and any caller that does not go through DefaultServerConfig. Treating it as
// NeverExpiresDisabled means a config that says nothing grants nothing, which is the same answer
// the documented default gives; the alternative is a zero value that silently permits.
func (p NeverExpiresPolicy) resolve() NeverExpiresPolicy {
	if strings.TrimSpace(string(p)) == "" {
		return NeverExpiresDisabled
	}
	return NeverExpiresPolicy(strings.ToLower(strings.TrimSpace(string(p))))
}

// String renders the resolved value, so an empty policy reports as "disabled" wherever it is
// logged or advertised rather than as "".
func (p NeverExpiresPolicy) String() string {
	return string(p.resolve())
}

// validate rejects anything that is not one of the three. key names the setting, so the operator
// is told which of the three they mistyped.
//
// A hard error rather than a fall back to the default: the two differ in the direction that
// matters. An operator who writes `allwoed` and is quietly given `disabled` finds out when a user
// reports the option missing; one who writes it and is quietly given `allowed` never finds out at
// all. The gateway refuses to start either way, which is the only outcome that cannot be
// mistaken for the setting having worked.
func (p NeverExpiresPolicy) validate(key string) error {
	if strings.TrimSpace(string(p)) == "" {
		return nil
	}
	for _, valid := range neverExpiresPolicies {
		if p.resolve() == valid {
			return nil
		}
	}
	names := make([]string, 0, len(neverExpiresPolicies))
	for _, valid := range neverExpiresPolicies {
		names = append(names, string(valid))
	}
	return fmt.Errorf("never_expires.%s: %q is not a policy this gateway understands; use one of %s",
		key, string(p), strings.Join(names, ", "))
}

// NeverExpiresConfig is the `never_expires:` block. One policy per resource, independently set,
// because the three are separate decisions -- see the Liferay gateway, which allows a permanent
// custom domain outright, requires approval for a permanent subdomain, and allows a permanent
// token.
type NeverExpiresConfig struct {
	// Tokens governs a Personal Access Token created with no expiry.
	Tokens NeverExpiresPolicy `yaml:"tokens" json:"tokens"`
	// Subdomains governs a subdomain reservation with no expiry, however it is reached:
	// an extension approved as permanent, or a role whose subdomain_expiry_days is <= 0.
	Subdomains NeverExpiresPolicy `yaml:"subdomains" json:"subdomains"`
	// CustomDomains governs a custom-domain reservation with no expiry. Unconditionally
	// permanent before #2264, so a gateway that wants the old behaviour sets this to
	// "allowed" -- which is not the default, deliberately.
	CustomDomains NeverExpiresPolicy `yaml:"custom_domains" json:"custom_domains"`
}

// DefaultNeverExpiresConfig is the strict position: nothing may be granted permanently until the
// operator says which resource may be.
func DefaultNeverExpiresConfig() NeverExpiresConfig {
	return NeverExpiresConfig{
		Tokens:        NeverExpiresDisabled,
		Subdomains:    NeverExpiresDisabled,
		CustomDomains: NeverExpiresDisabled,
	}
}

// Validate checks all three, reporting every bad value rather than the first. An operator fixing
// a config file should need one restart, not three.
func (n NeverExpiresConfig) Validate() error {
	var problems []string
	for key, policy := range map[string]NeverExpiresPolicy{
		"tokens":         n.Tokens,
		"subdomains":     n.Subdomains,
		"custom_domains": n.CustomDomains,
	} {
		if err := policy.validate(key); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 {
		return nil
	}
	// Map iteration is unordered and an error message that reorders itself between runs is one
	// nobody can diff against a previous boot.
	sort.Strings(problems)
	return fmt.Errorf("%s", strings.Join(problems, "\n"))
}

// applyNeverExpiresEnv lets each policy be set out of band, matching how every other server
// setting can be. Validated with the file's values rather than separately, so an override is
// held to the same three names.
func applyNeverExpiresEnv(cfg *ServerConfig) {
	if val := os.Getenv("LFT_NEVER_EXPIRES_TOKENS"); val != "" {
		cfg.NeverExpires.Tokens = NeverExpiresPolicy(val)
	}
	if val := os.Getenv("LFT_NEVER_EXPIRES_SUBDOMAINS"); val != "" {
		cfg.NeverExpires.Subdomains = NeverExpiresPolicy(val)
	}
	if val := os.Getenv("LFT_NEVER_EXPIRES_CUSTOM_DOMAINS"); val != "" {
		cfg.NeverExpires.CustomDomains = NeverExpiresPolicy(val)
	}
}

// NeverExpiresTokens returns the resolved policy for Personal Access Tokens, safely on a nil
// receiver -- several server helpers hold a *ServerConfig that a test may not have set.
func (c *ServerConfig) NeverExpiresTokens() NeverExpiresPolicy {
	if c == nil {
		return NeverExpiresDisabled
	}
	return c.NeverExpires.Tokens.resolve()
}

// NeverExpiresSubdomains returns the resolved policy for subdomain reservations.
func (c *ServerConfig) NeverExpiresSubdomains() NeverExpiresPolicy {
	if c == nil {
		return NeverExpiresDisabled
	}
	return c.NeverExpires.Subdomains.resolve()
}

// NeverExpiresCustomDomains returns the resolved policy for custom domains.
func (c *ServerConfig) NeverExpiresCustomDomains() NeverExpiresPolicy {
	if c == nil {
		return NeverExpiresDisabled
	}
	return c.NeverExpires.CustomDomains.resolve()
}

// RolesConfiguredPermanent names every role whose subdomain_expiry_days grants permanence on its
// own (a value <= 0), which is the fourth door to a never-expiring reservation and the one that
// does not go through any of the three create paths.
//
// Returned sorted, and returned rather than acted on here, so the caller decides: the server logs
// it once at startup and clamps under `disabled`, which is louder than a silent clamp and less
// disruptive than refusing to boot a fleet gateway over a setting that was legal yesterday.
func (c *ServerConfig) RolesConfiguredPermanent() []string {
	if c == nil || c.RoleSettings == nil {
		return nil
	}
	var roles []string
	for role, rs := range c.RoleSettings {
		if rs.SubdomainExpiryDays != nil && *rs.SubdomainExpiryDays <= 0 {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}
