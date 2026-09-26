package server

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// One home for every question of the form "may this be permanent, and if not, what expiry does it
// get instead" (#2264).
//
// Written as free functions over a policy rather than methods on *Server, because the two things
// that create a Personal Access Token -- Server.handleCreateToken, which is routed, and
// portalService.CreateToken, which is the refactor's replacement for it and is not -- have
// different receivers. That split is exactly how the role gate came to exist in one of them and
// not the other: handleCreateToken refuses a non-admin, portalService.CreateToken accepts anyone,
// and nothing failed because only the routed one runs. A shared function both must call is the
// only arrangement where the next implementation cannot quietly omit the rule.

// defaultRequestedPATExpiryDays is what a Personal Access Token gets when the holder asked for
// "never" and the policy is `approval`: the token is real and usable immediately, at the shortest
// expiry either portal offers, and the permanence is what waits on an admin.
//
// 30 rather than a new config setting because the alternative to it is not "some other number",
// it is refusing to create the token at all -- and a user who asked for a long-lived token and
// got nothing has no working credential while they wait.
const defaultRequestedPATExpiryDays = 30

// resolvePATExpiry turns a requested lifetime in days into the expiry a token should be stored
// with, applying the operator's never_expires.tokens policy.
//
// days <= 0 is the wire's spelling of "never" -- both portal arms send 0 -- so it is the
// permanence request, not an invalid duration.
// requested is true when the caller asked for "never" and the policy turned that into a pending
// request rather than a grant or a refusal -- the one case where the token is created with an
// expiry the holder did not choose, and so the one case where something has to be recorded for
// an admin to act on (#2267).
func resolvePATExpiry(policy config.NeverExpiresPolicy, days int, now time.Time) (expiry *time.Time, requested bool, err error) {
	if days > 0 {
		t := now.AddDate(0, 0, days)
		return &t, false, nil
	}
	switch {
	case policy.GrantsImmediately():
		return nil, false, nil
	case policy.RequiresApproval():
		t := now.AddDate(0, 0, defaultRequestedPATExpiryDays)
		return &t, true, nil
	default:
		return nil, false, ErrPermanenceNotAllowed
	}
}

// defaultSubdomainExpiryDays is what a subdomain reservation gets when no role setting applies.
const defaultSubdomainExpiryDays = 7

// defaultCustomDomainExpiryDays is the same for a custom domain, and is deliberately not 7.
//
// A subdomain lives in a shared, contested namespace and a week is a reasonable hold on one. A
// custom domain is not in that namespace at all -- nobody else can ever claim a name its holder
// controls through DNS (#1009) -- and it costs its holder a DNS change and this gateway a
// certificate. It expires only because an operator asked for expiry at all, so the default is
// generous and `custom_domain_expiry_days` is there to tighten it.
const defaultCustomDomainExpiryDays = 90

// expiryInputs is what the config says about one reservation, before any policy is applied.
type expiryInputs struct {
	// PermanentByRole is set when the role's <kind>_expiry_days is 0 or less. That is the
	// door no create handler guards, because nobody asked for it: it is simply what the role
	// gets, and a policy that only checked the explicit "never" option would leave it open.
	PermanentByRole bool
	// Days is the lifetime to use when the reservation is not permanent.
	Days int
	// DefaultDays is this resource's own fallback, used when Days says nothing. Carried on
	// the inputs rather than chosen inside the resolver, so the resolver never has to know
	// which kind it is looking at -- which is the knowledge that kept getting lost.
	DefaultDays int
	// PermanentByDefault is the resource's behaviour when the role says nothing at all.
	//
	// The one real asymmetry between the two resources, and the reason this is a parameter
	// rather than two functions: a subdomain with no role setting expires, and a custom domain
	// with no role setting was unconditionally permanent before #2264. Writing that difference
	// down once is the point -- as two separate resolvers they drifted, and the drift was a
	// permanent custom domain on a gateway that forbids them (#2267 review).
	PermanentByDefault bool
}

// resolveExpiryUnderPolicy is the single place a reservation's expiry is decided.
//
// Two different policy questions are being asked here and they are not interchangeable:
//
//   - A role configured permanent is honoured under `approval` as well as `allowed`, because an
//     operator writing it into the config file IS the approval, given in advance. Demanding a
//     second one per reservation would make the setting unusable. Only `disabled` clamps it.
//   - A resource that is permanent BY DEFAULT is permanent only under `allowed`. Under
//     `approval` it is created with an ordinary expiry and an admin's grant is what removes it;
//     treating this case as Available() would make `approval` identical to `allowed` for the
//     one resource that used to be permanent unconditionally.
func resolveExpiryUnderPolicy(policy config.NeverExpiresPolicy, in expiryInputs, now time.Time) *time.Time {
	// The fallback belongs to the RESOURCE. This clamped to defaultSubdomainExpiryDays for
	// both kinds -- unreachable from expiryInputsFor today, and still the wrong contract on
	// the one function that now decides every expiry: it would hand a custom domain 7 days the
	// moment anyone routed a role value straight into Days (found reviewing #2276).
	days := in.Days
	if days <= 0 {
		days = in.DefaultDays
	}
	if days <= 0 {
		days = defaultSubdomainExpiryDays
	}

	if in.PermanentByRole {
		if policy.Available() {
			return nil
		}
		t := now.AddDate(0, 0, days)
		return &t
	}

	if in.PermanentByDefault && policy.GrantsImmediately() {
		return nil
	}

	t := now.AddDate(0, 0, days)
	return &t
}

// permanenceGrantAllowed reports whether an ADMIN may set an expiry of never on an existing
// resource -- approving a permanent extension, or extending a token by zero days.
//
// True under both `approval` and `allowed`, because `approval` is precisely the state in which an
// admin's grant is the mechanism. Only `disabled` refuses, and it refuses the admin too: a policy
// that any admin can step around is a preference, not a policy.
func permanenceGrantAllowed(policy config.NeverExpiresPolicy) bool {
	return policy.Available()
}

// logNeverExpiresPolicy states the three policies once at startup, and names any role whose
// subdomain_expiry_days contradicts the subdomain policy.
//
// The contradiction is logged rather than fatal. Refusing to boot would be the louder choice and
// the wrong one here: subdomain_expiry_days <= 0 was legal on every release before this, so a
// fleet-wide upgrade would take gateways down over a setting that was correct when it was
// written. Clamped and said out loud is the behaviour an operator can act on at their own pace.
func logNeverExpiresPolicy(cfg *config.ServerConfig) {
	if cfg == nil {
		return
	}
	slog.Info(fmt.Sprintf("[Config] never_expires: tokens=%s subdomains=%s custom_domains=%s",
		cfg.NeverExpiresTokens(), cfg.NeverExpiresSubdomains(), cfg.NeverExpiresCustomDomains()))

	if roles := cfg.RolesConfiguredPermanent(); len(roles) > 0 && !cfg.NeverExpiresSubdomains().Available() {
		slog.Warn(fmt.Sprintf("[Config] never_expires.subdomains is %q, but role_settings gives a "+
			"subdomain_expiry_days of 0 or less to: %s. Reservations for those roles will expire after "+
			"%d days instead of never. Set never_expires.subdomains to %q or %q to honour the role "+
			"setting, or give those roles a positive subdomain_expiry_days to stop this warning.",
			cfg.NeverExpiresSubdomains(), strings.Join(roles, ", "), defaultSubdomainExpiryDays,
			config.NeverExpiresApproval, config.NeverExpiresAllowed))
	}
}

// permanenceStateFor turns resolvePATExpiry's "a request was raised" into the state stored on the
// row, so the two callers that create tokens cannot spell it differently (#2267).
func permanenceStateFor(requested bool) string {
	if requested {
		return db.PATPermanencePending
	}
	return db.PATPermanenceNone
}

// resolveAdminGrantedExpiry is resolvePATExpiry for an action an ADMIN is taking directly.
//
// The difference is what `approval` means to each caller. To a holder it means "ask, and wait":
// the token is created with an ordinary expiry and a request goes to a queue. To an admin it
// means "you are the approval" -- there is nobody else to wait for, and the admin route through
// the permanence queue already grants outright, so the extend route has to agree or the same
// person gets two different answers to the same intent depending on which button they pressed.
//
// `disabled` still refuses. That is the state in which this gateway grants permanence by no
// route at all, and an admin is not an exception to it -- a rule any admin can step around is a
// preference (#2264).
func resolveAdminGrantedExpiry(policy config.NeverExpiresPolicy, days int, now time.Time) (*time.Time, error) {
	if days > 0 {
		t := now.AddDate(0, 0, days)
		return &t, nil
	}
	if !permanenceGrantAllowed(policy) {
		return nil, ErrPermanenceNotAllowed
	}
	return nil, nil
}

// policyForKind returns the never_expires policy that governs a reservation of this kind.
func policyForKind(cfg *config.ServerConfig, kind string) config.NeverExpiresPolicy {
	if kind == resourceKindCustomDomain {
		return cfg.NeverExpiresCustomDomains()
	}
	return cfg.NeverExpiresSubdomains()
}

// expiryInputsFor reads what the config says about a reservation of this kind for this user.
//
// A free function over *config.ServerConfig rather than a method, because BOTH holders of a
// config compute this -- *Server for the registration path the client uses, *portalService for
// the portal -- and they had two copies that disagreed. The duplication is the bug: the Server
// copy kept passing the SUBDOMAIN expiry in as a custom domain's fallback long after the portal
// copy stopped (#2264, #2267 review).
func expiryInputsFor(cfg *config.ServerConfig, kind string, user *db.User) expiryInputs {
	if kind == resourceKindCustomDomain {
		// PermanentByDefault describes the resource when the ROLE SAYS NOTHING. It is not a
		// property of custom domains in general, and setting it unconditionally made the new
		// key inert on exactly the gateway it was written for: under `allowed`,
		// resolveExpiryUnderPolicy returns permanent on PermanentByDefault before the
		// lifetime is ever read, so `custom_domain_expiry_days: 200` was silently ignored
		// (found reviewing #2276). An operator who names a number has said what they want.
		in := expiryInputs{Days: defaultCustomDomainExpiryDays, DefaultDays: defaultCustomDomainExpiryDays, PermanentByDefault: true}
		if cfg != nil && cfg.RoleSettings != nil {
			if rs, ok := cfg.RoleSettings[user.Role]; ok && rs.CustomDomainExpiryDays != nil {
				if *rs.CustomDomainExpiryDays <= 0 {
					in.PermanentByRole = true
					in.PermanentByDefault = false
				} else {
					in.Days = *rs.CustomDomainExpiryDays
					in.PermanentByDefault = false
				}
			}
		}
		return in
	}

	in := expiryInputs{Days: defaultSubdomainExpiryDays, DefaultDays: defaultSubdomainExpiryDays}
	if cfg == nil {
		return in
	}
	if cfg.RoleSettings != nil {
		if rs, ok := cfg.RoleSettings[user.Role]; ok && rs.SubdomainExpiryDays != nil {
			if *rs.SubdomainExpiryDays <= 0 {
				in.PermanentByRole = true
			} else {
				in.Days = *rs.SubdomainExpiryDays
			}
		}
		return in
	}
	// There used to be one more branch here: with NO role_settings block at all, an owner's
	// subdomains never expired. It is gone, deliberately.
	//
	// Only ONE of the two copies of this logic had it -- Server (the registration path) did,
	// portalService did not -- so an owner's subdomain was permanent when the client created
	// it and seven days when the portal did. Collapsing the copies forced a choice, and
	// keeping it would have WIDENED the portal: an owner reserving in the portal would go
	// from 7 days to permanent, and an admin pressing Demote on such a reservation would find
	// the button does nothing. That is a permanence route newly opened, which is the one thing
	// #2264 exists to prevent.
	//
	// Narrow, and near-dead in practice: DefaultServerConfig always populates RoleSettings, so
	// only a config that explicitly sets `role_settings:` to nothing reaches this path at all.
	// An operator who wants permanent owner subdomains says so with
	// `role_settings.owner.subdomain_expiry_days: 0`, which is governed by the policy like
	// every other route (#2276 review).
	return in
}

// reservationExpiry is the whole decision in one call: what the config says, run through the
// policy that governs this kind of resource.
func reservationExpiry(cfg *config.ServerConfig, kind string, user *db.User, now time.Time) *time.Time {
	return resolveExpiryUnderPolicy(policyForKind(cfg, kind), expiryInputsFor(cfg, kind, user), now)
}
