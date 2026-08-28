package db

import (
	"testing"
)

// Routine page-view entries are analytics, not administrative or security events. Dozens of
// near-identical rows bury the events someone opened the audit log to find (#1208).
func setupAuditRepo(t *testing.T) *SQLiteAuditRepo {
	t.Helper()
	database := setupTestDB(t)
	return NewSQLiteAuditRepo(database.conn)
}

func writeEntries(t *testing.T, repo *SQLiteAuditRepo) {
	t.Helper()
	entries := []AuditEntry{
		{ActorID: "someone@example.com", Action: ActionPortalVisit, TargetType: "portal", TargetID: "v2", Details: "visit"},
		{ActorID: "someone@example.com", Action: ActionPortalVisit, TargetType: "portal", TargetID: "v2", Details: "visit"},
		{ActorID: "admin@example.com", Action: "user.promote", TargetType: "user", TargetID: "someone@example.com", Details: "promoted"},
	}
	for i := range entries {
		if err := repo.WriteAuditEntry(&entries[i]); err != nil {
			t.Fatalf("failed to write audit entry: %v", err)
		}
	}
}

func TestListAuditEntries_HidesRoutineVisitsByDefault(t *testing.T) {
	repo := setupAuditRepo(t)
	writeEntries(t, repo)

	got, err := repo.ListAuditEntries(AuditFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range got {
		if e.Action == ActionPortalVisit {
			t.Errorf("a routine visit should not appear by default, got %+v", e)
		}
	}
	if len(got) != 1 {
		t.Errorf("expected only the real event to remain, got %d entries", len(got))
	}
}

// Hidden must not mean unreachable -- the entries still back the portal usage metric, and
// an operator investigating access should be able to see them.
func TestListAuditEntries_RoutineVisitsRemainReachable(t *testing.T) {
	repo := setupAuditRepo(t)
	writeEntries(t, repo)

	optedIn, err := repo.ListAuditEntries(AuditFilter{IncludeRoutine: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(optedIn) != 3 {
		t.Errorf("expected all three entries when opting in, got %d", len(optedIn))
	}

	byAction, err := repo.ListAuditEntries(AuditFilter{Action: ActionPortalVisit})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(byAction) != 2 {
		t.Errorf("expected filtering for the action to return both visits, got %d", len(byAction))
	}
}

// The exclusion must not swallow anything else -- only this one action is routine.
func TestListAuditEntries_OtherActionsAreUnaffected(t *testing.T) {
	repo := setupAuditRepo(t)
	writeEntries(t, repo)

	got, err := repo.ListAuditEntries(AuditFilter{Action: "user.promote"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected the promotion to be returned, got %d entries", len(got))
	}
}
