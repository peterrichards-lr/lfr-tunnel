package db

import (
	"testing"
	"time"
)

// Tests for the diagnostic bundle store (#1894).
//
// The property under test is erasure. This is the first user data the gateway keeps, PRIVACY.md
// now makes checkable promises about it -- at most 30 days, deleted immediately on withdrawal,
// deleted with the account -- and a promise nothing enforces is worse than no promise. Each case
// below is one of those sentences.

func seedBundleUser(t *testing.T, d *DB, id string) {
	t.Helper()
	if err := d.CreateUser(&User{ID: id, Email: id + "@example.com", Role: "user", Status: "approved"}); err != nil {
		t.Fatalf("creating user: %v", err)
	}
}

func seedBundle(t *testing.T, d *DB, id, userID string) {
	t.Helper()
	if err := d.StoreDiagnosticsBundle(&DiagnosticsBundle{
		ID: id, UserID: userID, RequestedBy: "admin@example.com",
		Kind: "error", Content: []byte("a log line\n"), Bytes: 11,
	}); err != nil {
		t.Fatalf("storing bundle %s: %v", id, err)
	}
}

func TestStoreAndReadBackABundle(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundle(t, d, "b1", "u1")

	got, err := d.GetDiagnosticsBundle("b1")
	if err != nil || got == nil {
		t.Fatalf("reading back: %v (got %v)", err, got)
	}
	if string(got.Content) != "a log line\n" {
		t.Errorf("content = %q", got.Content)
	}

	list, err := d.ListDiagnosticsBundles("u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, err = %v", list, err)
	}
	// A listing is metadata. Shipping every byte of every bundle to render a table would be
	// slow and a wider exposure than the page needs.
	if list[0].Content != nil {
		t.Errorf("the listing carried bundle content: %q", list[0].Content)
	}
}

// PRIVACY.md: "deleted immediately if you withdraw consent -- not just 'no new logs are
// collected', the ones already collected are destroyed".
func TestWithdrawalDeletesEverythingAlreadyCollected(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundle(t, d, "b1", "u1")
	seedBundle(t, d, "b2", "u1")

	n, err := d.DeleteDiagnosticsBundlesForUser("u1")
	if err != nil {
		t.Fatalf("purging: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d, want 2 -- the count is what the audit entry reports", n)
	}
	list, err := d.ListDiagnosticsBundles("u1")
	if err != nil {
		t.Fatalf("listing after the purge: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("bundles survived a withdrawal: %v", list)
	}
}

func TestWithdrawalDoesNotTouchAnotherUsersBundles(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundleUser(t, d, "u2")
	seedBundle(t, d, "b1", "u1")
	seedBundle(t, d, "b2", "u2")

	if _, err := d.DeleteDiagnosticsBundlesForUser("u1"); err != nil {
		t.Fatalf("purging: %v", err)
	}
	list, err := d.ListDiagnosticsBundles("u2")
	if err != nil {
		t.Fatalf("listing u2's bundles: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("purging u1 destroyed u2's bundles: %v", list)
	}
}

// PRIVACY.md: "deleted immediately ... if your account is deleted". The schema carries
// ON DELETE CASCADE, and foreign_keys is ON -- this asserts the cascade is real rather than
// decorative, because a FK that is not enforced looks identical in the schema.
func TestDeletingTheUserCascadesToTheirBundles(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundle(t, d, "b1", "u1")

	if err := d.DeleteUser("u1"); err != nil {
		t.Fatalf("deleting user: %v", err)
	}
	got, err := d.GetDiagnosticsBundle("b1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if got != nil {
		t.Fatal("a bundle survived the deletion of its user -- ON DELETE CASCADE is not being enforced")
	}
}

// PRIVACY.md: "for at most 30 days". The number is only true because the sweep runs.
func TestRetentionSweepDeletesOnlyWhatIsOverdue(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundle(t, d, "fresh", "u1")
	seedBundle(t, d, "stale", "u1")

	overdue := time.Now().UTC().AddDate(0, 0, -(DiagnosticsRetentionDays + 1))
	if _, err := d.conn.Exec(`UPDATE diagnostics_bundles SET collected_at = ? WHERE id = 'stale'`, overdue); err != nil {
		t.Fatalf("ageing a bundle: %v", err)
	}

	n, err := d.PruneDiagnosticsBundles()
	if err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d, want 1", n)
	}

	// BOUNDING. A sweep that deleted everything would satisfy "nothing is kept too long" and
	// destroy the feature.
	fresh, err := d.GetDiagnosticsBundle("fresh")
	if err != nil {
		t.Fatalf("reading the fresh bundle: %v", err)
	}
	if fresh == nil {
		t.Error("the retention sweep deleted a bundle that was not overdue")
	}
	stale, err := d.GetDiagnosticsBundle("stale")
	if err != nil {
		t.Fatalf("reading the stale bundle: %v", err)
	}
	if stale != nil {
		t.Error("the overdue bundle survived the sweep")
	}
}

// A bundle exactly at the boundary must not be swept: "at most 30 days" means 30 is allowed.
func TestABundleInsideTheWindowSurvives(t *testing.T) {
	d := setupTestDB(t)
	seedBundleUser(t, d, "u1")
	seedBundle(t, d, "edge", "u1")
	justInside := time.Now().UTC().AddDate(0, 0, -(DiagnosticsRetentionDays - 1))
	if _, err := d.conn.Exec(`UPDATE diagnostics_bundles SET collected_at = ? WHERE id = 'edge'`, justInside); err != nil {
		t.Fatalf("ageing: %v", err)
	}
	if _, err := d.PruneDiagnosticsBundles(); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	edge, err := d.GetDiagnosticsBundle("edge")
	if err != nil {
		t.Fatalf("reading the boundary bundle: %v", err)
	}
	if edge == nil {
		t.Error("a bundle inside the retention window was swept")
	}
}

func TestAMissingBundleIsNotAnError(t *testing.T) {
	d := setupTestDB(t)
	got, err := d.GetDiagnosticsBundle("nope")
	if err != nil {
		t.Fatalf("a missing bundle returned an error: %v", err)
	}
	if got != nil {
		t.Fatalf("a missing bundle returned %v", got)
	}
}
