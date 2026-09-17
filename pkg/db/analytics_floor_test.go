package db

import (
	"testing"
	"time"
)

// All Time was broken in two directions at once (#1565): the portals omitted `days`, so the
// server used its 30-day default and the option returned a month; and sending days=0 would have
// made the floor today, returning a single day. Neither is all time.
//
// These pin the meaning of the argument itself, which is what both portals now depend on.

func TestAnalyticsFloorTreatsZeroAsNoLowerBound(t *testing.T) {
	got := analyticsFloor(0)

	// A floor of today is the specific wrong answer that shipped, so name it.
	today := time.Now().UTC().Format("2006-01-02")
	if got == today || got == today+" 00:00:00" {
		t.Fatalf("days=0 produced today's date (%s) -- that is 'today only', not all time", got)
	}

	// Any real row must sort at or after the floor for the WHERE clause to include it.
	if got >= "1970-01-01" {
		t.Fatalf("floor %q is not early enough to precede stored data", got)
	}
}

func TestAnalyticsFloorTreatsNegativeAsNoLowerBound(t *testing.T) {
	// Nothing sends a negative today, but the guard is `<= 0` and a caller subtracting its way
	// to one should get all time rather than a floor in the future.
	if analyticsFloor(-5) != analyticsFloor(0) {
		t.Fatalf("negative days should mean the same as zero, got %q vs %q",
			analyticsFloor(-5), analyticsFloor(0))
	}
}

// Second-precision since #1981, not day-precision. Rounding down to a date made "Last N days"
// mean "since midnight N days ago" -- between N and N+1 days of data depending on the hour the
// page was opened -- and made a sub-day window inexpressible, so the 24h period the Analytics
// screen now offers could not exist at all.
func TestAnalyticsFloorCountsBackFromNow(t *testing.T) {
	for _, days := range []int{1, 7, 14, 30} {
		got := analyticsFloor(days)
		parsed, err := time.Parse("2006-01-02 15:04:05", got)
		if err != nil {
			t.Fatalf("analyticsFloor(%d) = %q, which is not a comparable DATETIME: %v", days, got, err)
		}
		want := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		if d := want.Sub(parsed); d > 2*time.Second || d < -2*time.Second {
			t.Errorf("analyticsFloor(%d) = %q, want ~%q", days, got, want.Format("2006-01-02 15:04:05"))
		}
	}
}

// The DATE columns cannot take the second-precision floor: SQLite compares region_probes.day as
// a string, so '2026-09-16' >= '2026-09-16 14:00:00' is false and the boundary day's probes
// would vanish from every report that reads them.
func TestAnalyticsDayFloorRoundsDownForDateColumns(t *testing.T) {
	for _, days := range []int{1, 7, 30} {
		got := analyticsDayFloor(days)
		if len(got) != len("2006-01-02") {
			t.Errorf("analyticsDayFloor(%d) = %q, want a bare date -- a DATE column compares as a string", days, got)
		}
		if got > analyticsFloor(days) {
			t.Errorf("analyticsDayFloor(%d) = %q sorts after the datetime floor %q, so it would drop the boundary day",
				days, got, analyticsFloor(days))
		}
	}
	if analyticsDayFloor(0) >= "1970-01-01" {
		t.Errorf("all-time day floor %q is not early enough to precede stored data", analyticsDayFloor(0))
	}
}

// The bug users actually saw: All Time and Last 30 Days returned the same data, because the
// portals omitted `days` and the server defaulted to 30. Whatever else changes, these two must
// not resolve to the same window.
//
// (There is no assertion here that the three reports share the helper -- they call it directly,
// so that is structural rather than something a test can meaningfully check.)
func TestAllTimeAndThirtyDaysAreDifferentWindows(t *testing.T) {
	if analyticsFloor(0) == analyticsFloor(30) {
		t.Fatal("All Time and Last 30 Days resolve to the same floor -- the original bug")
	}
}
