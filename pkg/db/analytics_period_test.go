package db

import (
	"testing"
	"time"
)

// The period selector has to FILTER, not merely render (#1981).
//
// The Analytics screen had a range control already, and it did change the data -- but only in
// whole days, and nothing on the screen said which window a figure covered. So the ~54x
// inflation the #1970 watermark bug left in tunnel_metrics was indistinguishable from corrected
// data, and "last week versus this week" was unanswerable: every headline figure was an
// unlabelled aggregate.
//
// These tests seed rows on BOTH sides of a boundary and require the totals to CHANGE when the
// period does. Asserting that a narrower window merely returns *something* would pass against a
// predicate wired to nothing, which is the trap the issue names.

// seedAt writes one sample of a known size, at a known instant, on a named gateway.
func seedAt(t *testing.T, repo *SQLiteMetricRepo, node, host string, at time.Time, bytesIn, bytesOut int64) {
	t.Helper()
	if err := repo.RecordTunnelMetric(&TunnelMetric{
		UserID:          "u1",
		SubdomainPrefix: host,
		FullHost:        host + ".example.com",
		BytesIn:         bytesIn,
		BytesOut:        bytesOut,
		ConnectedAt:     at,
		RecordedAt:      at,
		NodeID:          node,
	}); err != nil {
		t.Fatalf("RecordTunnelMetric: %v", err)
	}
}

func totalBytes(tot BandwidthTotals) int64 { return tot.BytesIn + tot.BytesOut }

// The headline figure must move when the period moves. Seeded two hours ago and forty hours ago
// -- either side of the 24h boundary, and both inside 7 days.
func TestPeriodFiltersTheHeadlineTotal(t *testing.T) {
	repo := setupMetricRepo(t)
	now := time.Now().UTC()

	seedAt(t, repo, "control", "recent", now.Add(-2*time.Hour), 1_000, 2_000)
	seedAt(t, repo, "control", "older", now.Add(-40*time.Hour), 400_000, 800_000)

	day, err := repo.GetGlobalAnalytics(1)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(1): %v", err)
	}
	week, err := repo.GetGlobalAnalytics(7)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(7): %v", err)
	}

	if got := totalBytes(day.Totals); got != 3_000 {
		t.Errorf("24h total = %d, want 3000 (only the row two hours old)", got)
	}
	if got := totalBytes(week.Totals); got != 1_203_000 {
		t.Errorf("7d total = %d, want 1203000 (both rows)", got)
	}
	// The assertion that carries the feature: a control the server ignores would make these
	// equal, and every "does it render?" check would still pass.
	if totalBytes(day.Totals) == totalBytes(week.Totals) {
		t.Fatal("24h and 7d produced the same total -- the period is not being applied")
	}
}

// All Time must include what 24h excludes, and must not be a synonym for the default window.
func TestAllTimeIncludesRowsEveryBoundedPeriodExcludes(t *testing.T) {
	repo := setupMetricRepo(t)
	now := time.Now().UTC()

	seedAt(t, repo, "control", "recent", now.Add(-2*time.Hour), 1_000, 0)
	seedAt(t, repo, "control", "ancient", now.AddDate(0, 0, -400), 9_000_000, 0)

	day, err := repo.GetGlobalAnalytics(1)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(1): %v", err)
	}
	month, err := repo.GetGlobalAnalytics(30)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(30): %v", err)
	}
	all, err := repo.GetGlobalAnalytics(0)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(0): %v", err)
	}

	if totalBytes(day.Totals) != 1_000 {
		t.Errorf("24h total = %d, want 1000", totalBytes(day.Totals))
	}
	if totalBytes(month.Totals) != 1_000 {
		t.Errorf("30d total = %d, want 1000 -- the 400-day-old row is outside it", totalBytes(month.Totals))
	}
	if totalBytes(all.Totals) != 9_001_000 {
		t.Errorf("all-time total = %d, want 9001000", totalBytes(all.Totals))
	}
	if totalBytes(all.Totals) == totalBytes(month.Totals) {
		t.Fatal("All Time equals Last 30 Days -- #1565's original symptom, returned")
	}
}

// A sub-day window has to be expressible at all. Before #1981 the floor rounded down to a date,
// so days=1 meant "since midnight yesterday" -- between 24 and 48 hours depending on the hour --
// and a row 30 hours old fell inside the "last 24 hours" the portal offered to show.
func TestTwentyFourHoursIsTwentyFourHoursNotTwoCalendarDays(t *testing.T) {
	repo := setupMetricRepo(t)
	now := time.Now().UTC()

	seedAt(t, repo, "control", "inside", now.Add(-23*time.Hour), 7, 0)
	seedAt(t, repo, "control", "outside", now.Add(-30*time.Hour), 5_000, 0)

	day, err := repo.GetGlobalAnalytics(1)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(1): %v", err)
	}
	if got := totalBytes(day.Totals); got != 7 {
		t.Fatalf("24h total = %d, want 7 -- a row 30 hours old is outside a 24-hour window", got)
	}
}

// The per-node breakdown is where a stalled reporting path shows up, so it has to honour the
// period too -- #1958/#1970 was an edge whose bytes stopped being recorded, and against an
// all-time aggregate that reads as a flat number rather than as a drop.
func TestPeriodFiltersThePerNodeBreakdown(t *testing.T) {
	repo := setupMetricRepo(t)
	now := time.Now().UTC()

	// "eu" moved traffic recently. "us" moved a lot, but only before the boundary -- exactly
	// the shape of a gateway that has stopped reporting.
	seedAt(t, repo, "eu", "a", now.Add(-3*time.Hour), 100, 100)
	seedAt(t, repo, "us", "b", now.Add(-40*time.Hour), 500_000, 500_000)

	byNode := func(days int) map[string]int64 {
		t.Helper()
		stats, err := repo.GetGlobalAnalytics(days)
		if err != nil {
			t.Fatalf("GetGlobalAnalytics(%d): %v", days, err)
		}
		out := map[string]int64{}
		for _, n := range stats.NodeTotals {
			out[n.NodeID] = n.BytesIn + n.BytesOut
		}
		return out
	}

	day := byNode(1)
	week := byNode(7)

	if day["eu"] != 200 {
		t.Errorf("24h eu = %d, want 200", day["eu"])
	}
	if _, present := day["us"]; present {
		t.Errorf("24h breakdown still contains us (%d bytes) -- its only traffic is 40 hours old", day["us"])
	}
	if week["us"] != 1_000_000 {
		t.Errorf("7d us = %d, want 1000000", week["us"])
	}
	if day["us"] == week["us"] {
		t.Fatal("the per-node breakdown is identical across periods -- it is not being filtered")
	}
}

// Sessions are counted per period as well, and still counted as sessions rather than as samples.
func TestPeriodFiltersPerNodeSessionCounts(t *testing.T) {
	repo := setupMetricRepo(t)
	now := time.Now().UTC()

	// One session sampled three times inside the window, one distinct session outside it.
	// connected_at is stable across a session's samples, which is what identifies it.
	connected := now.Add(-4 * time.Hour)
	for _, offset := range []time.Duration{-3 * time.Hour, -2 * time.Hour, -1 * time.Hour} {
		if err := repo.RecordTunnelMetric(&TunnelMetric{
			UserID:          "u1",
			SubdomainPrefix: "live",
			FullHost:        "live.example.com",
			BytesIn:         1,
			BytesOut:        1,
			ConnectedAt:     connected,
			RecordedAt:      now.Add(offset),
			NodeID:          "eu",
		}); err != nil {
			t.Fatalf("RecordTunnelMetric: %v", err)
		}
	}
	seedAt(t, repo, "eu", "gone", now.Add(-40*time.Hour), 1, 1)

	day, err := repo.GetGlobalAnalytics(1)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(1): %v", err)
	}
	week, err := repo.GetGlobalAnalytics(7)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics(7): %v", err)
	}

	sessionsFor := func(stats *GlobalAnalytics, node string) int {
		for _, n := range stats.NodeTotals {
			if n.NodeID == node {
				return n.Sessions
			}
		}
		return -1
	}

	if got := sessionsFor(day, "eu"); got != 1 {
		t.Errorf("24h eu sessions = %d, want 1 -- three samples of one session", got)
	}
	if got := sessionsFor(week, "eu"); got != 2 {
		t.Errorf("7d eu sessions = %d, want 2", got)
	}
}

// The window the server reports has to be the window it queried. A screen that states bounds it
// did not honour is worse than one that states none, because it looks authoritative.
func TestAnalyticsWindowMatchesTheFloorTheQueriesUse(t *testing.T) {
	for _, days := range []int{1, 7, 30} {
		from, to := AnalyticsWindow(days)
		if from.IsZero() {
			t.Fatalf("AnalyticsWindow(%d) reported an unbounded window", days)
		}
		if gap := to.Sub(from); gap != time.Duration(days)*24*time.Hour {
			t.Errorf("AnalyticsWindow(%d) spans %v, want %dh", days, gap, days*24)
		}
		// Within a second of the string the WHERE clauses actually compare against.
		if want, got := from.Format("2006-01-02 15:04:05"), analyticsFloor(days); want != got {
			delta, err := time.Parse("2006-01-02 15:04:05", got)
			if err != nil {
				t.Fatalf("analyticsFloor(%d) = %q, unparseable: %v", days, got, err)
			}
			if d := from.Sub(delta); d > time.Second || d < -time.Second {
				t.Errorf("analyticsFloor(%d) = %q but the reported window starts %q", days, got, want)
			}
		}
	}

	from, _ := AnalyticsWindow(0)
	if !from.IsZero() {
		t.Error("All Time should report an unbounded start, so the portals can say so")
	}
}
