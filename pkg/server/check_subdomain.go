package server

import (
	"errors"
	"fmt"
	"strings"

	"lfr-tunnel/pkg/db"
)

// What /api/check-subdomain answers, and why it now answers the same question registration does
// (#2562).
//
// The endpoint had been reachable only through a hand-built curl until the client grew a
// `check-subdomain` subcommand, and putting it behind a command made three of its gaps matter:
//
//   - it consulted s.registry alone -- the leases THIS gateway serves. Edge-served registration
//     has been the normal case since #1947, and those leases live in s.edgeLeases, so a name held
//     by a tunnel on an edge was reported "available" by central. subdomainHeldInMemory is the
//     view registration uses; this is the same class #2020 closed for the random-name path.
//   - it never read the reservations table, so a name another user had reserved -- which
//     registration refuses with subdomainReservedByOther -- was reported "available" too.
//   - it ignored the documented `domain` parameter and answered for whichever single domain the
//     allocation rule picked for the request, without saying which. A caller asking about each
//     domain the gateway serves got the same answer every time, labelled however it liked.
//
// Each of those is an "available" that was really "not looked at" -- unknown reported as absent.

// errReservationsUnreadable means the reservations table could not be read, so whether the name
// is free is unknown. Answered 503, never "available".
var errReservationsUnreadable = errors.New("the gateway could not check subdomain reservations just now; retry in a moment")

// servedTunnelDomain returns the configured spelling of requested when it is a domain this
// gateway issues tunnels on, matched case-insensitively.
func (s *Server) servedTunnelDomain(requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	for _, d := range s.tunnelDomains() {
		if strings.EqualFold(d, requested) {
			return d, true
		}
	}
	return "", false
}

// subdomainAvailability reports whether userID could register subdomain on domain, and if not,
// why. An error means it could not be determined.
//
// The order mirrors registration: name rules and this gateway's leases (registry.CheckSubdomain,
// which also supplies the existing reasons), then edge leases, then reservations. The reservation
// rules are reservationStandingOf's: a live or quarantined reservation is the holder's alone, a
// lapsed one is anybody's. A user's own reservation does not make the name unavailable to them.
//
// It does NOT apply the per-user "custom subdomains must be reserved first" rule
// (canUserAutoReserve) or the reservation quota: those are about the asker, not the name, and
// answering "taken" for a name nobody holds would be its own kind of wrong answer.
func (s *Server) subdomainAvailability(subdomain, domain, userID string) (bool, string, error) {
	if available, reason := s.registry.CheckSubdomain(subdomain, []string{domain}); !available {
		return false, reason, nil
	}
	prefix := strings.ToLower(strings.TrimSpace(subdomain))
	if s.subdomainHeldInMemory(prefix, []string{domain}) {
		return false, "subdomain is already taken", nil
	}
	if s.db == nil {
		return true, "", nil
	}
	existing, err := s.db.GetSubdomainReservationByName(prefix, domain)
	if errors.Is(err, db.ErrNotFound) || (err == nil && existing == nil) {
		return true, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("%w: %v", errReservationsUnreadable, err)
	}
	if existing.UserID == userID {
		return true, "", nil
	}
	switch s.standingOf(existing) {
	case reservationLive:
		return false, subdomainReservedByOther, nil
	case reservationQuarantined:
		return false, subdomainQuarantinedMessage, nil
	default:
		return true, "", nil
	}
}

// availableSuggestions keeps only the suggestions subdomainAvailability agrees are free.
// Registry.GenerateSuggestions knows only this gateway's leases, so on its own it can suggest a
// name an edge tunnel or another user's reservation already holds. A suggestion that could not be
// checked is dropped rather than offered.
func (s *Server) availableSuggestions(candidates []string, domains []string, userID string) []string {
	var out []string
	for _, c := range candidates {
		free := true
		for _, d := range domains {
			available, _, err := s.subdomainAvailability(c, d, userID)
			if err != nil || !available {
				free = false
				break
			}
		}
		if free {
			out = append(out, c)
		}
	}
	return out
}
