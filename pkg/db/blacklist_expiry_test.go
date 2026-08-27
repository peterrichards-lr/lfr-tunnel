package db

import (
	"testing"
	"time"
)

// Automatic bans expire and escalate; manual bans do not (#1353). These tests pin that split,
// because the enforcement point sits ahead of all routing -- a banned address cannot reach the
// admin API to undo its own ban, so "permanent" really does mean permanent.

// Split from TestAutoBan_Expires (#1389). "The ban takes effect" has nothing to do with
// expiry, but it used to be checked against a row that self-destructed in 40ms -- so on a
// loaded runner the row was already gone by the time it was read back, and the test
// reported that the ban never took effect. Under -race, which instruments every memory
// access, that was often enough to fail unrelated PRs. A lifetime that cannot lapse
// mid-test removes the race rather than widening it.
func TestAutoBan_TakesEffect(t *testing.T) {
	database := setupTestDB(t)

	if _, err := database.AddAutoBan("203.0.113.9", "flooding", time.Hour, 1, 0); err != nil {
		t.Fatalf("AddAutoBan: %v", err)
	}

	if !mustBlacklisted(t, database, "203.0.113.9") {
		t.Fatal("the ban did not take effect")
	}
}

func TestAutoBan_Expires(t *testing.T) {
	database := setupTestDB(t)

	// Assert on the returned entry rather than reading the row back, so this stays
	// non-vacuous -- it proves an expiring ban was created -- without depending on
	// beating the clock to observe it.
	entry, err := database.AddAutoBan("203.0.113.9", "flooding", 40*time.Millisecond, 1, 0)
	if err != nil {
		t.Fatalf("AddAutoBan: %v", err)
	}
	if entry.ExpiresAt == nil {
		t.Fatal("expected an expiring ban, got one with no expiry")
	}

	// The only timing assertion left, and it fails safe: more delay can only make the ban
	// more expired, so a slow machine cannot produce a false failure.
	time.Sleep(200 * time.Millisecond)

	if mustBlacklisted(t, database, "203.0.113.9") {
		t.Error("the ban was still in force after its expiry -- an automatic ban must lift on its own")
	}
}

// A zero duration is the pre-existing behaviour, kept available on purpose.
func TestAutoBan_ZeroDurationNeverExpires(t *testing.T) {
	database := setupTestDB(t)

	entry, err := database.AddAutoBan("203.0.113.9", "flooding", 0, 2, time.Hour)
	if err != nil {
		t.Fatalf("AddAutoBan: %v", err)
	}
	if entry.ExpiresAt != nil {
		t.Error("a zero duration should mean the ban never expires")
	}
}

func TestManualBan_NeverExpires(t *testing.T) {
	database := setupTestDB(t)

	if err := database.AddBlacklistIP("203.0.113.9", "banned by an admin"); err != nil {
		t.Fatalf("AddBlacklistIP: %v", err)
	}

	entries, err := database.ListBlacklistedIPs()
	if err != nil {
		t.Fatalf("ListBlacklistedIPs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ExpiresAt != nil {
		t.Error("a manual ban must not expire -- a person decided on it")
	}
}

// The count has to outlive the ban, or a repeat offender starts from zero every time and the
// escalation never escalates.
func TestAutoBan_BanCountSurvivesExpiry(t *testing.T) {
	database := setupTestDB(t)

	if _, err := database.AddAutoBan("203.0.113.9", "flooding", 20*time.Millisecond, 2, time.Hour); err != nil {
		t.Fatalf("first ban: %v", err)
	}
	time.Sleep(40 * time.Millisecond)

	second, err := database.AddAutoBan("203.0.113.9", "flooding again", 20*time.Millisecond, 2, time.Hour)
	if err != nil {
		t.Fatalf("second ban: %v", err)
	}
	if second.BanCount != 2 {
		t.Errorf("ban count = %d, want 2 -- history was lost when the first ban lapsed", second.BanCount)
	}
}

// An expired row stays until its history is no longer useful. Pruning it earlier would silently
// reset escalation.
func TestPruneBlacklist_KeepsRecentHistoryAndDropsOld(t *testing.T) {
	database := setupTestDB(t)

	if _, err := database.AddAutoBan("203.0.113.9", "flooding", 10*time.Millisecond, 1, 0); err != nil {
		t.Fatalf("AddAutoBan: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	// Retention far longer than the ban: expired, but still wanted for escalation.
	removed, err := database.PruneBlacklist(time.Hour)
	if err != nil {
		t.Fatalf("PruneBlacklist: %v", err)
	}
	if removed != 0 {
		t.Errorf("pruned %d rows, want 0 -- recent history must be kept so escalation still sees it", removed)
	}

	// Retention shorter than how long ago it expired: now it can go.
	removed, err = database.PruneBlacklist(time.Nanosecond)
	if err != nil {
		t.Fatalf("PruneBlacklist: %v", err)
	}
	if removed != 1 {
		t.Errorf("pruned %d rows, want 1", removed)
	}
}

func TestPruneBlacklist_LeavesManualBansAlone(t *testing.T) {
	database := setupTestDB(t)

	if err := database.AddBlacklistIP("203.0.113.9", "banned by an admin"); err != nil {
		t.Fatalf("AddBlacklistIP: %v", err)
	}

	if _, err := database.PruneBlacklist(time.Nanosecond); err != nil {
		t.Fatalf("PruneBlacklist: %v", err)
	}

	if !mustBlacklisted(t, database, "203.0.113.9") {
		t.Error("a permanent ban was pruned -- only expired automatic bans should be")
	}
}

func TestEscalatedBanDuration(t *testing.T) {
	base := time.Hour
	cap := 8 * time.Hour

	cases := []struct {
		name     string
		factor   float64
		banCount int
		want     time.Duration
	}{
		{"first offence is the base duration", 2, 1, time.Hour},
		{"second doubles", 2, 2, 2 * time.Hour},
		{"third doubles again", 2, 3, 4 * time.Hour},
		{"escalation is capped", 2, 10, 8 * time.Hour},
		{"factor of 1 does not escalate", 1, 5, time.Hour},
		{"a nonsense factor does not shrink the ban", 0.5, 5, time.Hour},
		{"a zero ban count is treated as the first", 2, 0, time.Hour},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscalatedBanDuration(base, tc.factor, cap, tc.banCount); got != tc.want {
				t.Errorf("EscalatedBanDuration(%v, %v, %v, %d) = %v, want %v",
					base, tc.factor, cap, tc.banCount, got, tc.want)
			}
		})
	}
}

// A repeat offender must not overflow the multiplication into something undefined.
func TestEscalatedBanDuration_HandlesAVeryLargeBanCount(t *testing.T) {
	got := EscalatedBanDuration(time.Hour, 2, 7*24*time.Hour, 500)
	if got != 7*24*time.Hour {
		t.Errorf("got %v, want the cap -- a long ban history must saturate, not overflow", got)
	}
}

// mustBlacklisted keeps the error handling out of the assertions above, which are about the
// expiry rules rather than about the query working.
func mustBlacklisted(t *testing.T, database *DB, ip string) bool {
	t.Helper()
	blocked, err := database.IsBlacklisted(ip)
	if err != nil {
		t.Fatalf("IsBlacklisted(%s): %v", ip, err)
	}
	return blocked
}
