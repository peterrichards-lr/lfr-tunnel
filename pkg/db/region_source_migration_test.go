package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/regionvocab"
)

// TestMigrationsRekeyAnExistingRegionSourceTable upgrades the production shape (#2372).
//
// Production's client_region_source is keyed (user_id, day) and holds real rows. Migrations
// 38-42 rebuild it keyed (user_id, day, source). A test through setupTestDB would only ever see
// a fresh database, where the base schema already creates the new shape and the rebuild copies
// nothing -- so it would pass against a migration that dropped every row. This builds a
// version-37 database by hand, with rows in the old table, and opens it through the real path.
func TestMigrationsRekeyAnExistingRegionSourceTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "schema37.db")

	old, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := old.Exec(`
		CREATE TABLE client_region_source (
			user_id TEXT NOT NULL,
			day TEXT NOT NULL,
			source TEXT NOT NULL,
			recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, day)
		);
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_version (version) VALUES (37);
		INSERT INTO client_region_source (user_id, day, source, recorded_at) VALUES
			('a', '2026-10-01', 'failback', '2026-10-01 10:00:00'),
			('b', '2026-10-01', 'explicit_server', '2026-10-01 11:00:00'),
			('a', '2026-10-02', 'probe', '2026-10-02 09:00:00');
	`); err != nil {
		t.Fatalf("building a version-37 database: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the migrated database: %v", err)
		}
	})

	// Every row came across, unchanged.
	rows, err := database.conn.Query(`SELECT user_id, day, source, recorded_at FROM client_region_source ORDER BY user_id, day`)
	if err != nil {
		t.Fatalf("reading migrated rows: %v", err)
	}
	var got []string
	for rows.Next() {
		var u, d, s, at string
		if err := rows.Scan(&u, &d, &s, &at); err != nil {
			t.Fatalf("scan: %v", err)
		}
		// The driver renders DATETIME as "2006-01-02T15:04:05..."; compare the instant, not the
		// separator, so the assertion is about the row surviving and not about formatting.
		got = append(got, u+"|"+d+"|"+s+"|"+strings.Replace(at, "T", " ", 1)[:19])
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close rows: %v", err)
	}
	want := []string{
		"a|2026-10-01|failback|2026-10-01 10:00:00",
		"a|2026-10-02|probe|2026-10-02 09:00:00",
		"b|2026-10-01|explicit_server|2026-10-01 11:00:00",
	}
	if len(got) != len(want) {
		t.Fatalf("migrated rows = %v, want %v -- the rebuild lost or invented rows", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The new key, at the SCHEMA level: the repository could hide a wrong key behind an upsert.
	var pk int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('client_region_source') WHERE pk > 0`).Scan(&pk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if pk != 3 {
		t.Errorf("client_region_source primary key has %d column(s), want 3 (user_id, day, source)", pk)
	}
	var leftover int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'client_region_source_pre2372'`).Scan(&leftover); err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	if leftover != 0 {
		t.Error("the intermediate table client_region_source_pre2372 was left behind")
	}

	// And it now behaves: a second route for user a on 2026-10-01 is added, not overwritten.
	repo := NewSQLiteRegionProbeRepo(database.conn)
	at, err := time.Parse("2006-01-02 15:04:05", "2026-10-01 12:00:00")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	if err := repo.RecordRegionSource("a", regionvocab.SourceFailover, at); err != nil {
		t.Fatalf("record after migration: %v", err)
	}
	var routes int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM client_region_source WHERE user_id = 'a' AND day = '2026-10-01'`).Scan(&routes); err != nil {
		t.Fatalf("count: %v", err)
	}
	if routes != 2 {
		t.Errorf("user a has %d route(s) on 2026-10-01 after recording a second, want 2 -- the old key is still in force", routes)
	}
}

// TestARebuildIgnoresAStaleCopyTable covers the one state the #2374 review showed could lose
// live rows: a client_region_source_pre2372 left over from some earlier hand-run. Without the
// leading DROP (migration 38), the copy-out's CREATE ... IF NOT EXISTS is a no-op, the live
// table is dropped, and only the stale rows come back -- reproduced by hand during review.
func TestARebuildIgnoresAStaleCopyTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "schema37-leftover.db")

	old, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := old.Exec(`
		CREATE TABLE client_region_source (
			user_id TEXT NOT NULL,
			day TEXT NOT NULL,
			source TEXT NOT NULL,
			recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, day)
		);
		CREATE TABLE client_region_source_pre2372 (user_id TEXT, day TEXT, source TEXT, recorded_at NUM);
		INSERT INTO client_region_source_pre2372 VALUES ('stale', '2026-01-01', 'probe', '2026-01-01 00:00:00');
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_version (version) VALUES (37);
		INSERT INTO client_region_source (user_id, day, source, recorded_at) VALUES
			('live-1', '2026-10-01', 'failback', '2026-10-01 10:00:00'),
			('live-2', '2026-10-01', 'explicit_server', '2026-10-01 11:00:00');
	`); err != nil {
		t.Fatalf("building a version-37 database with a stale copy: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the migrated database: %v", err)
		}
	})

	var live, stale int
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM client_region_source WHERE user_id LIKE 'live-%'`).Scan(&live); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if err := database.conn.QueryRow(
		`SELECT COUNT(*) FROM client_region_source WHERE user_id = 'stale'`).Scan(&stale); err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if live != 2 {
		t.Errorf("%d of 2 live rows survived the rebuild -- a stale copy table displaced them", live)
	}
	if stale != 0 {
		t.Errorf("the stale copy's row was restored into the live table")
	}
}
