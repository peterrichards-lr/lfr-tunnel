package server

import (
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Tests for the cumulative per-user bandwidth quota (#1959).
//
// Every fixture here is built through a path the running gateway actually takes -- users via
// db.CreateUser, traffic via db.RecordTunnelMetric (which is what MetricsCollector and
// queueEdgeMetrics both call), leases via Registry.Register -- rather than by writing rows or
// structs by hand. A fixture describing a state production cannot produce passes against the
// bug it claims to guard, which this repo has now done often enough to be a rule.

// newQuotaTestServer is a control plane with a database, a registry and a quota tracker, which
// is the only configuration that enforces anything: an edge has no database and is told the
// outcome over the control channel.
func newQuotaTestServer(t *testing.T, quota int64) *Server {
	t.Helper()
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.com"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
		BandwidthQuota: config.BandwidthQuotaConfig{
			DefaultBytes:      quota,
			ThrottlePercent:   80,
			ThrottleRateLimit: 1,
		},
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "quota_test.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func createQuotaUser(t *testing.T, srv *Server, id, role string) *db.User {
	t.Helper()
	u := &db.User{ID: id, Email: id, Role: role, Status: db.UserStatusApproved}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	return u
}

// recordTraffic writes bytes the way the gateway does -- through RecordTunnelMetric, the same
// call MetricsCollector's tick and the edge report ingest both make.
func recordTraffic(t *testing.T, srv *Server, userID string, in, out int64) {
	t.Helper()
	if err := srv.db.RecordTunnelMetric(&db.TunnelMetric{
		UserID:          userID,
		SubdomainPrefix: "demo",
		FullHost:        "demo.example.com",
		BytesIn:         in,
		BytesOut:        out,
		ConnectedAt:     time.Now().UTC(),
		RecordedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to record traffic: %v", err)
	}
}

// TestQuotaEnforcesTotalNotEgress is the guard on the measure itself.
//
// The user below has moved 900 bytes inbound and 200 outbound against a 1000-byte allowance.
// Egress alone (200) is a fifth of the allowance and would not trip anything; the total
// (1100) is over it. Enforcing on egress is the plausible "optimisation" -- AWS charges for
// egress, so it looks like the cost driver -- and it leaves a user who pulls heavily inbound
// entirely unbounded, which is precisely the resource-fairness the issue's title asks for.
//
// If this assertion fails, exactly one thing caused it: the enforced measure stopped being
// the total.
func TestQuotaEnforcesTotalNotEgress(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	user := createQuotaUser(t, srv, "heavy@example.com", "user")

	recordTraffic(t, srv, user.ID, 900, 200)

	srv.sweepBandwidthQuotas(time.Now())

	standing, known := srv.quotas.Standing(user.ID)
	if !known {
		t.Fatal("the sweep produced no standing for a user with recorded traffic")
	}
	if standing.State != quotaStopped {
		t.Fatalf("enforced measure is wrong: 900 in + 200 out = 1100 against a 1000 allowance should be %q, got %q "+
			"(egress alone is 200, so a quota enforced on bytes_out would report %q here)",
			quotaStopped, standing.State, quotaNormal)
	}
	if standing.BytesTotal != 1100 {
		t.Fatalf("enforced total should be in+out = 1100, got %d", standing.BytesTotal)
	}
	// Egress is reported beside the total, not instead of it: it is what the AWS invoice
	// charges for, and an admin cannot attribute cost from the enforced number alone.
	if standing.BytesOut != 200 {
		t.Fatalf("egress should be reported separately as 200, got %d", standing.BytesOut)
	}
}

// TestQuotaAllowancePrecedence pins the three-level chain in the one configuration that can
// tell the levels apart: all three set, to three different values.
//
// A precedence that is only implied by the order of some if-statements inverts silently the
// next time someone reorders them. Each step below removes one level and asserts the next
// takes over, so a chain that resolves in the wrong direction fails on the FIRST case rather
// than looking correct because the values happened to agree.
func TestQuotaAllowancePrecedence(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	roleQuota := int64(2000)
	srv.cfg.RoleSettings = map[string]config.RoleSetting{
		"admin": {BandwidthQuotaBytes: &roleQuota},
	}

	userQuota := int64(3000)
	user := &db.User{ID: "a@example.com", Email: "a@example.com", Role: "admin", BandwidthQuotaBytes: &userQuota}

	if got := srv.resolveBandwidthQuota(user); got != 3000 {
		t.Fatalf("per-user override must win over the role default (2000) and the global default (1000); got %d", got)
	}

	user.BandwidthQuotaBytes = nil
	if got := srv.resolveBandwidthQuota(user); got != 2000 {
		t.Fatalf("with no per-user override the role default must win over the global default (1000); got %d", got)
	}

	user.Role = "user"
	if got := srv.resolveBandwidthQuota(user); got != 1000 {
		t.Fatalf("with neither override nor a matching role setting the global default applies; got %d", got)
	}

	// A zero at a level means "unlimited here", not "say nothing" -- that is how an
	// exemption is granted, and it must stop the chain rather than fall through.
	zero := int64(0)
	user.BandwidthQuotaBytes = &zero
	if got := srv.resolveBandwidthQuota(user); got != 0 {
		t.Fatalf("a per-user zero is an exemption and must stop the chain, got %d", got)
	}
}

// TestQuotaStagesThrottleThenStop is the staged enforcement itself, driven through the real
// sweep against real recorded traffic.
//
// Throttle first is the point: a live demo is made slow rather than killed. But a throttled
// tunnel still transfers, so the stop stage has to follow -- and this asserts both, in order,
// on the same user, plus that the throttle reaches the lease the proxy actually reads.
func TestQuotaStagesThrottleThenStop(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	user := createQuotaUser(t, srv, "demo@example.com", "user")

	if _, _, err := srv.registry.Register(user.ID, "demo", []PortMapping{{LocalPort: 8080}},
		[]string{"example.com"}, 100, "203.0.113.5", "", nil); err != nil {
		t.Fatalf("failed to register a tunnel: %v", err)
	}

	// 1. Under the soft cap: nothing happens.
	recordTraffic(t, srv, user.ID, 300, 100)
	srv.sweepBandwidthQuotas(time.Now())
	if standing, _ := srv.quotas.Standing(user.ID); standing.State != quotaNormal {
		t.Fatalf("400 bytes of a 1000 allowance is under the 80%% soft cap; want %q, got %q", quotaNormal, standing.State)
	}
	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 100 {
		t.Fatalf("a tunnel under its soft cap must keep the limit it was granted (100 rps), got %d", limit)
	}

	// 2. Over the soft cap: throttled, and the tunnel is still there.
	recordTraffic(t, srv, user.ID, 400, 100)
	srv.sweepBandwidthQuotas(time.Now())
	if standing, _ := srv.quotas.Standing(user.ID); standing.State != quotaThrottled {
		t.Fatalf("900 bytes of a 1000 allowance is over the 80%% soft cap and under the hard cap; want %q, got %q",
			quotaThrottled, standing.State)
	}
	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 1 {
		t.Fatalf("a throttled tunnel must have its rate limit dropped to the configured 1 rps, got %d", limit)
	}
	if !leaseExists(srv, "demo.example.com") {
		t.Fatal("throttling must NOT terminate the tunnel -- killing a live demo is the failure this staging exists to avoid")
	}
	if refusal := srv.quotaRegistrationRefusal(user.ID); refusal != "" {
		t.Fatalf("a throttled user must still be able to register; got refused with %q", refusal)
	}

	// 3. Over the hard cap: terminated, and kept out.
	recordTraffic(t, srv, user.ID, 100, 100)
	srv.sweepBandwidthQuotas(time.Now())
	if standing, _ := srv.quotas.Standing(user.ID); standing.State != quotaStopped {
		t.Fatalf("1100 bytes of a 1000 allowance is over the hard cap; want %q, got %q", quotaStopped, standing.State)
	}
	if leaseExists(srv, "demo.example.com") {
		t.Fatal("a user over their hard cap must have their tunnels terminated -- throttling alone is not enforcement, a throttled tunnel still transfers")
	}
	if refusal := srv.quotaRegistrationRefusal(user.ID); refusal == "" {
		t.Fatal("a stopped user must be refused at registration; without that they reconnect three seconds later and the enforcement is decorative")
	}
}

// TestQuotaThrottleLiftsWhenAllowanceIsRaised is the escape hatch working.
//
// The owner's stated model is that a genuine need is accommodated by raising that user's
// limit rather than by leaving the system open. That only holds if raising it takes effect on
// the tunnel already running -- a throttle that needs a reconnect to clear is no use to
// someone mid-presentation.
func TestQuotaThrottleLiftsWhenAllowanceIsRaised(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	user := createQuotaUser(t, srv, "raise@example.com", "user")

	if _, _, err := srv.registry.Register(user.ID, "demo", []PortMapping{{LocalPort: 8080}},
		[]string{"example.com"}, 100, "203.0.113.5", "", nil); err != nil {
		t.Fatalf("failed to register a tunnel: %v", err)
	}

	recordTraffic(t, srv, user.ID, 850, 0)
	srv.sweepBandwidthQuotas(time.Now())
	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 1 {
		t.Fatalf("precondition: the user should be throttled to 1 rps first, got %d", limit)
	}

	// Raise the allowance the way an administrator does -- the per-user override.
	raised := int64(10000)
	stored, err := srv.db.GetUser(user.ID)
	if err != nil {
		t.Fatalf("failed to read back the user: %v", err)
	}
	stored.BandwidthQuotaBytes = &raised
	if err := srv.db.UpdateUser(stored); err != nil {
		t.Fatalf("failed to raise the allowance: %v", err)
	}

	srv.sweepBandwidthQuotas(time.Now())
	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 100 {
		t.Fatalf("raising the allowance must restore the limit the lease was GRANTED (100 rps), got %d -- "+
			"restoring to anything else means the lease no longer has what it registered with", limit)
	}
}

// TestQuotaRegistrationFailsOpenWithoutAStanding is the partition decision, asserted rather
// than assumed.
//
// Fail-open is chosen here: a gateway that has not swept yet, a user nothing has been recorded
// against, and a database that could not be read all allow the registration. This is a demo
// tool, and turning a reporting outage into a full outage is a worse failure than some
// unenforced bytes.
func TestQuotaRegistrationFailsOpenWithoutAStanding(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	createQuotaUser(t, srv, "fresh@example.com", "user")

	// No sweep has run: the tracker holds nothing at all.
	if refusal := srv.quotaRegistrationRefusal("fresh@example.com"); refusal != "" {
		t.Fatalf("a gateway that has not yet swept must not refuse anyone; got %q", refusal)
	}

	// A swept fleet, but this user has no recorded traffic.
	srv.sweepBandwidthQuotas(time.Now())
	if refusal := srv.quotaRegistrationRefusal("fresh@example.com"); refusal != "" {
		t.Fatalf("a user with no recorded traffic must not be refused; got %q", refusal)
	}
	if refusal := srv.quotaRegistrationRefusal("nobody@example.com"); refusal != "" {
		t.Fatalf("an unknown user id must not be refused; got %q", refusal)
	}
}

// TestQuotaPeriodResetRestoresService pins the period decision: a calendar boundary, which
// resets, rather than a rolling window, which never does.
//
// The assertion that matters is on the LIVE LEASE, not on the registration refusal. Clearing
// the standings map alone already unblocks registration -- so a test that only checked the
// refusal would pass whether or not the rollover lifted anything, which is the "assertion
// satisfied by the wrong failure" trap. Measured: with releaseAllQuotaEnforcement removed,
// the refusal check still passed and only the rate-limit check went red.
//
// The mechanism being guarded: after the standings are wiped, a still-throttled user
// evaluates to normal with no PREVIOUS state to transition from, so the per-user transition
// path never runs and nothing would lift their throttle. The rollover has to do it directly.
func TestQuotaPeriodResetRestoresService(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	srv.cfg.BandwidthQuota.PeriodDays = 1
	user := createQuotaUser(t, srv, "reset@example.com", "user")

	if _, _, err := srv.registry.Register(user.ID, "demo", []PortMapping{{LocalPort: 8080}},
		[]string{"example.com"}, 100, "203.0.113.5", "", nil); err != nil {
		t.Fatalf("failed to register a tunnel: %v", err)
	}

	now := time.Now().UTC()
	recordTraffic(t, srv, user.ID, 850, 0)
	srv.sweepBandwidthQuotas(now)
	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 1 {
		t.Fatalf("precondition: the user should be throttled to 1 rps before the period rolls, got %d", limit)
	}

	// A sweep in the next period. The traffic above is outside its window, so the user is
	// back to nothing used.
	next := srv.quotaPeriodStart(now).Add(25 * time.Hour)
	srv.sweepBandwidthQuotas(next)

	if limit := leaseRateLimit(t, srv, "demo.example.com"); limit != 100 {
		t.Fatalf("the period reset must lift the throttle on a live tunnel and restore its granted limit (100 rps), got %d", limit)
	}
	if standing, known := srv.quotas.Standing(user.ID); known && standing.BytesTotal != 0 {
		t.Fatalf("usage from the previous period must not count against the new one; got %d bytes carried over", standing.BytesTotal)
	}
}

// TestQuotaPeriodResetReadmitsAStoppedUser is the other half of the reset, kept separate
// because it is satisfied by a different mechanism and merging the two would let either one
// carry the test.
//
// The mechanism here is the PERIOD BOUNDARY itself: the next sweep asks the database for
// usage since the new period started, so the previous period's traffic is not in the answer.
// Measured, because the obvious candidate is wrong -- removing the standings wipe does NOT
// make this fail, since the sweep overwrites each standing anyway. Removing the boundary
// (making quotaPeriodStart always return the same instant) is what turns it red, with the
// user still refused at 1.2 KiB of a 1000 B allowance.
func TestQuotaPeriodResetReadmitsAStoppedUser(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	srv.cfg.BandwidthQuota.PeriodDays = 1
	user := createQuotaUser(t, srv, "stopped@example.com", "user")

	now := time.Now().UTC()
	recordTraffic(t, srv, user.ID, 1200, 0)
	srv.sweepBandwidthQuotas(now)
	if refusal := srv.quotaRegistrationRefusal(user.ID); refusal == "" {
		t.Fatal("precondition: the user should be stopped before the period rolls")
	}

	srv.sweepBandwidthQuotas(srv.quotaPeriodStart(now).Add(25 * time.Hour))

	if refusal := srv.quotaRegistrationRefusal(user.ID); refusal != "" {
		t.Fatalf("the period reset must let a stopped user register again; still refused with %q", refusal)
	}
}

// TestQuotaPeriodIsCalendarMonthByDefault states the boundary in a test rather than in a
// comment, because "when does my allowance come back" is the question a rolling window cannot
// answer and the one this decision was made to answer.
func TestQuotaPeriodIsCalendarMonthByDefault(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)

	mid := time.Date(2026, 9, 17, 13, 45, 0, 0, time.UTC)
	got := srv.quotaPeriodStart(mid)
	want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("the period must start at 00:00 UTC on the first of the month; want %s, got %s", want, got)
	}

	// The same instant in the next month must resolve to a DIFFERENT period -- a window
	// that never rolls is a rolling window wearing a boundary's clothes.
	if next := srv.quotaPeriodStart(mid.AddDate(0, 1, 0)); next.Equal(got) {
		t.Fatalf("a month later must be a new period, got the same start %s", next)
	}
}

// TestQuotaStateBoundaries pins the two comparisons, including the exact-boundary cases that
// an off-by-one would move.
func TestQuotaStateBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		total     int64
		allowance int64
		want      quotaState
	}{
		{"zero allowance is unlimited", 1 << 40, 0, quotaNormal},
		// A tiny allowance must still stage. allowance/100*pct floors to zero below 100
		// bytes, which would report a user over their soft cap before they sent anything.
		{"tiny allowance still has a soft cap", 1, 50, quotaNormal},
		{"tiny allowance reaches its soft cap", 40, 50, quotaThrottled},
		{"well under", 100, 1000, quotaNormal},
		{"one byte under the soft cap", 799, 1000, quotaNormal},
		{"exactly at the soft cap", 800, 1000, quotaThrottled},
		{"one byte under the hard cap", 999, 1000, quotaThrottled},
		{"exactly at the hard cap", 1000, 1000, quotaStopped},
		{"over", 5000, 1000, quotaStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quotaStateFor(tc.total, tc.allowance, 80); got != tc.want {
				t.Fatalf("%d of %d at 80%%: want %q, got %q", tc.total, tc.allowance, tc.want, got)
			}
		})
	}
}

// TestQuotaThrottleSurvivesReconnect closes the most obvious hole in any limit: doing it
// again.
//
// A throttled user whose client reconnects gets a brand new lease, granted at the limit their
// registration asked for. Without re-applying the standing throttle, one reconnect sheds it --
// and a client reconnects on every network blip anyway.
func TestQuotaThrottleSurvivesReconnect(t *testing.T) {
	srv := newQuotaTestServer(t, 1000)
	user := createQuotaUser(t, srv, "reconnect@example.com", "user")

	recordTraffic(t, srv, user.ID, 850, 0)
	srv.sweepBandwidthQuotas(time.Now())
	if standing, _ := srv.quotas.Standing(user.ID); standing.State != quotaThrottled {
		t.Fatalf("precondition: the user should be throttled, got %q", standing.State)
	}

	if _, _, err := srv.registry.Register(user.ID, "fresh", []PortMapping{{LocalPort: 8080}},
		[]string{"example.com"}, 100, "203.0.113.5", "", nil); err != nil {
		t.Fatalf("failed to register: %v", err)
	}
	srv.applyQuotaToNewLeases(user.ID)

	if limit := leaseRateLimit(t, srv, "fresh.example.com"); limit != 1 {
		t.Fatalf("a tunnel registered while the user is throttled must start throttled (1 rps), got %d -- "+
			"otherwise reconnecting clears the throttle", limit)
	}
}

// TestQuotaEdgeFrameAppliesEnforcement is the edge half, which is where most of the fleet's
// traffic is served (#1947) and where an edge holds no database to decide anything from.
//
// Central decides; this is the edge applying what it was told. A quota that enforces only on
// central would bite hardest on the users it was not aimed at, because the heaviest users are
// the most likely to be on a nearby edge.
func TestQuotaEdgeFrameAppliesEnforcement(t *testing.T) {
	reg := NewRegistry(nil)
	lease := &TunnelLease{
		UserID:          "edge-user",
		SubdomainPrefix: "demo",
		FullHost:        "demo.example.com",
		RateLimit:       100,
		BaseRateLimit:   100,
	}
	addLease(reg, lease)

	if applied := reg.SetQuotaRateLimitForUser("edge-user", 1); applied != 1 {
		t.Fatalf("the throttle should reach the one lease this node holds, reached %d", applied)
	}
	if lease.RateLimit != 1 {
		t.Fatalf("the edge's lease must carry the throttled limit the proxy reads, got %d", lease.RateLimit)
	}
	if lease.BaseRateLimit != 100 {
		t.Fatalf("the granted limit must be preserved so the throttle can be lifted, got %d", lease.BaseRateLimit)
	}

	if restored := reg.ClearQuotaRateLimitForUser("edge-user"); restored != 1 {
		t.Fatalf("lifting the throttle should restore the one lease, restored %d", restored)
	}
	if lease.RateLimit != 100 {
		t.Fatalf("lifting must restore the granted limit (100), got %d", lease.RateLimit)
	}

	// A frame naming a user this node has never heard of is a no-op, which is what makes
	// broadcasting the enforcement to every edge safe.
	if applied := reg.SetQuotaRateLimitForUser("someone-else", 1); applied != 0 {
		t.Fatalf("a quota frame for an unknown user must change nothing here, changed %d", applied)
	}
	if lease.RateLimit != 100 {
		t.Fatalf("an unrelated user's quota frame must not touch this lease, got %d", lease.RateLimit)
	}
}

// TestQuotaAdminOverrideBecomesTheNewBaseline guards a collision between two features that
// both write the same field.
//
// An administrator overriding a live tunnel's rate limit is an explicit decision. If the
// quota's restore path put back the registration-time value, lifting a throttle would
// silently undo that decision -- and the admin would have no way to tell, because both paths
// write lease.RateLimit.
func TestQuotaAdminOverrideBecomesTheNewBaseline(t *testing.T) {
	reg := NewRegistry(nil)
	lease := &TunnelLease{
		UserID:          "user-1",
		SubdomainPrefix: "demo",
		FullHost:        "demo.example.com",
		RateLimit:       100,
		BaseRateLimit:   100,
	}
	addLease(reg, lease)

	if err := reg.UpdateLeaseRateLimit("demo.example.com", 25); err != nil {
		t.Fatalf("admin override failed: %v", err)
	}
	reg.SetQuotaRateLimitForUser("user-1", 1)
	reg.ClearQuotaRateLimitForUser("user-1")

	if lease.RateLimit != 25 {
		t.Fatalf("lifting a quota throttle must restore the administrator's override (25 rps), got %d", lease.RateLimit)
	}
}

// leaseRateLimit reads the effective limit off the registry's real lease -- the same field
// proxy.go consults on every request -- rather than off a snapshot copy.
func leaseRateLimit(t *testing.T, srv *Server, fullHost string) int {
	t.Helper()
	srv.registry.RLock()
	defer srv.registry.RUnlock()
	lease, ok := srv.registry.leases[fullHost]
	if !ok {
		t.Fatalf("no lease for %s", fullHost)
	}
	return lease.RateLimit
}

func leaseExists(srv *Server, fullHost string) bool {
	srv.registry.RLock()
	defer srv.registry.RUnlock()
	_, ok := srv.registry.leases[fullHost]
	return ok
}
