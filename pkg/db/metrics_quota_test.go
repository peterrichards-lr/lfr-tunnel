package db

import (
	"path/filepath"
	"testing"
	"time"
)

// BandwidthUsageByUserSince is the feed the whole quota is enforced from (#1959), so the
// three decisions inside its query each get an assertion here: the two directions stay apart,
// the period cuts on recorded_at, and a row with no user is not aggregated into a phantom
// account.

func openQuotaTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "quota.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database failed: %v", err)
		}
	})
	return database
}

// TestBandwidthUsageKeepsDirectionsApart is the reporting half of the measure decision.
//
// The quota is enforced on the total, but egress has to remain separately visible, because
// that is the figure that maps to the AWS invoice. A query that summed them in SQL would
// satisfy enforcement and silently lose the ability to say what the traffic cost.
func TestBandwidthUsageKeepsDirectionsApart(t *testing.T) {
	database := openQuotaTestDB(t)
	now := time.Now().UTC()

	for _, m := range []*TunnelMetric{
		{UserID: "u1", FullHost: "a.example.com", BytesIn: 100, BytesOut: 400, RecordedAt: now},
		{UserID: "u1", FullHost: "a.example.com", BytesIn: 50, BytesOut: 50, RecordedAt: now},
		{UserID: "u2", FullHost: "b.example.com", BytesIn: 7, BytesOut: 3, RecordedAt: now},
	} {
		if err := database.RecordTunnelMetric(m); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	usage, err := database.BandwidthUsageByUserSince(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	byUser := map[string]UserBandwidthUsage{}
	for _, u := range usage {
		byUser[u.UserID] = u
	}

	u1 := byUser["u1"]
	if u1.BytesIn != 150 || u1.BytesOut != 450 {
		t.Fatalf("directions must be summed separately: want in=150 out=450, got in=%d out=%d", u1.BytesIn, u1.BytesOut)
	}
	if u1.Total() != 600 {
		t.Fatalf("the enforced measure is the total of both directions: want 600, got %d", u1.Total())
	}
	if byUser["u2"].Total() != 10 {
		t.Fatalf("per-user grouping is wrong: u2 should total 10, got %d", byUser["u2"].Total())
	}
}

// TestBandwidthUsageCutsOnRecordedAtNotConnectedAt pins the column the period boundary uses.
//
// connected_at is when the SESSION started. A tunnel opened on the 28th and still running on
// the 3rd would, under connected_at, put every byte it has ever carried into the previous
// period -- so a long-lived tunnel would never trip a quota at all, which is the exact
// opposite of the usage pattern this exists to bound. recorded_at is when the bytes were
// measured, and that is the instant the boundary has to cut on.
//
// The fixture is a state the gateway genuinely produces: MetricsCollector stamps
// ConnectedAt from the lease's creation time and RecordedAt from the moment of the sweep, so
// a row whose connected_at precedes the period and whose recorded_at falls inside it is what
// every tick of a long-running tunnel writes.
func TestBandwidthUsageCutsOnRecordedAtNotConnectedAt(t *testing.T) {
	database := openQuotaTestDB(t)
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	// Session started well before the period; bytes measured inside it. Must COUNT.
	if err := database.RecordTunnelMetric(&TunnelMetric{
		UserID:      "u1",
		FullHost:    "a.example.com",
		BytesIn:     500,
		BytesOut:    500,
		ConnectedAt: now.Add(-72 * time.Hour),
		RecordedAt:  now,
	}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Bytes measured before the period. Must NOT count.
	if err := database.RecordTunnelMetric(&TunnelMetric{
		UserID:      "u1",
		FullHost:    "a.example.com",
		BytesIn:     9000,
		BytesOut:    9000,
		ConnectedAt: now.Add(-72 * time.Hour),
		RecordedAt:  now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	usage, err := database.BandwidthUsageByUserSince(periodStart)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(usage) != 1 {
		t.Fatalf("expected the one user's traffic in the window, got %d row(s) -- 0 means the window cut on "+
			"connected_at, so a session that started before the period contributed nothing at all and could "+
			"never trip a quota", len(usage))
	}
	if usage[0].Total() != 1000 {
		t.Fatalf("the window must cut on recorded_at: want 1000 (the row measured inside the period), got %d "+
			"-- 19000 means it cut on connected_at and swept in a previous period's traffic, 0 means it excluded "+
			"a live session because the session predates the period", usage[0].Total())
	}
}

// TestBandwidthUsageExcludesUnattributedRows guards against a phantom account.
//
// A row with no user_id is real traffic with nobody to charge it to. Left in, GROUP BY makes
// it one enormous "user" whose usage only ever grows and which no administrator can raise an
// allowance for -- a permanently-stopped account that does not exist.
func TestBandwidthUsageExcludesUnattributedRows(t *testing.T) {
	database := openQuotaTestDB(t)
	now := time.Now().UTC()

	if err := database.RecordTunnelMetric(&TunnelMetric{
		UserID: "", FullHost: "orphan.example.com", BytesIn: 10, BytesOut: 10, RecordedAt: now,
	}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}
	if err := database.RecordTunnelMetric(&TunnelMetric{
		UserID: "u1", FullHost: "a.example.com", BytesIn: 1, BytesOut: 1, RecordedAt: now,
	}); err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	usage, err := database.BandwidthUsageByUserSince(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	for _, u := range usage {
		if u.UserID == "" {
			t.Fatal("rows with no user must not be aggregated into a phantom account")
		}
	}
	if len(usage) != 1 {
		t.Fatalf("expected exactly the one attributable user, got %d", len(usage))
	}
}

// TestUserBandwidthQuotaOverridePersists is the per-user escape hatch surviving a round trip.
//
// #1004 is the precedent worth remembering: db.User.MaxCustomDomains was added without its
// migration, so every write of it silently no-op'd -- the admin field saved with a 200 OK and
// the value was never stored. A quota override that does not persist looks identical to one
// that was never set, and the user stays throttled.
func TestUserBandwidthQuotaOverridePersists(t *testing.T) {
	database := openQuotaTestDB(t)

	quota := int64(12345678)
	u := &User{ID: "u1", Email: "u1@example.com", Role: "user", Status: UserStatusApproved, BandwidthQuotaBytes: &quota}
	if err := database.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	got, err := database.GetUser("u1")
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	if got.BandwidthQuotaBytes == nil || *got.BandwidthQuotaBytes != 12345678 {
		t.Fatalf("the per-user override must survive a create/read round trip; got %v", got.BandwidthQuotaBytes)
	}

	raised := int64(99999999)
	got.BandwidthQuotaBytes = &raised
	if err := database.UpdateUser(got); err != nil {
		t.Fatalf("failed to update: %v", err)
	}
	again, err := database.GetUser("u1")
	if err != nil {
		t.Fatalf("failed to read back after update: %v", err)
	}
	if again.BandwidthQuotaBytes == nil || *again.BandwidthQuotaBytes != 99999999 {
		t.Fatalf("raising an allowance must persist; got %v", again.BandwidthQuotaBytes)
	}

	// nil is "no override", and must round-trip as nil rather than as a zero that would
	// read as an exemption.
	again.BandwidthQuotaBytes = nil
	if err := database.UpdateUser(again); err != nil {
		t.Fatalf("failed to clear: %v", err)
	}
	cleared, err := database.GetUser("u1")
	if err != nil {
		t.Fatalf("failed to read back after clearing: %v", err)
	}
	if cleared.BandwidthQuotaBytes != nil {
		t.Fatalf("clearing an override must store NULL, not 0 -- 0 means unlimited; got %v", *cleared.BandwidthQuotaBytes)
	}
}
