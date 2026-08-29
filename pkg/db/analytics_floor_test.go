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
	if got == today {
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

func TestAnalyticsFloorCountsBackFromToday(t *testing.T) {
	for _, days := range []int{1, 7, 14, 30} {
		want := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
		if got := analyticsFloor(days); got != want {
			t.Errorf("analyticsFloor(%d) = %q, want %q", days, got, want)
		}
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
