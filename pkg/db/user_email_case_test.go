package db

import (
	"testing"
	"time"
)

// #2466: an email is one identity whatever its case. Rows written before every path lowercased --
// or from a mixed-case owner.user_id -- must still be found.
func TestGetUserByEmailIgnoresCase(t *testing.T) {
	database := setupTestDB(t)
	if err := database.CreateUser(&User{ID: "Mixed.Case@Example.com", Email: "Mixed.Case@Example.com", Role: "user", Status: UserStatusApproved}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	u, err := database.GetUserByEmail("mixed.case@example.com")
	if err != nil || u == nil {
		t.Fatalf("lookup in another case found nothing: %v", err)
	}
	if u.ID != "Mixed.Case@Example.com" {
		t.Errorf("found %q", u.ID)
	}
}

// Where case duplicates already exist, the lookup must be deterministic and return the original
// account -- the oldest -- rather than whichever row SQLite happens to reach first.
func TestGetUserByEmailPrefersTheOldestCaseDuplicate(t *testing.T) {
	database := setupTestDB(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, u := range []*User{
		{ID: "dup@example.com", Email: "dup@example.com", Role: "user", Status: UserStatusApproved, CreatedAt: old.Add(time.Hour)},
		{ID: "Dup@Example.com", Email: "Dup@Example.com", Role: "owner", Status: UserStatusApproved, CreatedAt: old},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatalf("seeding %s: %v", u.ID, err)
		}
	}
	u, err := database.GetUserByEmail("DUP@EXAMPLE.COM")
	if err != nil || u == nil {
		t.Fatalf("lookup: %v", err)
	}
	if u.ID != "Dup@Example.com" {
		t.Errorf("got %q, want the oldest row Dup@Example.com", u.ID)
	}
}

// The startup report names every address split across case duplicates, and nothing else.
func TestEmailCaseDuplicatesNamesSplitAccounts(t *testing.T) {
	database := setupTestDB(t)
	for _, e := range []string{"Split@Example.com", "split@example.com", "alone@example.com", "Other@x.test", "OTHER@x.test"} {
		if err := database.CreateUser(&User{ID: e, Email: e, Role: "user", Status: UserStatusApproved}); err != nil {
			t.Fatalf("seeding %s: %v", e, err)
		}
	}
	got, err := database.EmailCaseDuplicates()
	if err != nil {
		t.Fatalf("EmailCaseDuplicates: %v", err)
	}
	if len(got) != 2 || got[0] != "other@x.test" || got[1] != "split@example.com" {
		t.Errorf("got %q, want [other@x.test split@example.com]", got)
	}
}
