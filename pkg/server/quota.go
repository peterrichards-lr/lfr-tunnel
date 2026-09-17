package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"lfr-tunnel/pkg/db"
)

// Cumulative per-user bandwidth quota (#1959).
//
// The per-tunnel RateLimit this repo already had caps an INSTANTANEOUS rate: it stops one
// tunnel saturating a node right now, and it is enforced wherever the proxying happens,
// including on an edge. It says nothing at all about how much a user may move over a day or a
// month, so a user held to a reasonable rate could still transfer without limit and nothing
// noticed. This is the other half.
//
// Shape, deliberately the same one RateLimit already uses rather than a second one: the
// decision is made centrally, the effect is carried on the lease, and the enforcement happens
// in the proxy. Edges hold no database, so central is the counting authority -- which is what
// #1970 (edge byte reporting) and #1980 (noticing when an edge stops reporting) exist to make
// reliable. A quota is only as good as the feed it is sized from.
//
// THE ENFORCED MEASURE IS THE TOTAL, bytes_in + bytes_out. Egress is the AWS invoice and the
// obvious thing to enforce on, but the title of the issue is "so no one user can take the
// system's resources" -- fairness as well as cost -- and a cap on egress alone can be walked
// straight past by a user pulling heavily inbound. So: one enforced number (the total), two
// reported ones (the total and the egress beside it), everywhere the quota is surfaced. Do
// not "optimise" this to egress-only; that reopens the gap on purpose.
//
// THE PERIOD IS THE CALENDAR MONTH, UTC. It resets at 00:00 UTC on the first of the month,
// and a user who has been stopped gets their tunnels back then. Chosen over a rolling window
// for one reason that matters more than precision: a user can be told when their allowance
// comes back. A rolling 30-day window never resets -- it dribbles capacity back as old rows
// age out -- so "when do I get my demo back" has no answer anyone can state at a support desk.
// The calendar month is also the unit the invoice being defended against is cut on.
//
// ENFORCEMENT LAGS BY ONE REPORTING INTERVAL, and that lag and the quota's granularity are
// the same decision. Traffic becomes visible to this enforcer only once it has been recorded
// in tunnel_metrics: at most defaultEdgeMetricsInterval (30s) behind for an edge-served
// session, and at most metricsCollectorInterval (5 min) behind for a central-served one,
// because that is central's own sweep. The sweep below then runs every quotaSweepInterval.
// So a user can overshoot by roughly (their peak rate x 5 minutes) before anything fires.
// That is accepted rather than engineered away: making it tighter means recording bytes more
// often, and the write amplification of a per-request quota check on a SQLite file with a
// single writer is a worse problem than a few minutes of overshoot on a demo tool.
//
// DURING A CONTROL-CHANNEL PARTITION THIS FAILS OPEN, chosen rather than inherited. An edge
// serves from its in-memory lease, so a user who goes over while their edge is disconnected
// keeps working at full speed until the channel returns; and if the database read fails, the
// sweep changes nothing rather than assuming the worst. Both directions are deliberate: this
// is a demo tool, and turning a reporting outage into a fleet-wide outage -- or into a
// presentation cut off because central could not read a table -- is a worse failure than some
// unenforced bytes. The bill is bounded by the next successful sweep, which sees the traffic
// that happened during the partition, because the edge CARRIES its unreported deltas rather
// than discarding them (#1958).

// quotaState is where one user stands against their allowance.
type quotaState string

const (
	// quotaNormal is under the soft cap: nothing is applied.
	quotaNormal quotaState = "normal"
	// quotaThrottled is over the soft cap: the rate limit is dropped hard and the tunnel
	// stays up. Throttling first is deliberate -- this tool runs live demos, and killing
	// one mid-presentation is a worse failure than making it slow.
	quotaThrottled quotaState = "throttled"
	// quotaStopped is over the allowance: tunnels are terminated and new registrations are
	// refused until the period resets. Throttling alone is not enforcement, because a
	// throttled tunnel still transfers; this is the stage that actually bounds the bill.
	quotaStopped quotaState = "stopped"
)

// quotaSweepInterval is how often central re-reads usage and re-evaluates every user.
//
// A minute, against an edge reporting interval of 30 seconds, so an edge's report is acted on
// within two sweeps. Making it faster would not help: the figures it reads cannot be fresher
// than the feed that writes them.
const quotaSweepInterval = time.Minute

// alertKeyQuotaThrottled and alertKeyQuotaStopped are two events, not one with a severity.
// Throttled is informational -- someone is using a lot and has been slowed. Stopped is
// somebody's demo ending, which an owner may want to act on within minutes.
const (
	alertKeyQuotaThrottled = "alert_notify_quota_throttled"
	alertKeyQuotaStopped   = "alert_notify_quota_stopped"
)

// quotaUserStanding is one user's evaluated position, as the last sweep left it.
type quotaUserStanding struct {
	State quotaState
	// BytesTotal is the enforced measure; BytesOut is carried beside it because that is
	// the half that maps to the AWS invoice, and an admin looking at a throttled user
	// needs both -- how much of the allowance went, and how much of it costs money.
	BytesTotal int64
	BytesOut   int64
	Allowance  int64
	// Alerted is the furthest stage an admin has already been told about this period, so a
	// user who sits over their soft cap for a week produces one email rather than one per
	// sweep. It does not decay within a period: going back under the soft cap and over it
	// again is the same event, not a new one.
	Alerted quotaState
}

// quotaTracker holds the enforcer's view of the fleet between sweeps.
//
// In memory only, and rebuilt from tunnel_metrics on the first sweep after a restart, so
// there is nothing to persist and nothing to go stale. That also means a restart re-alerts,
// which is the right way round: a missed alert is worse than a repeated one.
type quotaTracker struct {
	mu sync.RWMutex
	// periodStart is the period the standings below were computed for. A sweep that finds
	// a different one wipes the map, which is what "the period resets" means mechanically.
	periodStart time.Time
	standings   map[string]quotaUserStanding
}

func newQuotaTracker() *quotaTracker {
	return &quotaTracker{standings: make(map[string]quotaUserStanding)}
}

// Standing reports one user's position, and whether the enforcer has any opinion at all.
//
// The second return is what makes the registration gate fail open: before the first sweep,
// and for a user nothing has been recorded against, there is no standing and nothing is
// refused.
func (t *quotaTracker) Standing(userID string) (quotaUserStanding, bool) {
	if t == nil {
		return quotaUserStanding{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.standings[userID]
	return st, ok
}

// Snapshot copies every standing, for the admin API.
func (t *quotaTracker) Snapshot() (time.Time, map[string]quotaUserStanding) {
	if t == nil {
		return time.Time{}, nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]quotaUserStanding, len(t.standings))
	for k, v := range t.standings {
		out[k] = v
	}
	return t.periodStart, out
}

// BeginPeriod points the tracker at a period, clearing everything if it has moved on.
// Reports whether a reset happened, so the caller can lift throttles that the new period has
// made obsolete.
func (t *quotaTracker) BeginPeriod(start time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.periodStart.Equal(start) {
		return false
	}
	previous := t.periodStart
	t.periodStart = start
	t.standings = make(map[string]quotaUserStanding)
	return !previous.IsZero()
}

// Record stores a user's evaluated standing and returns the state it replaced, so the caller
// can act on the transition rather than on the state.
func (t *quotaTracker) Record(userID string, st quotaUserStanding) quotaState {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous := t.standings[userID]
	if previous.State == "" {
		previous.State = quotaNormal
	}
	// The alert latch survives the update: it is a property of the period, not of the
	// sweep that happened to raise it.
	if previous.Alerted != "" && st.Alerted == "" {
		st.Alerted = previous.Alerted
	}
	t.standings[userID] = st
	return previous.State
}

// MarkAlerted latches a stage as already reported, returning false if it was latched already.
func (t *quotaTracker) MarkAlerted(userID string, stage quotaState) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.standings[userID]
	if !ok {
		return false
	}
	if st.Alerted == stage || (st.Alerted == quotaStopped && stage == quotaThrottled) {
		return false
	}
	st.Alerted = stage
	t.standings[userID] = st
	return true
}

// quotaPeriodStart is the instant the current enforcement period began.
//
// The calendar month in UTC by default -- see the file header for why a boundary beats a
// rolling window here. A configured PeriodDays replaces it with fixed-length windows anchored
// at the Unix epoch, which is still a boundary (every window has a stated start and a stated
// end) rather than a rolling one; it exists so a test can drive a period roll without waiting
// for a month, and so a deployment can pick a shorter cycle.
func (s *Server) quotaPeriodStart(now time.Time) time.Time {
	now = now.UTC()
	if s.cfg != nil && s.cfg.BandwidthQuota.PeriodDays > 0 {
		window := time.Duration(s.cfg.BandwidthQuota.PeriodDays) * 24 * time.Hour
		return time.Unix(0, 0).UTC().Add(now.Sub(time.Unix(0, 0).UTC()) / window * window)
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// resolveBandwidthQuota is the three-level precedence, stated here once so it cannot be
// inferred from the order of if-statements somewhere else (#1959).
//
//	per-user override   (db.User.BandwidthQuotaBytes)     -- most specific, wins outright
//	per-role default    (RoleSetting.BandwidthQuotaBytes) -- an admin may reasonably get more
//	global default      (BandwidthQuota.DefaultBytes)     -- applies to everyone else
//
// The same shape MaxReservations has had since #1004, deliberately, rather than a parallel
// one. A nil at either of the first two levels means "says nothing" and falls through; a zero
// means "unlimited" and stops there, which is how an exemption is expressed at whichever
// level it is granted. A zero global default disables the quota for the fleet.
func (s *Server) resolveBandwidthQuota(user *db.User) int64 {
	if user != nil && user.BandwidthQuotaBytes != nil {
		return *user.BandwidthQuotaBytes
	}
	if s.cfg != nil && user != nil && s.cfg.RoleSettings != nil {
		if setting, ok := s.cfg.RoleSettings[user.Role]; ok && setting.BandwidthQuotaBytes != nil {
			return *setting.BandwidthQuotaBytes
		}
	}
	if s.cfg == nil {
		return 0
	}
	return s.cfg.BandwidthQuota.DefaultBytes
}

// quotaThrottlePercent is where the soft stage begins, guarded against a configured value
// that would collapse the staging.
//
// Anything outside 1..99 falls back to the default: at 100 the throttle and the stop fire on
// the same byte, which is the staged behaviour silently becoming unstaged, and at 0 every
// user with a quota is throttled from their first byte.
func (s *Server) quotaThrottlePercent() int {
	if s.cfg == nil {
		return defaultQuotaThrottlePercentFallback
	}
	p := s.cfg.BandwidthQuota.ThrottlePercent
	if p <= 0 || p >= 100 {
		return defaultQuotaThrottlePercentFallback
	}
	return p
}

// quotaThrottleRateLimit is the requests per second a throttled tunnel is held to.
func (s *Server) quotaThrottleRateLimit() int {
	if s.cfg == nil || s.cfg.BandwidthQuota.ThrottleRateLimit <= 0 {
		return defaultQuotaThrottleRateLimitFallback
	}
	return s.cfg.BandwidthQuota.ThrottleRateLimit
}

// The fallbacks a zero-valued config falls back to. Duplicated from pkg/config's defaults
// because a ServerConfig built by hand -- every test does -- never passes through
// DefaultServerConfig, and a zero there must not mean "throttle everyone immediately".
const (
	defaultQuotaThrottlePercentFallback   = 80
	defaultQuotaThrottleRateLimitFallback = 1
)

// quotaStateFor classifies usage against an allowance.
//
// Both comparisons are >=, not >: an allowance of exactly N bytes is used up at N, not at
// N+1. The zero allowance means unlimited and is checked first, so a fleet with the quota
// disabled cannot be classified into anything.
func quotaStateFor(total, allowance int64, throttlePercent int) quotaState {
	if allowance <= 0 {
		return quotaNormal
	}
	if total >= allowance {
		return quotaStopped
	}
	// Multiplied before dividing, deliberately. `allowance/100*pct` floors to zero for any
	// allowance under 100 bytes, which would put such a user over their soft cap on their
	// first byte -- a degenerate case a test with a realistic allowance never reaches.
	if total >= allowance*int64(throttlePercent)/100 {
		return quotaThrottled
	}
	return quotaNormal
}

// watchBandwidthQuotas re-evaluates the fleet on a ticker (#1959).
//
// A sweep rather than a check at the point bytes are recorded, for the same reason #1980 is a
// sweep: the quota has to be re-evaluated when the CONFIGURATION changes too -- an admin
// raising a user's allowance must lift the throttle without waiting for that user to send
// another byte, which a receipt-time hook would never do.
func (s *Server) watchBandwidthQuotas(ctx context.Context) {
	ticker := time.NewTicker(quotaSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepBandwidthQuotas(time.Now())
		}
	}
}

// sweepBandwidthQuotas is the body of the watch, separated so a test can drive it with an
// explicit clock instead of waiting on a ticker.
func (s *Server) sweepBandwidthQuotas(now time.Time) {
	if s.db == nil || s.quotas == nil {
		// An edge reaches neither: it has no database, and enforcement decisions are
		// central's to make. It receives the result over the control channel.
		return
	}

	periodStart := s.quotaPeriodStart(now)
	if rolled := s.quotas.BeginPeriod(periodStart); rolled {
		slog.Info(fmt.Sprintf("[Quota] Bandwidth period rolled over; every allowance resets from %s", periodStart.Format(time.RFC3339)))
		// Lift every throttle and stop before re-evaluating. Doing it here rather than
		// leaving it to the per-user transition below matters: a user who was stopped
		// has no lease left to transition, so nothing would ever tell the edges to
		// start accepting their registrations again.
		s.releaseAllQuotaEnforcement()
	}

	usage, err := s.db.BandwidthUsageByUserSince(periodStart)
	if err != nil {
		// Fail open, loudly. A reporting outage must not become a full outage, and a
		// silent one must not look like a fleet that is suddenly under quota.
		slog.Warn(fmt.Sprintf("[Quota] Could not read bandwidth usage; leaving every user's standing unchanged: %v", err))
		return
	}

	users, err := s.db.ListUsers()
	if err != nil {
		slog.Warn(fmt.Sprintf("[Quota] Could not list users; leaving every user's standing unchanged: %v", err))
		return
	}

	byUser := make(map[string]db.UserBandwidthUsage, len(usage))
	for _, u := range usage {
		byUser[u.UserID] = u
	}

	for _, user := range users {
		allowance := s.resolveBandwidthQuota(user)
		if allowance <= 0 {
			continue
		}
		used := byUser[user.ID]
		state := quotaStateFor(used.Total(), allowance, s.quotaThrottlePercent())
		previous := s.quotas.Record(user.ID, quotaUserStanding{
			State:      state,
			BytesTotal: used.Total(),
			BytesOut:   used.BytesOut,
			Allowance:  allowance,
		})
		if state == previous {
			continue
		}
		s.applyQuotaTransition(user, previous, state, used, allowance)
	}
}

// applyQuotaTransition acts on one user crossing a stage boundary.
func (s *Server) applyQuotaTransition(user *db.User, from, to quotaState, used db.UserBandwidthUsage, allowance int64) {
	switch to {
	case quotaThrottled:
		rate := s.quotaThrottleRateLimit()
		applied := s.registry.SetQuotaRateLimitForUser(user.ID, rate)
		s.broadcastQuotaEnforcement(user.ID, "throttle", rate)
		slog.Warn(fmt.Sprintf("[Quota] %s is over the soft cap (%s of %s, %s out); %d local lease(s) dropped to %d rps",
			user.Email, formatQuotaBytes(used.Total()), formatQuotaBytes(allowance), formatQuotaBytes(used.BytesOut), applied, rate))
		if s.quotas.MarkAlerted(user.ID, quotaThrottled) {
			s.sendAdminAlert(alertKeyQuotaThrottled,
				"LFR Tunnel Alert: User throttled by bandwidth quota",
				quotaAlertBody(user, used, allowance, quotaThrottled, rate))
		}
	case quotaStopped:
		for _, subdomain := range s.registry.LeaseSubdomainsForUser(user.ID) {
			s.registry.KickLease(subdomain)
		}
		s.broadcastQuotaEnforcement(user.ID, "stop", 0)
		slog.Warn(fmt.Sprintf("[Quota] %s has used their whole allowance (%s of %s, %s out); tunnels terminated and registration refused until the period resets",
			user.Email, formatQuotaBytes(used.Total()), formatQuotaBytes(allowance), formatQuotaBytes(used.BytesOut)))
		if s.quotas.MarkAlerted(user.ID, quotaStopped) {
			s.sendAdminAlert(alertKeyQuotaStopped,
				"LFR Tunnel Alert: User stopped by bandwidth quota",
				quotaAlertBody(user, used, allowance, quotaStopped, 0))
		}
	case quotaNormal:
		// Reached when an administrator raises an allowance, which is the whole point of
		// the per-user override: a genuine need is accommodated by raising that user's
		// limit, and the throttle has to lift without waiting for a reconnect.
		restored := s.registry.ClearQuotaRateLimitForUser(user.ID)
		s.broadcastQuotaEnforcement(user.ID, "release", 0)
		slog.Info(fmt.Sprintf("[Quota] %s is back under their soft cap (%s of %s); %d local lease(s) restored",
			user.Email, formatQuotaBytes(used.Total()), formatQuotaBytes(allowance), restored))
	}
}

// releaseAllQuotaEnforcement lifts every throttle at a period boundary.
func (s *Server) releaseAllQuotaEnforcement() {
	if s.registry == nil {
		return
	}
	for _, lease := range s.registry.ListLeases() {
		s.registry.ClearQuotaRateLimitForUser(lease.UserID)
	}
	s.broadcastQuotaEnforcement("", "release", 0)
}

// quotaAlertBody is what an admin actually reads.
//
// Total AND egress, always both. The total is what was enforced; the egress is what the AWS
// invoice will show. An admin deciding whether to raise this user's allowance needs to know
// which of the two the usage was, and a body carrying only the enforced number cannot tell
// them.
func quotaAlertBody(user *db.User, used db.UserBandwidthUsage, allowance int64, stage quotaState, rate int) string {
	action := fmt.Sprintf("Their tunnels have been throttled to %d request(s) per second and remain up.", rate)
	if stage == quotaStopped {
		action = "Their tunnels have been terminated and new registrations are refused until the period resets."
	}
	return fmt.Sprintf(
		"%s (%s) has used %s of a %s bandwidth allowance this period.\n\n"+
			"  total (enforced): %s\n"+
			"  of which egress:  %s\n"+
			"  inbound:          %s\n\n"+
			"%s\n\n"+
			"The quota is enforced on the TOTAL, which is the fairness measure; egress is shown "+
			"beside it because that is the figure that maps to the AWS invoice.\n\n"+
			"To accommodate a genuine need, raise this user's own allowance from the admin users "+
			"screen -- a per-user override beats the role default and the fleet default.",
		user.Email, user.Role,
		formatQuotaBytes(used.Total()), formatQuotaBytes(allowance),
		formatQuotaBytes(used.Total()), formatQuotaBytes(used.BytesOut), formatQuotaBytes(used.BytesIn),
		action,
	)
}

// formatQuotaBytes renders a byte count for a human reading an email at 3am.
func formatQuotaBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// quotaRegistrationRefusal reports the message a registration should be refused with, or the
// empty string to let it through (#1959).
//
// This is what makes "stopped" enforcement rather than a one-off kick: without it, a client
// whose tunnel was terminated reconnects three seconds later and carries on. The refusal
// lasts until the period resets.
//
// FAILS OPEN in every uncertain case, and each one is a separate decision:
//
//   - no tracker or no standing yet (a gateway that has not swept, or a user nothing has
//     been recorded against) -- allow. A gateway that has just started must not refuse the
//     fleet for a minute because it has not read the table yet.
//   - a standing that says throttled or normal -- allow. Only the hard cap refuses.
//
// An edge never calls this: it has no database and does not decide. Central answers for it,
// on the validate path, which is the same place every other registration decision is made.
func (s *Server) quotaRegistrationRefusal(userID string) string {
	if s.quotas == nil || userID == "" {
		return ""
	}
	standing, known := s.quotas.Standing(userID)
	if !known || standing.State != quotaStopped {
		return ""
	}
	return fmt.Sprintf(
		"Bandwidth quota reached: %s of a %s allowance used this period (%s of it outbound). "+
			"Tunnels resume when the period resets, or when an administrator raises your allowance.",
		formatQuotaBytes(standing.BytesTotal), formatQuotaBytes(standing.Allowance), formatQuotaBytes(standing.BytesOut))
}

// quotaRateLimitFor reports the rate limit a newly registered tunnel must be held to, and
// whether a throttle applies at all.
//
// Applied AFTER registration rather than by clamping the granted limit, deliberately: the
// lease has to remember what it was granted (BaseRateLimit) so the throttle can be lifted
// when the allowance is raised. Clamping at registration would make the throttled value the
// lease's own baseline, and lifting it would restore the throttle.
func (s *Server) quotaRateLimitFor(userID string) (int, bool) {
	if s.quotas == nil || userID == "" {
		return 0, false
	}
	standing, known := s.quotas.Standing(userID)
	if !known || standing.State != quotaThrottled {
		return 0, false
	}
	return s.quotaThrottleRateLimit(), true
}

// applyQuotaToNewLeases re-applies a standing throttle to a tunnel that has just registered.
//
// Without this, a throttled user could shed the throttle by reconnecting -- the single most
// obvious way to defeat a limit, and one that costs nothing to try.
func (s *Server) applyQuotaToNewLeases(userID string) {
	rate, throttled := s.quotaRateLimitFor(userID)
	if !throttled {
		return
	}
	applied := s.registry.SetQuotaRateLimitForUser(userID, rate)
	slog.Info(fmt.Sprintf("[Quota] New tunnel for a throttled user: %d lease(s) held to %d rps", applied, rate))
}

// QuotaStanding is one user's bandwidth-quota position as the admin portals render it.
//
// UsedBytes is the ENFORCED measure (in + out) and UsedOutBytes is the egress within it.
// Both are sent, always, because they answer different questions: the first is how much of
// the allowance is gone, the second is how much of it costs money. An admin deciding whether
// to raise someone's allowance needs both, and a payload carrying only the enforced number
// makes the AWS invoice impossible to attribute from this screen.
type QuotaStanding struct {
	State           string    `json:"state"`
	AllowanceBytes  int64     `json:"allowance_bytes"`
	UsedBytes       int64     `json:"used_bytes"`
	UsedOutBytes    int64     `json:"used_out_bytes"`
	ThrottleAtBytes int64     `json:"throttle_at_bytes"`
	PeriodStart     time.Time `json:"period_start"`
	// Measured is false when no sweep has produced a figure for this user yet -- a gateway
	// that has just started, or an account with no recorded traffic this period. The
	// portals must distinguish that from a measured zero, because "we have not looked" and
	// "they used nothing" look identical otherwise, which is the failure this repo keeps
	// rediscovering (#1923, #1938, #1956).
	Measured bool `json:"measured"`
}

// quotaStandingFor builds the admin view of one user, resolving the allowance through the
// full three-level precedence even when nothing has been measured yet -- so the screen can
// show what the limit IS before it ever bites.
func (s *Server) quotaStandingFor(user *db.User) QuotaStanding {
	allowance := s.resolveBandwidthQuota(user)
	periodStart, standings := s.quotas.Snapshot()
	out := QuotaStanding{
		State:          string(quotaNormal),
		AllowanceBytes: allowance,
		PeriodStart:    periodStart,
	}
	if allowance > 0 {
		out.ThrottleAtBytes = allowance * int64(s.quotaThrottlePercent()) / 100
	}
	if user == nil {
		return out
	}
	if standing, ok := standings[user.ID]; ok {
		out.State = string(standing.State)
		out.UsedBytes = standing.BytesTotal
		out.UsedOutBytes = standing.BytesOut
		out.Measured = true
	}
	return out
}
