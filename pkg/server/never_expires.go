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

// resolveReservationExpiry applies a never_expires policy to an expiry that has already been
// computed from role settings, and reports whether permanence was requested but not granted.
//
// The role-settings path is the door that does not go through any create handler: a role with
// subdomain_expiry_days <= 0 makes getUserSubdomainExpiry return nil, and every reservation that
// role creates is permanent without anyone asking for it. A policy that only guarded the explicit
// "never" option would leave that wide open and read as though it did not.
//
// fallbackDays is what such a reservation gets instead under `disabled`. Under `approval` the
// role setting stands: an operator writing it into the config file IS the approval, given in
// advance, and demanding a second one per reservation would make the setting unusable.
func resolveReservationExpiry(policy config.NeverExpiresPolicy, expiry *time.Time, fallbackDays int, now time.Time) (*time.Time, bool) {
	if expiry != nil {
		return expiry, false
	}
	if policy.Available() {
		return nil, false
	}
	if fallbackDays <= 0 {
		fallbackDays = defaultSubdomainExpiryDays
	}
	t := now.AddDate(0, 0, fallbackDays)
	return &t, true
}

// defaultSubdomainExpiryDays is the fallback both getUserSubdomainExpiry implementations already
// use when no role setting applies. Named here so resolveReservationExpiry cannot drift from
// them.
const defaultSubdomainExpiryDays = 7

// resolveCustomDomainExpiry decides what a newly reserved custom domain expires at.
//
// Custom domains were unconditionally permanent (#1009), for a reason that still holds: the
// expiry model exists to reclaim a contested shared namespace, and a custom domain is not in one
// -- nobody else can ever claim it, because the holder owns it externally through DNS. That
// reasoning is now the operator's to accept rather than the code's to assume, which is why the
// default is `disabled` and a gateway that wants the old behaviour says `allowed`.
func resolveCustomDomainExpiry(policy config.NeverExpiresPolicy, fallback *time.Time, now time.Time) *time.Time {
	// GrantsImmediately, not Available: under `approval` the domain is reserved with an ordinary
	// expiry and it is the admin's grant that removes it. Available() here would have made
	// `approval` identical to `allowed` for the one resource that used to be permanent
	// unconditionally -- the exact state this setting exists to make deliberate.
	if policy.GrantsImmediately() {
		return nil
	}
	if fallback != nil {
		return fallback
	}
	t := now.AddDate(0, 0, defaultSubdomainExpiryDays)
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
