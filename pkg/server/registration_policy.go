package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"lfr-tunnel/pkg/db"
)

// Per-registration policy, resolved in one place rather than once per registration path (#2018).
//
// This is the same class #2005 named and #2017 fixed the first instance of: a decision about a
// single registration, computed independently by handleRegister (a client registering directly on
// this gateway) and handleEdgeRegister (central validating on an edge's behalf), thousands of
// lines apart in the same file. Edge-served registration has been the NORMAL case since #1947, so
// a change made to one block lands on whichever half of the fleet a client happens to be routed
// to, and nothing reports the divergence -- both paths still register successfully.
//
// The max-active-tunnels resolution is the sharper of the two, because it decides a REFUSAL: a
// divergence there is not a wrong number on a dashboard, it is a user who can open a tunnel from
// one region and not from another.
//
// What is NOT here, deliberately: the tunnel COUNTING around maxActiveTunnelsFor. The two paths
// count different things and are right to -- central has to add s.edgeLeases for tunnels it is
// not itself serving, and the direct path has no such leases to add. Only the resolution of the
// limit was duplicated. See registration_policy_test.go for the property that holds both halves
// together.

// Refusal messages. Named constants rather than inline literals because each is now written from
// exactly one place, and a caller that renders it differently (RegisterResponse on the direct
// path, a bare JSON map on the edge path) must still be rendering the same words.
const (
	activeTunnelLimitFormat      = "Active tunnels concurrency limit reached (%d). Stop another active tunnel or ask an administrator to increase your limit."
	subdomainQuarantinedMessage  = "Subdomain is currently in quarantine"
	subdomainReservedByOther     = "Subdomain is reserved by another user"
	subdomainMustBeReserved      = "Custom subdomains must be reserved in the portal prior to connecting"
	subdomainQuotaReachedMessage = "Subdomain reservation quota limit reached"
)

// maxActiveTunnelsFor resolves how many concurrent tunnels this registration's user is allowed,
// where 0 (or less) means unlimited.
//
// Two steps, and the order is the policy:
//
//  1. the role default -- admin and owner may each carry their own ceiling, and fall back to
//     default_max_active_tunnels when they do not;
//  2. the user's own override, when their record sets one, wins outright over the role default,
//     in both directions. An operator raising one user above their role's ceiling and an
//     operator capping one user below it are the same mechanism, so this is a replacement and
//     not a clamp.
//
// user is the authenticated identity and carries the role; userRec is the same user re-read from
// the database and may be nil when that read failed or there is no database. A nil userRec
// therefore falls back to the role default rather than refusing -- the override is the user's,
// and an unavailable record is not evidence that one exists.
func (s *Server) maxActiveTunnelsFor(user *db.User, userRec *db.User) int {
	maxTunnels := s.cfg.DefaultMaxActiveTunnels
	if user != nil {
		if user.Role == "admin" && s.cfg.AdminMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.AdminMaxActiveTunnels
		} else if user.Role == "owner" && s.cfg.OwnerMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.OwnerMaxActiveTunnels
		}
	}
	if userRec != nil && userRec.MaxTunnels != nil {
		maxTunnels = *userRec.MaxTunnels
	}
	return maxTunnels
}

// activeTunnelLimitRefusal is the message both paths refuse with once the limit is reached. The
// number is in the text on purpose: "you have too many" is not actionable without it.
func activeTunnelLimitRefusal(maxTunnels int) string {
	return fmt.Sprintf(activeTunnelLimitFormat, maxTunnels)
}

// reservationStanding is where an existing reservation sits on the
// live -> expired-but-quarantined -> lapsed timeline.
//
// The quarantine window is what stops a subdomain that expired an hour ago from being claimed by
// a stranger while its former holder is still reconnecting to it. Shared by all THREE reservation
// sites, including the custom-domain one that otherwise has its own quota, its own expiry rule
// and its own wording.
type reservationStanding int

const (
	// reservationLive: unexpired, or permanent. Only its holder may register on it.
	reservationLive reservationStanding = iota
	// reservationQuarantined: expired, still inside subdomain_quarantine_days. Its former
	// holder may take it back; nobody else may take it at all.
	reservationQuarantined
	// reservationLapsed: expired and past quarantine. Reclaimable by anyone, and the stale row
	// is deleted on the way past.
	reservationLapsed
)

// standingOf classifies an existing reservation. Written against the row rather than against a
// bare *time.Time so the three-way answer is impossible to collapse back into a bool at a call
// site -- "not live" and "free to take" are different questions, and conflating them is what a
// quarantine window exists to prevent.
func (s *Server) standingOf(existing *db.SubdomainReservation) reservationStanding {
	if existing == nil || existing.ExpiresAt == nil || !existing.ExpiresAt.Before(time.Now()) {
		return reservationLive
	}
	if time.Now().Before(existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)) {
		return reservationQuarantined
	}
	return reservationLapsed
}

// reservationRefusal is a decision to refuse, returned rather than written.
//
// The two registration paths do not write errors the same way: the direct path answers with a
// RegisterResponse the client parses, the edge path answers a bare JSON map that the edge relays
// verbatim. Returning the decision keeps the POLICY shared while leaving each caller its own
// wire format -- which is the part that legitimately differs.
type reservationRefusal struct {
	Status  int
	Message string
}

// resolveSubdomainReservations decides which of domains must be auto-reserved for subdomain
// before this registration can proceed, or refuses.
//
// Returns the domains needing a reservation and a nil refusal when the registration may go ahead;
// an empty list and a non-nil refusal otherwise. Only ever called with s.db non-nil.
func (s *Server) resolveSubdomainReservations(subdomain string, domains []string, user *db.User, userRec *db.User) ([]string, *reservationRefusal) {
	var domainsToReserve []string
	for _, d := range domains {
		existing, err := s.db.GetSubdomainReservationByName(subdomain, d)
		if err != nil || existing == nil {
			// No reservation exists. Whether the client may create one on the spot is the
			// operator's call, per role.
			if !s.canUserAutoReserve(userRec) {
				return nil, &reservationRefusal{Status: http.StatusForbidden, Message: subdomainMustBeReserved}
			}
			domainsToReserve = append(domainsToReserve, d)
			continue
		}

		switch s.standingOf(existing) {
		case reservationQuarantined:
			if existing.UserID != user.ID {
				return nil, &reservationRefusal{Status: http.StatusConflict, Message: subdomainQuarantinedMessage}
			}
			// Quarantined but this user's own: extend/re-reserve it.
			domainsToReserve = append(domainsToReserve, d)
		case reservationLapsed:
			// Past quarantine: drop the stale row and re-reserve, whoever held it.
			if err := s.db.DeleteSubdomainReservation(existing.ID); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to delete lapsed reservation for %s on %s: %v", subdomain, d, err))
			}
			domainsToReserve = append(domainsToReserve, d)
		case reservationLive:
			if existing.UserID != user.ID {
				return nil, &reservationRefusal{Status: http.StatusConflict, Message: subdomainReservedByOther}
			}
		}
	}
	return domainsToReserve, nil
}

// createSubdomainReservations checks the user's reservation quota and then creates the
// reservations resolveSubdomainReservations asked for, or refuses.
//
// Only ever called with s.db non-nil. A no-op, and never a refusal, when there is nothing to
// reserve -- the quota bounds what a user may hold, not what they may register on.
func (s *Server) createSubdomainReservations(subdomain string, domainsToReserve []string, user *db.User, userRec *db.User) *reservationRefusal {
	if len(domainsToReserve) == 0 {
		return nil
	}

	limit := s.cfg.DefaultMaxReservations
	if userRec != nil {
		limit = s.getUserMaxReservations(userRec)
	}

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	activeCount := 0
	if err == nil {
		for _, res := range list {
			// Custom domain reservations (Subdomain == "") are tracked against their own,
			// separate and smaller quota (#1004) and must not count here.
			if res.Subdomain == "" {
				continue
			}
			if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
				activeCount++
			}
		}
	}

	if limit >= 0 && activeCount+len(domainsToReserve) > limit {
		return &reservationRefusal{Status: http.StatusForbidden, Message: subdomainQuotaReachedMessage}
	}

	for _, d := range domainsToReserve {
		// Delete any existing quarantined or expired reservation for this user first.
		if existing, err := s.db.GetSubdomainReservationByName(subdomain, d); err == nil && existing != nil {
			if err := s.db.DeleteSubdomainReservation(existing.ID); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to clear reservation before re-creating %s on %s: %v", subdomain, d, err))
			}
		}
		res := &db.SubdomainReservation{
			UserID:    user.ID,
			Subdomain: subdomain,
			Domain:    d,
			ExpiresAt: s.getUserSubdomainExpiry(user),
		}
		if err := s.db.CreateSubdomainReservation(res); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to auto-create reservation for %s on %s: %v", subdomain, d, err))
		}
	}
	return nil
}

// applySubdomainReservationPolicy is the whole per-registration reservation decision: resolve,
// then create. Both registration paths call exactly this, and differ only in how they render the
// refusal it returns.
func (s *Server) applySubdomainReservationPolicy(subdomain string, domains []string, user *db.User, userRec *db.User) *reservationRefusal {
	domainsToReserve, refusal := s.resolveSubdomainReservations(subdomain, domains, user, userRec)
	if refusal != nil {
		return refusal
	}
	return s.createSubdomainReservations(subdomain, domainsToReserve, user, userRec)
}
