package db

import (
	"testing"
	"time"
)

// Acknowledgement timestamps must be readable by SQLite's own date functions (#1897).
//
// Found on production: the row recording that the owner accepted the privacy policy held
// "2026-09-12 05:15:12.509239608 +0000 UTC" -- Go's time.Time.String() form, which SQLite cannot
// parse, so date(accepted_at) returned NULL. Consent never depended on it (HasAcknowledged
// matches on user/document/version), which is exactly why it went unnoticed: the failure is
// evidential. This column is the record of WHEN somebody accepted a privacy policy, and that is
// the question asked in a subject access request.

func TestAcceptedAtIsQueryableByDate(t *testing.T) {
	d := setupTestDB(t)
	if err := d.CreateUser(&User{ID: "u1", Email: "u1@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if err := d.RecordAcknowledgement(&Acknowledgement{
		UserID: "u1", DocumentID: "privacy_policy", Version: "2026-09-11-test",
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	var day, stamp string
	err := d.conn.QueryRow(
		`SELECT COALESCE(date(accepted_at), ''), COALESCE(datetime(accepted_at), '')
		 FROM user_acknowledgements WHERE user_id = 'u1'`).Scan(&day, &stamp)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if day == "" || stamp == "" {
		t.Fatalf("date(accepted_at)=%q datetime(accepted_at)=%q -- SQLite cannot read the stored format", day, stamp)
	}
	if day != time.Now().UTC().Format("2006-01-02") {
		t.Errorf("date(accepted_at) = %q, want today", day)
	}
}

// A range comparison has to work too -- "which acceptances happened after X" is the other half
// of the question, and a lexicographic compare only behaves if the format is fixed-width.
func TestAcceptedAtSupportsRangeQueries(t *testing.T) {
	d := setupTestDB(t)
	if err := d.CreateUser(&User{ID: "u1", Email: "u1@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if err := d.RecordAcknowledgement(&Acknowledgement{
		UserID: "u1", DocumentID: "privacy_policy", Version: "v",
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02 15:04:05")
	var n int
	if err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM user_acknowledgements WHERE accepted_at > ?`, yesterday).Scan(&n); err != nil {
		t.Fatalf("range query: %v", err)
	}
	if n != 1 {
		t.Fatalf("a range comparison found %d rows, want 1", n)
	}
}

// The value must still round-trip into a time.Time, since ListAcknowledgements scans it back and
// policy_consent.go renders it as RFC3339 for the portal. A fix that made the column queryable
// but unreadable by Go would trade one broken thing for another.
func TestAcceptedAtRoundTripsThroughGo(t *testing.T) {
	d := setupTestDB(t)
	if err := d.CreateUser(&User{ID: "u1", Email: "u1@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	when := time.Now().UTC().Truncate(time.Second)
	if err := d.RecordAcknowledgement(&Acknowledgement{
		UserID: "u1", DocumentID: "privacy_policy", Version: "v", AcceptedAt: when,
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	list, err := d.ListAcknowledgements("u1")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d acknowledgements, want 1", len(list))
	}
	if got := list[0].AcceptedAt.UTC(); !got.Equal(when) {
		t.Errorf("round-tripped %v, wrote %v", got, when)
	}
}

// The migration repairs rows written by the old code. Seeded in the broken format exactly as
// production held it, because a fix that only works on new rows leaves the one row that matters
// -- the acceptance already on record -- unqueryable forever.
func TestMigrationNormalisesAnOldStyleTimestamp(t *testing.T) {
	d := setupTestDB(t)
	if err := d.CreateUser(&User{ID: "u1", Email: "u1@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	if _, err := d.conn.Exec(
		`INSERT INTO user_acknowledgements (user_id, document_id, version, accepted_at, ip, user_agent)
		 VALUES ('u1','privacy_policy','old','2026-09-12 05:15:12.509239608 +0000 UTC','1.2.3.4','ua')`); err != nil {
		t.Fatalf("seeding the broken row: %v", err)
	}

	// Before: unreadable, which is the premise -- without this the assertion after the repair
	// could pass against a format that was never broken.
	var before string
	if err := d.conn.QueryRow(
		`SELECT COALESCE(date(accepted_at),'') FROM user_acknowledgements WHERE version='old'`).Scan(&before); err != nil {
		t.Fatalf("pre-check: %v", err)
	}
	if before != "" {
		t.Fatalf("the seeded row was already queryable (%q) -- this test proves nothing", before)
	}

	if _, err := d.conn.Exec(`UPDATE user_acknowledgements
		SET accepted_at = substr(accepted_at, 1, 19)
		WHERE length(accepted_at) > 19
		  AND accepted_at LIKE '____-__-__ __:__:__%'
		  AND accepted_at LIKE '%UTC%'`); err != nil {
		t.Fatalf("running the migration: %v", err)
	}

	var after string
	if err := d.conn.QueryRow(
		`SELECT COALESCE(date(accepted_at),'') FROM user_acknowledgements WHERE version='old'`).Scan(&after); err != nil {
		t.Fatalf("post-check: %v", err)
	}
	if after != "2026-09-12" {
		t.Fatalf("after the migration date(accepted_at) = %q, want 2026-09-12", after)
	}
}
