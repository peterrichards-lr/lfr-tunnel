package db

import (
	"errors"
	"testing"
	"time"
)

// GetPATByHash must tell a token row it FOUND but could not read apart from a lookup that failed
// (#2347 review). The server answers a failed lookup with 503 -- safe only because nothing is known
// about the token at that point. A row that exists but cannot be scanned is the exception: if it
// came back as an ordinary error it would be answered 503 for a token that exists and 401 for one
// that does not, which is an existence oracle.
func TestAnUnreadableTokenRowIsNotALookupFailure(t *testing.T) {
	d := setupTestDB(t)
	if err := d.CreateUser(&User{ID: "u@example.com", Email: "u@example.com", Role: "user", Status: UserStatusApproved}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreatePAT(&PersonalAccessToken{UserID: "u@example.com", TokenHash: "h-bad", TokenPrefix: "p", Name: "n", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Out-of-band damage -- nothing in the application writes this column except as a time.Time.
	if _, err := d.conn.Exec(`UPDATE personal_access_tokens SET created_at = 'not a time' WHERE token_hash = 'h-bad'`); err != nil {
		t.Fatal(err)
	}

	_, err := d.GetPATByHash("h-bad")
	if err == nil {
		t.Fatal("the damaged row scanned cleanly, so this fixture does not exercise an unreadable row")
	}
	if !errors.Is(err, ErrRowUnreadable) {
		t.Errorf("a found-but-unreadable row must be ErrRowUnreadable, distinct from a failed lookup; got %v", err)
	}

	// The control: an absent hash is still ErrNotFound, and neither is the other.
	if _, err := d.GetPATByHash("h-absent"); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrRowUnreadable) {
		t.Errorf("an absent token must be ErrNotFound only; got %v", err)
	}
}
