package server

import (
	"fmt"
	"log/slog"
	"net"
	"time"
)

// Blacklist enforcement, shared by the control plane and every edge (#1353).
//
// The cache holds an expiry rather than a bare "banned" flag. A zero time means the ban never
// expires, which is what a manual ban by an admin is; anything else is an automatic ban from
// the rate limiter, which lifts on its own.
//
// The expiry has to live in the cache rather than only in the database, because the check runs
// on every single request ahead of all routing. Consulting the database there to find out
// whether a ban had lapsed would put a query on the hot path to answer a question that is
// almost always "no".

// cacheBan records a ban in the in-memory cache. A nil expiry means it never expires.
func (s *Server) cacheBan(ip string, expiresAt *time.Time) {
	if expiresAt == nil {
		s.blacklist.Store(ip, time.Time{})
		return
	}
	s.blacklist.Store(ip, *expiresAt)
}

// isBlacklisted reports whether this address is blocked right now.
//
// An expired entry is dropped from the cache as it is found. The database row is deliberately
// left alone: ban_count lives there and drives the escalating ban time, so deleting it on
// expiry would let a repeat offender start from zero every time.
func (s *Server) isBlacklisted(ip string) bool {
	val, found := s.blacklist.Load(ip)
	if !found {
		return false
	}

	expiry, ok := val.(time.Time)
	if !ok {
		// A value written before the cache carried expiries. Treat it as permanent, which is
		// what it was.
		return true
	}
	if expiry.IsZero() {
		return true
	}
	if time.Now().Before(expiry) {
		return true
	}

	s.blacklist.Delete(ip)
	return false
}

// autoBanExempt reports whether an address must never be auto-banned -- fail2ban's ignoreip.
//
// Loopback is exempt by default. The blacklist is enforced ahead of all routing, so a banned
// address cannot reach the admin API to lift its own ban; locking an operator out of their own
// gateway is a worse outcome than a missed ban.
func (s *Server) autoBanExempt(ip string) bool {
	if s.cfg == nil {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		// An address that will not parse cannot be matched against a CIDR. Banning it is still
		// safe -- it can only have come from a header this gateway did not generate.
		return false
	}
	for _, entry := range s.cfg.AutoBan.Ignore {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			// A malformed entry is ignored rather than fatal: the cost is a missed exemption,
			// whereas refusing to start over a typo in an allow-list would be worse.
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// applyAutoBan bans an address for a bounded time, escalating for repeat offenders, and returns
// the expiry it settled on (nil when the ban does not expire).
//
// Escalation is what makes a finite first ban safe rather than lenient: someone who comes
// straight back is held for twice as long, and again after that, up to the configured cap.
func (s *Server) applyAutoBan(ip, reason string) (*time.Time, bool) {
	if s.autoBanExempt(ip) {
		slog.Info(fmt.Sprintf("[Defense] Not auto-banning %s: it is in the auto_ban ignore list", ip))
		return nil, false
	}

	cfg := s.autoBanConfig()

	var expiresAt *time.Time
	if s.db != nil {
		entry, err := s.db.AddAutoBan(ip, reason, cfg.Duration, cfg.escalationFactor(), cfg.MaxDuration)
		if err != nil {
			slog.Info(fmt.Sprintf("[Defense] Failed to record the ban for %s: %v", ip, err))
			// Still ban in memory. Failing to persist should not mean failing to defend, and
			// the cache is what the request path actually reads.
		} else {
			expiresAt = entry.ExpiresAt
		}
	} else if cfg.Duration > 0 {
		// No database, so no ban history to escalate from; a flat ban is all that is available.
		expiry := time.Now().UTC().Add(cfg.Duration)
		expiresAt = &expiry
	}

	s.cacheBan(ip, expiresAt)
	return expiresAt, true
}

// autoBanConfig returns the effective settings, tolerating a Server built without a config --
// several tests construct one directly.
func (s *Server) autoBanConfig() autoBanSettings {
	if s.cfg == nil {
		return autoBanSettings{}
	}
	return autoBanSettings{
		Duration:    s.cfg.AutoBan.Duration,
		Increment:   s.cfg.AutoBan.Increment,
		Factor:      s.cfg.AutoBan.Factor,
		MaxDuration: s.cfg.AutoBan.MaxDuration,
	}
}

type autoBanSettings struct {
	Duration    time.Duration
	Increment   bool
	Factor      float64
	MaxDuration time.Duration
}

// escalationFactor collapses "escalation is off" and "the factor is meaningless" into the same
// value, so the database layer only has to understand one rule: a factor of 1 or less does not
// escalate.
func (c autoBanSettings) escalationFactor() float64 {
	if !c.Increment || c.Factor <= 1 {
		return 1
	}
	return c.Factor
}

// describeBanDuration renders an expiry for an operator-facing message.
func describeBanDuration(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "until manually removed"
	}
	return fmt.Sprintf("until %s", expiresAt.UTC().Format(time.RFC3339))
}
