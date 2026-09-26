package server

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// ListReservations returns the reservations, the subdomain quota limit/used count, and the
// custom-domain quota limit/used count. Custom domains are SubdomainReservation rows with an
// empty Subdomain field (see CreateReservation/getUserMaxCustomDomains below) -- they're tracked
// against a separate, smaller quota, so the two counts must never be mixed.
func (s *portalService) ListReservations(user *db.User) ([]*db.SubdomainReservation, int, int, int, int, error) {
	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, 0, 0, 0, 0, ErrInternalError
	}

	usedCount := 0
	customDomainUsedCount := 0
	for _, res := range list {
		if res.Subdomain == "" {
			customDomainUsedCount++
		} else {
			usedCount++
		}
	}
	limit := s.getUserMaxReservations(user)
	customDomainLimit := s.getUserMaxCustomDomains(user)
	return list, limit, usedCount, customDomainLimit, customDomainUsedCount, nil
}

// CreateReservation validates and persists a new subdomain reservation.
func (s *portalService) CreateReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error) {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	domain = strings.ToLower(strings.TrimSpace(domain))

	if subdomain == "" || domain == "" {
		return nil, ErrInvalidRequest
	}

	if !isValidSubdomain(subdomain) {
		return nil, ErrInvalidRequest
	}

	domainSupported := false
	for _, d := range s.cfg.Domains {
		if strings.EqualFold(d, domain) {
			domainSupported = true
			break
		}
	}
	if !domainSupported {
		return nil, ErrInvalidRequest
	}

	limit := s.getUserMaxReservations(user)
	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}

	activeCount := 0
	for _, res := range list {
		// Custom domains (Subdomain == "") are tracked against their own, separate quota --
		// see getUserMaxCustomDomains -- and must not count against the plain subdomain limit.
		if res.Subdomain == "" {
			continue
		}
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		return nil, ErrQuotaReached
	}

	existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err == nil && existing != nil {
		if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
			quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
			if time.Now().Before(quarantineCutoff) {
				if existing.UserID != user.ID {
					return nil, ErrConflict
				}
				_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
			} else {
				_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
			}
		} else {
			return nil, ErrConflict
		}
	}

	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: subdomain,
		Domain:    domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.reserved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    fmt.Sprintf("Subdomain reserved. ExpiresAt: %v", expiry),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// CreateCustomDomain reserves a custom domain for a user from the portal (#2222).
//
// A SEPARATE method from CreateReservation, not a mode of it. The two validate opposite things:
// CreateReservation requires a subdomain and requires the domain to be one this gateway serves;
// this one requires no subdomain and requires the domain NOT to be one this gateway serves. They
// also differ on expiry, on which quota they count against, and on what a conflict means. Folding
// them together would put two policies behind one signature, which is the shape #2018 already
// rejected for the registration path's own copy of this decision.
//
// What it deliberately does NOT do is verify that the caller controls the domain. Pointing a
// CNAME at this gateway already requires authority over it, so the action is the proof; and a
// name nobody has pointed here can never be served anyway, because provisioning validates over
// ACME HTTP-01 and no certificate is ever issued. The residual risk is parking a name you cannot
// use, which releasing (self-service, and it tears down the vhost and certificate -- #1010)
// undoes.
func (s *portalService) CreateCustomDomain(user *db.User, domain, ip string) (*db.SubdomainReservation, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimSuffix(domain, ".")

	if domain == "" || !isValidCustomDomain(domain) {
		return nil, ErrInvalidRequest
	}
	if isUnderServedRootDomain(domain, s.cfg.Domains) {
		return nil, ErrInvalidRequest
	}

	// A failed delete FAILS THE REQUEST here, unlike CreateReservation's neighbouring copy which
	// ignores it. The stale row is keyed on this exact domain, so carrying on would create a
	// second row for one name -- and the lookup that decides who holds it returns one row. Better
	// to refuse and leave the old row standing, which is a state the user can act on.
	//
	// THIS DOMAIN's own standing is resolved BEFORE the quota, and the order is load-bearing.
	//
	// A holder re-submitting the form has changed nothing and consumes no additional quota, but
	// with the count taken first they are refused for a limit their own existing row fills --
	// "you already have one" reported as "you may not have one". Resolving the row first also
	// means a quarantined or lapsed row is deleted before the count, so the count is of what
	// will actually exist rather than of what is about to be replaced.
	//
	// The same standing rule the registration path applies, through the same function rather
	// than a second reading of ExpiresAt and the quarantine window.
	existing, err := s.db.GetSubdomainReservationByName("", domain)
	if err == nil && existing != nil {
		switch reservationStandingOf(existing, s.cfg.SubdomainQuarantineDays) {
		case reservationLive:
			// Already theirs is success, not a conflict: the portal form is the obvious place
			// to land twice.
			if existing.UserID == user.ID {
				return existing, nil
			}
			return nil, ErrConflict
		case reservationQuarantined:
			if existing.UserID != user.ID {
				return nil, ErrConflict
			}
			if derr := s.db.DeleteSubdomainReservation(existing.ID); derr != nil {
				return nil, ErrInternalError
			}
		case reservationLapsed:
			if derr := s.db.DeleteSubdomainReservation(existing.ID); derr != nil {
				return nil, ErrInternalError
			}
		}
	}

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}

	// Only rows with an EMPTY subdomain count here -- custom domains have their own, smaller
	// quota and must not be charged against the plain subdomain limit (#1004), exactly as
	// CreateReservation declines to charge them the other way round.
	limit := s.getUserMaxCustomDomains(user)
	activeCount := 0
	for _, res := range list {
		if res.Subdomain != "" {
			continue
		}
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}
	if limit >= 0 && activeCount >= limit {
		return nil, ErrQuotaReached
	}

	// Permanent only where the operator has said custom domains may be (#2264).
	//
	// This was an unconditional `ExpiresAt: nil`, matching what the registration path creates
	// (#1009), and the reasoning behind it is still good: the expiry/quarantine/extension model
	// exists to reclaim a shared, contested namespace, and nobody else can ever claim this exact
	// name because it belongs to the holder externally through DNS. What changed is who gets to
	// decide -- the argument is strong enough for an operator to accept, not strong enough for
	// the code to assume on their behalf. A gateway that wants the old behaviour sets
	// never_expires.custom_domains to "allowed"; the Liferay gateway does.
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: "",
		Domain:    domain,
		ExpiresAt: s.reservationExpiryForKind(resourceKindCustomDomain, user),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.db.CreateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	// Logged rather than suppressed, and deliberately NOT fatal: the reservation is already
	// committed, so failing the request here would tell the user it did not happen when it did.
	// The surrounding methods spell this as `_ =` with a //nolint, which the ratchet is at its
	// ceiling for -- and a silent audit gap is the one thing an audit log must not have.
	if aerr := s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "custom_domain.reserved",
		TargetType: "custom_domain",
		TargetID:   domain,
		Details:    "Custom domain reserved from the portal. Permanent; released explicitly.",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	}); aerr != nil {
		slog.Warn(fmt.Sprintf("[Portal] Reserved custom domain %s for %s but could not write the audit entry: %v",
			domain, user.Email, aerr))
	}

	return res, nil
}

// DeleteReservation removes a reservation securely. Returns the now-deleted reservation so
// callers can act on what kind of reservation it was -- in particular, the HTTP handler uses
// this to trigger the vanity domain hook's "remove" action for a custom domain (Subdomain ==
// ""), since that's now the only place explicit removal happens for one (#1010).
func (s *portalService) DeleteReservation(user *db.User, idStr, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return nil, ErrForbidden
	}

	if err := s.db.DeleteSubdomainReservation(id); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.released",
		TargetType: "subdomain",
		// A custom domain has no subdomain, so "%s.%s" recorded it as ".example.com" with a
		// leading dot. handleDeleteReservation already gets this right when it names the thing
		// in its own audit line; the two now agree (#2217-adjacent, fixed with #2222).
		TargetID:  releasedReservationName(res),
		Details:   "Subdomain reservation deleted / released by owner",
		IPAddress: ip,
		CreatedAt: time.Now(),
	})

	return res, nil
}

// RequestExtension marks a reservation for extension.
func (s *portalService) RequestExtension(user *db.User, idStr, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return nil, ErrForbidden
	}

	if res.ExpiresAt == nil {
		return nil, ErrInvalidRequest
	}

	res.ExtensionRequested = true
	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.extension_requested",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Extension requested for subdomain reservation",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	if s.sendAlert != nil {
		s.sendAlert("alert_notify_extension_requested", "LFR Tunnel Alert: Subdomain Extension Requested",
			fmt.Sprintf("User %s has requested an extension for subdomain %s.%s.", user.Email, res.Subdomain, res.Domain))
	}

	return res, nil
}

// PromoteReservation promotes a tunnel lease to a reservation.
func (s *portalService) PromoteReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error) {
	limit := s.getUserMaxReservations(user)

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}

	activeCount := 0
	for _, res := range list {
		// Custom domains are tracked against their own, separate quota (see
		// getUserMaxCustomDomains) and must not count against the plain subdomain limit.
		if res.Subdomain == "" {
			continue
		}
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		return nil, ErrQuotaReached
	}

	existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err == nil && existing != nil {
		if existing.ExpiresAt == nil || existing.ExpiresAt.After(time.Now()) {
			if existing.UserID != user.ID {
				return nil, ErrConflict
			}
			return existing, nil
		}
		_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
	}

	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: subdomain,
		Domain:    domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.promoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    "Subdomain promoted from active random lease to standard reservation",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// UpdateReservationAccessControl updates access controls for a reservation.

// missingAccessControlValue names what a mode claims and the reservation does not have, or ""
// when the two agree (#2156).
//
// Takes the values as they will be STORED -- a bcrypt hash or empty for the passcode -- because
// that is what the proxy reads. It never sees a plaintext passcode and must not: this answers
// "is one set", nothing more.
func missingAccessControlValue(accessMode, passcode, whitelistIPs string) string {
	hasPasscode := passcode != ""
	hasWhitelist := whitelistIPs != ""

	switch accessMode {
	case "and":
		switch {
		case !hasPasscode && !hasWhitelist:
			return "a passcode and an IP whitelist; neither is set"
		case !hasPasscode:
			return "a passcode; only the IP whitelist is set"
		case !hasWhitelist:
			return "an IP whitelist; only the passcode is set"
		}
	case "passcode":
		if !hasPasscode {
			return "a passcode; none is set"
		}
	case "whitelist":
		if !hasWhitelist {
			return "an IP whitelist; none is set"
		}
	}
	return ""
}

func (s *portalService) UpdateReservationAccessControl(user *db.User, subdomain, domain, accessMode, passcode, whitelistIPs, ip string) error {
	res, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrNotFound
		}
		return ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return ErrForbidden
	}

	switch passcode {
	case PasscodeMask:
		// The field was not touched. Leaving res.Passcode alone is the whole point of the mask:
		// the API hands out PasscodeMask rather than the stored bcrypt hash, so a form that
		// round-trips GET -> POST cannot re-hash it.
		//
		// It could: the portal pre-filled the input with the hash, so saving the dialog to
		// change the whitelist or the MODE silently stored HashPasscode(hash) and the passcode
		// the user chose stopped working -- with no error, and no way back to a value anyone
		// knows (#2103). Same treatment the client's auth token already gets (#1772).
	case "":
		res.Passcode = ""
	default:
		res.Passcode = HashPasscode(passcode)
	}
	res.WhitelistIPs = whitelistIPs
	// Stored as chosen. "or" remains the fallback for a caller that says nothing, which is the
	// historic behaviour, but an explicit public/passcode/whitelist must survive -- it is what
	// decides which factors the proxy applies (#2098).
	if accessMode != "" {
		res.AccessMode = accessMode
	} else {
		res.AccessMode = "or"
	}

	// A mode must not name a factor that is not there (#2156).
	//
	// The proxy decides what to enforce from whether each VALUE is non-empty, not from the
	// mode: `hasIPWhitelist := ipWhitelist != ""`, and the "and" branch then skips the
	// whitelist check entirely when there is no whitelist. So saving "and" with the IP field
	// empty stored the strictest label in the product against passcode-only enforcement, with
	// no error and nothing to see. Found in production on a reservation in exactly that state.
	//
	// Checked HERE, after the mask has been resolved above, so the question asked is "what will
	// this reservation hold once saved" and not "what did the request body contain".
	//
	// The difference matters in the UNSAFE direction. PasscodeMask is the non-empty string
	// "********", so a request carrying it looks like a passcode to anything inspecting the
	// body. For a reservation that has no stored passcode, the mask resolves to no passcode at
	// all -- and request-body validation would wave through "and" on a tunnel with one factor,
	// which is the exact state this function exists to refuse. Resolved state cannot lie about
	// it; the request can.
	//
	// "or" is deliberately not validated. An unset mode defaults to it a few lines above, so
	// every reservation nobody has configured is "or" with neither value -- rejecting that
	// would make untouched reservations unsaveable. "or" with one factor is also meaningful:
	// either satisfies it, so with one, that one is required.
	if missing := missingAccessControlValue(res.AccessMode, res.Passcode, res.WhitelistIPs); missing != "" {
		return fmt.Errorf("%w: access mode %s requires %s", ErrInvalidRequest, res.AccessMode, missing)
	}

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.access_control_updated",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    fmt.Sprintf("Access controls updated: Mode=%s, Passcode=[MASKED], IPs=%s", res.AccessMode, res.WhitelistIPs),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

// AdminListExtensions lists all reservations that have requested an extension.
//
// Each entry says which kind of resource it is and whether permanence is on the table for it
// (#2267). Both were previously left for the two portal arms to work out from `subdomain === ”`
// and from nothing respectively -- the first is how the queue came to call a custom domain a
// subdomain, and the second is how an admin came to be offered a "Permanent" button that, after
// #2264, a gateway set to `disabled` answers with a 403.
func (s *portalService) AdminListExtensions() ([]*ExtensionRequestView, error) {
	all, err := s.db.ListAllSubdomainReservations()
	if err != nil {
		return nil, ErrInternalError
	}

	list := make([]*ExtensionRequestView, 0)
	for _, res := range all {
		if !res.ExtensionRequested {
			continue
		}
		kind := reservationResourceKind(res)
		policy := s.cfg.NeverExpiresSubdomains()
		if kind == resourceKindCustomDomain {
			policy = s.cfg.NeverExpiresCustomDomains()
		}
		list = append(list, &ExtensionRequestView{
			SubdomainReservation: res,
			ResourceKind:         kind,
			PermanenceAllowed:    permanenceGrantAllowed(policy),
		})
	}

	return list, nil
}

// AdminApproveExtension approves an extension request.
func (s *portalService) AdminApproveExtension(actor, idStr string, days int, permanent bool, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	// An admin approving permanence is still subject to the operator's policy (#2264). A rule
	// any admin can step around is a preference, not a rule -- and `disabled` is the state in
	// which this gateway grants permanence by no route at all, which has to include this one.
	//
	// Which policy depends on what the row IS: a custom domain is a reservation with an empty
	// subdomain (#1004), and charging it against never_expires.subdomains would mean the two
	// settings the owner asked to be independent were not (#2267).
	if permanent {
		policy := s.cfg.NeverExpiresSubdomains()
		if reservationResourceKind(res) == resourceKindCustomDomain {
			policy = s.cfg.NeverExpiresCustomDomains()
		}
		if !permanenceGrantAllowed(policy) {
			return nil, ErrPermanenceNotAllowed
		}
	}

	res.ExtensionRequested = false
	if permanent {
		res.ExpiresAt = nil
	} else {
		baseTime := time.Now()
		if res.ExpiresAt != nil && res.ExpiresAt.After(time.Now()) {
			baseTime = *res.ExpiresAt
		}
		extended := baseTime.AddDate(0, 0, days)
		res.ExpiresAt = &extended
	}
	res.ExpiryWarningSent = 0

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    actor,
		Action:     "subdomain.extension_approved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    fmt.Sprintf("Extension approved. Permanent: %t, Days: %d", permanent, days),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// AdminDemoteReservation demotes a permanent reservation.
func (s *portalService) AdminDemoteReservation(actor, idStr, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	resOwner, err := s.db.GetUser(res.UserID)
	if err != nil {
		return nil, ErrInternalError
	}

	// By what the ROW is, not by which function is asking -- see reservationExpiryForKind.
	// This read s.getUserSubdomainExpiry unconditionally, so demoting a custom domain applied
	// never_expires.subdomains to it and could make it permanent on a gateway whose
	// custom-domain policy forbids that (#2267 review).
	res.ExpiresAt = s.reservationExpiryFor(res, resOwner)
	res.ExtensionRequested = false
	res.ExpiryWarningSent = 0

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    actor,
		Action:     "subdomain.demoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Subdomain demoted to standard temporary lease",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// getUserMaxReservations helper method matching the existing one in api.go
func (s *portalService) getUserMaxReservations(u *db.User) int {
	if s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[u.Role]; ok && rs.MaxReservations != nil {
			return *rs.MaxReservations
		}
	}
	if u.Role == "admin" && s.cfg.AdminMaxReservations != nil {
		return *s.cfg.AdminMaxReservations
	}
	if u.Role == "owner" && s.cfg.OwnerMaxReservations != nil {
		return *s.cfg.OwnerMaxReservations
	}
	return s.cfg.DefaultMaxReservations
}

// getUserMaxCustomDomains helper method matching the existing one in api.go. Custom domains are
// a separate, smaller quota from plain subdomain reservations -- see the comment on
// config.ServerConfig.DefaultMaxCustomDomains for why.
func (s *portalService) getUserMaxCustomDomains(u *db.User) int {
	if s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[u.Role]; ok && rs.MaxCustomDomains != nil {
			return *rs.MaxCustomDomains
		}
	}
	if u.Role == "admin" && s.cfg.AdminMaxCustomDomains != nil {
		return *s.cfg.AdminMaxCustomDomains
	}
	if u.Role == "owner" && s.cfg.OwnerMaxCustomDomains != nil {
		return *s.cfg.OwnerMaxCustomDomains
	}
	return s.cfg.DefaultMaxCustomDomains
}

// getUserSubdomainExpiry helper method
func (s *portalService) getUserSubdomainExpiry(u *db.User) *time.Time {
	return s.reservationExpiryForKind(resourceKindSubdomain, u)
}

// roleExpiry resolves what the ROLE says, with no policy applied.
//
// Split out so the policy can be chosen by the caller (#2267). A role whose subdomain_expiry_days
// is <= 0 gets permanence without anyone asking for it -- no create handler sees a request it
// could refuse, because there is no request -- which is why the policy has to be applied to this
// result rather than only where a portal offers a choice (#2264).
func (s *portalService) roleExpiry(u *db.User) (expiry *time.Time, days int) {
	days = defaultSubdomainExpiryDays
	permanentByRole := false
	if s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[u.Role]; ok && rs.SubdomainExpiryDays != nil {
			if *rs.SubdomainExpiryDays <= 0 {
				permanentByRole = true
			} else {
				days = *rs.SubdomainExpiryDays
			}
		}
	}
	if !permanentByRole {
		t := time.Now().AddDate(0, 0, days)
		expiry = &t
	}
	return expiry, days
}

// reservationExpiryForKind applies the policy that belongs to the KIND of resource named.
//
// The class rule, stated once because three functions in this file write a reservation's expiry
// and two of them used to get it right by accident: **the policy is chosen by what the row IS,
// not by which function is asking.** A custom domain is a subdomain reservation with an empty
// Subdomain (#1004), so any function that reaches for getUserSubdomainExpiry without looking at
// the row charges a custom domain to never_expires.subdomains -- and the three settings the
// owner asked to be independent are quietly not.
//
// AdminDemoteReservation was exactly that: the "Reject" button on the extension queue, which
// could hand a user a PERMANENT custom domain on a gateway whose custom-domain policy is
// `disabled`, because subdomains happened to be `allowed` and the holder's role was configured
// permanent. Found in review of #2267, in the same PR that fixed the other two.
func (s *portalService) reservationExpiryForKind(kind string, owner *db.User) *time.Time {
	if kind == resourceKindCustomDomain {
		// The role's subdomain_expiry_days is NOT consulted here, and that is the point.
		//
		// It is a subdomain setting, and letting it decide how long a custom domain lives is
		// the same misattribution as letting never_expires.subdomains decide whether one can
		// be permanent -- just in days rather than in yes/no. A gateway with
		// `developer.subdomain_expiry_days: 99` was handing out 99-day custom domains, which
		// nobody configured and nobody could turn off independently.
		//
		// There is no custom-domain equivalent of that setting today, so a custom domain that
		// may not be permanent gets the plain default. Naming the gap rather than papering
		// over it with the nearest-looking number: if operators want to tune this, it wants
		// its own key, not a borrowed one.
		return resolveCustomDomainExpiry(s.cfg.NeverExpiresCustomDomains(), nil, time.Now())
	}
	expiry, days := s.roleExpiry(owner)
	resolved, _ := resolveReservationExpiry(s.cfg.NeverExpiresSubdomains(), expiry, days, time.Now())
	return resolved
}

// reservationExpiryFor is reservationExpiryForKind for a row that already exists.
func (s *portalService) reservationExpiryFor(res *db.SubdomainReservation, owner *db.User) *time.Time {
	return s.reservationExpiryForKind(reservationResourceKind(res), owner)
}

// PasscodeMask stands in for a passcode that is set, wherever one would otherwise be sent to a
// client.
//
// The stored value is a bcrypt hash, and the reservations API used to return it verbatim. The
// portal put it straight into an editable text box, so the user saw `$2a$10$…` instead of what
// they typed -- and saving the dialog again submitted the hash as the new passcode, storing
// HashPasscode(hash). The passcode they chose stopped working and the replacement was a value
// nobody could type (#2103).
//
// Eight asterisks rather than a bespoke sentinel: the client's auth token already uses exactly
// this, so there is one convention to know rather than two (#1772).
const PasscodeMask = "********"

// MaskPasscode reports whether a passcode is set, without disclosing anything about it.
//
// Credential material has no reason to leave the server. A hash is not a plaintext passcode,
// but it is offline-crackable, and it was being handed to every caller who could list
// reservations.
func MaskPasscode(stored string) string {
	if stored == "" {
		return ""
	}
	return PasscodeMask
}

// releasedReservationName names a reservation for an audit entry, whichever kind it is.
//
// A custom domain's Subdomain is empty, so the obvious "%s.%s" produces ".example.com".
func releasedReservationName(res *db.SubdomainReservation) string {
	if res == nil {
		return ""
	}
	if res.Subdomain == "" {
		return res.Domain
	}
	return fmt.Sprintf("%s.%s", res.Subdomain, res.Domain)
}
