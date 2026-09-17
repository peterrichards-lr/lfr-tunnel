package server

import "lfr-tunnel/pkg/db"

// The per-tunnel rate limit a registration is actually granted (#2005).
//
// It was resolved by two identical blocks 4,700 lines apart -- one in handleRegister, for a
// client registering directly on this gateway, and one in handleEdgeRegister, for central
// validating on an edge's behalf. Both read the requested value, clamped it to the user's own
// limit, then clamped that to the fleet ceiling, in that order.
//
// The two are not equally easy to remember. Edge-served registration is the NORMAL case since
// #1947 stopped the incumbent gateway winning its own latency election, so a change made to one
// block lands on whichever half of the fleet a client happens to be routed to -- and it is
// invisible, because both paths still register successfully and only the granted number differs.
// That is the #1750 -> #1757 -> #1767 shape: fix one direction, leave the others alive.

// effectiveTunnelRateLimit resolves the rate limit a registration is granted, in requests per
// second, where 0 means unlimited.
//
// Two clamps, in this order, and the order is the policy:
//
//  1. the user's own limit, when their record sets one, wins over a larger or unset request --
//     an operator capping one user must not be undone by that user asking for more;
//  2. the fleet ceiling (max_tunnel_rate_limit), when configured, wins over both -- it is the
//     operator's whole-deployment bound and nothing below it may exceed it.
//
// A requested value of 0 means "no preference" on the way in and "unlimited" on the way out,
// which is why each clamp treats <= 0 as "take the limit" rather than as a floor. With neither
// a user limit nor a ceiling configured, 0 stays 0 and the tunnel is unlimited -- the default.
//
// userRec may be nil: a registration whose user record could not be re-read still gets the
// fleet ceiling applied, because the ceiling is the operator's, not the user's.
func (s *Server) effectiveTunnelRateLimit(requested int, userRec *db.User) int {
	effective := requested
	if userRec != nil && userRec.RateLimit > 0 {
		if effective <= 0 || effective > userRec.RateLimit {
			effective = userRec.RateLimit
		}
	}
	if s.cfg.MaxTunnelRateLimit > 0 {
		if effective <= 0 || effective > s.cfg.MaxTunnelRateLimit {
			effective = s.cfg.MaxTunnelRateLimit
		}
	} else if effective <= 0 {
		// Explicit rather than implied: "no preference" and "unlimited" are the same value on
		// the way out, and saying so here is what stops a later reader reading the branch above
		// as the only way to reach 0.
		effective = 0
	}
	return effective
}
