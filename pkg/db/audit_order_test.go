package db

import (
	"testing"
	"time"
)

// Entries written in the same instant must come back newest id first. The Audit Log export
// numbers rows by position and both portals page through this order, so ties left to SQLite would
// let a row number mean different rows in the view and the file (#2464).
func TestListAuditEntries_BreaksTimestampTiesByIDNewestFirst(t *testing.T) {
	repo := setupAuditRepo(t)
	same := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	var ids []int64
	for i := 0; i < 5; i++ {
		e := AuditEntry{ActorID: "a@example.com", Action: "tie.test", CreatedAt: same}
		if err := repo.WriteAuditEntry(&e); err != nil {
			t.Fatalf("write: %v", err)
		}
		ids = append(ids, e.ID)
	}

	got, err := repo.ListAuditEntries(AuditFilter{Action: "tie.test", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("got %d entries, want %d", len(got), len(ids))
	}
	for i, e := range got {
		if want := ids[len(ids)-1-i]; e.ID != want {
			t.Fatalf("position %d is id %d, want %d (newest id first)", i, e.ID, want)
		}
	}
}
