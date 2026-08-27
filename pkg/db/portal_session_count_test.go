package db

import (
	"testing"
	"time"
)

// Counting active portal sessions (#1455).
//
// local_leases answers "are any tunnels attached" for a restart decision, and says nothing about
// people logged into the portal -- a drain announcement travels on the tunnel-status heartbeat,
// which a browser never sends. The two genuinely diverge: on 2026-08-27 central had zero tunnels
// attached and one active portal session at the moment of a restart.

func setupSessionRepo(t *testing.T) *SQLitePortalSessionRepo {
	t.Helper()
	database := setupTestDB(t)
	return NewSQLitePortalSessionRepo(database.conn)
}

// TestCountActivePortalSessions checks the count reflects who is actually logged in.
//
// The expiry boundary is the point: expired rows are removed on the cleanup cycle rather than
// immediately, so counting every row would overstate the number of people using the portal and
// make an operator hold off a restart for sessions that ended hours ago.
func TestCountActivePortalSessions(t *testing.T) {
	repo := setupSessionRepo(t)

	if n, err := repo.CountActivePortalSessions(); err != nil || n != 0 {
		t.Fatalf("empty table should count zero, got %d (err %v)", n, err)
	}

	active := &PortalSession{
		TokenHash: "active-1",
		Email:     "someone@example.com",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	if err := repo.UpsertPortalSession(active); err != nil {
		t.Fatalf("upsert active: %v", err)
	}

	expired := &PortalSession{
		TokenHash: "expired-1",
		Email:     "someone-else@example.com",
		ExpiresAt: time.Now().Add(-2 * time.Hour),
	}
	if err := repo.UpsertPortalSession(expired); err != nil {
		t.Fatalf("upsert expired: %v", err)
	}

	n, err := repo.CountActivePortalSessions()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 active session (the expired row must not count), got %d", n)
	}

	// After pruning, the count must not change -- pruning removes rows that were already
	// excluded, so a difference here would mean the count and the prune disagree about "expired".
	if _, err := repo.PrunePortalSessions(); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n, err := repo.CountActivePortalSessions(); err != nil || n != 1 {
		t.Errorf("count must be unchanged by pruning expired rows, got %d (err %v)", n, err)
	}
}
