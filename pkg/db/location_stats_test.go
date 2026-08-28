package db

import (
	"strings"
	"testing"
)

// TestLocationStatsSchemaCannotHoldAUser is the assertion that makes this feature
// anonymous rather than pseudonymous, and it is deliberately made against the schema
// rather than against the code that writes it (#1152).
//
// Code that declines to store a user can be changed by anyone adding one more argument.
// A table with no column able to hold one cannot be, without a migration that would show
// up in review as exactly what it is.
func TestLocationStatsSchemaCannotHoldAUser(t *testing.T) {
	database := setupTestDB(t)

	rows, err := database.conn.Query(`PRAGMA table_info(location_stats)`)
	if err != nil {
		t.Fatalf("reading table_info: %v", err)
	}
	defer rows.Close() //nolint:errcheck

	columns := make([]string, 0, 4)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scanning table_info: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating table_info: %v", err)
	}
	if len(columns) == 0 {
		t.Fatalf("location_stats does not exist -- migration 24 did not run")
	}

	want := map[string]bool{"period": true, "bucket": true, "count": true, "updated_at": true}
	for _, col := range columns {
		if !want[col] {
			t.Errorf("unexpected column %q on location_stats: every column here must be part of the anonymous aggregate", col)
		}
		// Belt and braces, so a column renamed into something identifying still trips.
		lower := strings.ToLower(col)
		for _, banned := range []string{"user", "email", "ip", "addr", "host", "session", "token"} {
			if strings.Contains(lower, banned) {
				t.Errorf("column %q on location_stats looks identifying (contains %q); this table must never pair a user with a location", col, banned)
			}
		}
	}
	for col := range want {
		found := false
		for _, got := range columns {
			if got == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("location_stats is missing the %q column", col)
		}
	}
}

func TestUpsertAndGetLocationStats(t *testing.T) {
	database := setupTestDB(t)

	if err := database.UpsertLocationStats("2026-W35", []LocationStat{
		{Bucket: "GB", Count: 12},
		{Bucket: "US", Count: 7},
		{Bucket: "OTHER", Count: 5},
	}); err != nil {
		t.Fatalf("UpsertLocationStats: %v", err)
	}

	period, stats, err := database.GetLocationStats("2026-W35")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if period != "2026-W35" {
		t.Errorf("period: got %q, want %q", period, "2026-W35")
	}
	if len(stats) != 3 {
		t.Fatalf("got %d buckets, want 3", len(stats))
	}
	// Largest first, so the panel does not have to sort.
	if stats[0].Bucket != "GB" || stats[0].Count != 12 {
		t.Errorf("first bucket: got %+v, want {GB 12}", stats[0])
	}
	if stats[1].Bucket != "US" || stats[1].Count != 7 {
		t.Errorf("second bucket: got %+v, want {US 7}", stats[1])
	}
}

// TestUpsertLocationStatsNeverLowersACount covers a restart mid-period: the in-memory set
// the cardinality comes from is lost, so the rebuilt one starts empty and would otherwise
// overwrite a larger count with a smaller one.
func TestUpsertLocationStatsNeverLowersACount(t *testing.T) {
	database := setupTestDB(t)

	if err := database.UpsertLocationStats("2026-W35", []LocationStat{{Bucket: "GB", Count: 12}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// A smaller figure after a restart must not erase what was already known.
	if err := database.UpsertLocationStats("2026-W35", []LocationStat{{Bucket: "GB", Count: 3}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	_, stats, err := database.GetLocationStats("2026-W35")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Count != 12 {
		t.Errorf("got %+v, want a single GB bucket still holding 12", stats)
	}

	// A larger figure still raises it.
	if err := database.UpsertLocationStats("2026-W35", []LocationStat{{Bucket: "GB", Count: 20}}); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	_, stats, err = database.GetLocationStats("2026-W35")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Count != 20 {
		t.Errorf("got %+v, want a single GB bucket holding 20", stats)
	}
}

// TestGetLocationStatsDefaultsToTheLatestPeriod is what the admin panel relies on: it
// asks for no period in particular and gets the most recent ISO week on record.
func TestGetLocationStatsDefaultsToTheLatestPeriod(t *testing.T) {
	database := setupTestDB(t)

	if err := database.UpsertLocationStats("2026-W34", []LocationStat{{Bucket: "GB", Count: 8}}); err != nil {
		t.Fatalf("upsert W34: %v", err)
	}
	if err := database.UpsertLocationStats("2026-W35", []LocationStat{{Bucket: "US", Count: 9}}); err != nil {
		t.Fatalf("upsert W35: %v", err)
	}

	period, stats, err := database.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if period != "2026-W35" {
		t.Errorf("period: got %q, want %q", period, "2026-W35")
	}
	if len(stats) != 1 || stats[0].Bucket != "US" {
		t.Errorf("got %+v, want only the W35 buckets", stats)
	}
}

// TestGetLocationStatsOnAnEmptyTable is the state every deployment starts in, and the one
// it stays in permanently without a MaxMind database.
func TestGetLocationStatsOnAnEmptyTable(t *testing.T) {
	database := setupTestDB(t)

	period, stats, err := database.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if period != "" {
		t.Errorf("period: got %q, want empty", period)
	}
	if len(stats) != 0 {
		t.Errorf("got %+v, want no buckets", stats)
	}
}

// TestUpsertLocationStatsIgnoresEmptyInput keeps the scheduled flush from writing noise
// on a server with no traffic.
func TestUpsertLocationStatsIgnoresEmptyInput(t *testing.T) {
	database := setupTestDB(t)

	if err := database.UpsertLocationStats("", []LocationStat{{Bucket: "GB", Count: 5}}); err != nil {
		t.Errorf("empty period: %v", err)
	}
	if err := database.UpsertLocationStats("2026-W35", nil); err != nil {
		t.Errorf("no stats: %v", err)
	}
	// A zero or negative count is not a bucket; it must not create a row.
	if err := database.UpsertLocationStats("2026-W35", []LocationStat{{Bucket: "GB", Count: 0}}); err != nil {
		t.Errorf("zero count: %v", err)
	}
	_, stats, err := database.GetLocationStats("")
	if err != nil {
		t.Fatalf("GetLocationStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("got %+v, want no rows written", stats)
	}
}
