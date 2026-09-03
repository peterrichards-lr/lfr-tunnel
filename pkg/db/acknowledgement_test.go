package db

import (
	"testing"
	"time"
)

func seedAckUser(t *testing.T, database *DB, id string) {
	t.Helper()
	if err := database.CreateUser(&User{
		ID:     id,
		Email:  id + "@example.com",
		Role:   "user",
		Status: "approved",
	}); err != nil {
		t.Fatalf("creating user %s: %v", id, err)
	}
}

// The acceptance history is append-only, and that is the property the whole feature rests
// on: "what did this user agree to, and when" has to stay answerable after the document
// changes. A second acceptance of the SAME version must add a row rather than replace one,
// because re-accepting is an event with its own time.
func TestAcknowledgementHistoryIsAppendOnly(t *testing.T) {
	database := setupTestDB(t)
	seedAckUser(t, database, "u1")

	first := time.Now().UTC().Add(-48 * time.Hour)
	second := time.Now().UTC().Add(-1 * time.Hour)

	for _, at := range []time.Time{first, second} {
		if err := database.RecordAcknowledgement(&Acknowledgement{
			UserID: "u1", DocumentID: "privacy_policy", Version: "1",
			AcceptedAt: at, IP: "10.0.0.1", UserAgent: "test",
		}); err != nil {
			t.Fatalf("recording acknowledgement: %v", err)
		}
	}
	// A different version, so the history spans more than one document edition.
	if err := database.RecordAcknowledgement(&Acknowledgement{
		UserID: "u1", DocumentID: "privacy_policy", Version: "2",
		AcceptedAt: time.Now().UTC(), IP: "10.0.0.2",
	}); err != nil {
		t.Fatalf("recording v2: %v", err)
	}

	history, err := database.ListAcknowledgements("u1")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 rows kept, got %d -- an overwrite would leave fewer", len(history))
	}
	// Newest first, and the older acceptance of v1 must still be there with its own time.
	if history[0].Version != "2" {
		t.Errorf("expected the newest row first, got version %q", history[0].Version)
	}
	var sawFirst bool
	for _, a := range history {
		if a.Version == "1" && a.AcceptedAt.UTC().Truncate(time.Second).Equal(first.Truncate(time.Second)) {
			sawFirst = true
			if a.IP != "10.0.0.1" {
				t.Errorf("expected the original IP preserved, got %q", a.IP)
			}
		}
	}
	if !sawFirst {
		t.Error("the earliest acceptance was lost; the history is not append-only")
	}
}

func TestHasAcknowledgedIsPerVersion(t *testing.T) {
	database := setupTestDB(t)
	seedAckUser(t, database, "u2")

	if err := database.RecordAcknowledgement(&Acknowledgement{
		UserID: "u2", DocumentID: "privacy_policy", Version: "1", AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	ok, err := database.HasAcknowledged("u2", "privacy_policy", "1")
	if err != nil || !ok {
		t.Fatalf("expected v1 accepted, got %v (err %v)", ok, err)
	}
	// The point of the whole issue: a new version is NOT covered by an old acceptance.
	ok, err = database.HasAcknowledged("u2", "privacy_policy", "2")
	if err != nil {
		t.Fatalf("checking v2: %v", err)
	}
	if ok {
		t.Error("v2 reported as accepted on the strength of a v1 acceptance")
	}
}

// The grace window runs from the user's own first sight, so the stamp must never move
// once set -- a second call happens on every /api/me poll and every tunnel start.
func TestRecordFirstSeenIsIdempotent(t *testing.T) {
	database := setupTestDB(t)
	seedAckUser(t, database, "u3")

	original := time.Now().UTC().Add(-72 * time.Hour)
	got, err := database.RecordFirstSeen("u3", "privacy_policy", "2", original)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if !got.Truncate(time.Second).Equal(original.Truncate(time.Second)) {
		t.Fatalf("expected the supplied instant back, got %v", got)
	}

	again, err := database.RecordFirstSeen("u3", "privacy_policy", "2", time.Now().UTC())
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if !again.Truncate(time.Second).Equal(original.Truncate(time.Second)) {
		t.Errorf("first sight moved to %v; a later call must not restart the grace window", again)
	}

	// A different version is a different question and starts its own window.
	other, err := database.GetFirstSeen("u3", "privacy_policy", "3")
	if err != nil {
		t.Fatalf("reading an unseen version: %v", err)
	}
	if !other.IsZero() {
		t.Errorf("expected the zero time for a version never shown, got %v", other)
	}
}

// The warning email must go out once. MarkWarningNotified is the compare-and-set that
// makes the sweep safe to run on every tick.
func TestMarkWarningNotifiedClaimsOnce(t *testing.T) {
	database := setupTestDB(t)
	seedAckUser(t, database, "u4")

	seen := time.Now().UTC().Add(-10 * 24 * time.Hour)
	if _, err := database.RecordFirstSeen("u4", "privacy_policy", "2", seen); err != nil {
		t.Fatalf("recording first sight: %v", err)
	}

	pending, err := database.ListPendingWarnings("privacy_policy", "2", time.Now().UTC())
	if err != nil {
		t.Fatalf("listing pending: %v", err)
	}
	if len(pending) != 1 || pending[0] != "u4" {
		t.Fatalf("expected u4 pending a warning, got %v", pending)
	}

	claimed, err := database.MarkWarningNotified("u4", "privacy_policy", "2")
	if err != nil || !claimed {
		t.Fatalf("expected the first claim to succeed, got %v (err %v)", claimed, err)
	}
	claimed, err = database.MarkWarningNotified("u4", "privacy_policy", "2")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Error("the warning was claimed twice; the user would be emailed on every sweep")
	}

	pending, err = database.ListPendingWarnings("privacy_policy", "2", time.Now().UTC())
	if err != nil {
		t.Fatalf("listing after claim: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected nobody pending after the claim, got %v", pending)
	}
}
