package db

import (
	"errors"
	"testing"
	"time"
)

// The permanence-request state on a Personal Access Token (#2267).
//
// The cases that matter are the ones about ABSENCE. Every token that exists on a live gateway
// today predates this column, and the honest reading of those rows is "nobody asked" -- not
// "granted", even for the ones that happen to be permanent, because marking them granted would
// invent an approval nobody gave.

func aTokenUser(t *testing.T, database *DB, id string) *User {
	t.Helper()
	u := &User{ID: id, Email: id + "@example.com", Role: "user", Status: "approved"}
	if err := database.CreateUser(u); err != nil {
		t.Fatalf("creating %s: %v", id, err)
	}
	return u
}

func aToken(t *testing.T, database *DB, userID, name string, expires *time.Time) *PersonalAccessToken {
	t.Helper()
	pat := &PersonalAccessToken{
		UserID:      userID,
		Name:        name,
		TokenHash:   "hash-" + name,
		TokenPrefix: "pfx-" + name,
		ExpiresAt:   expires,
	}
	if err := database.CreatePAT(pat); err != nil {
		t.Fatalf("creating token %s: %v", name, err)
	}
	return pat
}

// A row written with no permanence_state -- which is every row the migration touches, and any
// insert that predates the column -- must read back as "nobody asked" rather than as an error or
// an empty-but-invalid state.
func TestARowWithNoPermanenceStateReadsAsNobodyAsked(t *testing.T) {
	database := setupTestDB(t)
	aTokenUser(t, database, "dev")

	// Inserted the way a gateway running an older build would: the column is not named, so
	// only the schema's DEFAULT can supply it. A migration that added the column NULLable, or
	// without a default, fails here rather than at 3am on a live database.
	if _, err := database.conn.Exec(
		`INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, name, created_at) VALUES (?, ?, ?, ?, ?)`,
		"dev", "legacy-hash", "legacy-pfx", "legacy", time.Now().UTC()); err != nil {
		t.Fatalf("inserting a row the old way: %v", err)
	}

	pats, err := database.ListPATs("dev")
	if err != nil || len(pats) != 1 {
		t.Fatalf("listing: %v (%d)", err, len(pats))
	}
	if pats[0].PermanenceState != PATPermanenceNone {
		t.Errorf("a row that predates the column reads as %q, want %q", pats[0].PermanenceState, PATPermanenceNone)
	}
	if queue, err := database.ListPATPermanenceRequests(); err != nil || len(queue) != 0 {
		t.Errorf("a row nobody asked about is in the request queue: %v (%d)", err, len(queue))
	}
}

// An ALREADY-PERMANENT legacy token must not read as granted. It is permanent because an older
// build allowed it, not because anyone approved a request.
func TestAnExistingPermanentTokenIsNotRecordedAsGranted(t *testing.T) {
	database := setupTestDB(t)
	aTokenUser(t, database, "dev")
	pat := aToken(t, database, "dev", "forever", nil)

	got, err := database.GetPATByID(pat.ID)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if got.ExpiresAt != nil {
		t.Fatal("the fixture is wrong: this token was meant to be permanent")
	}
	if got.PermanenceState != PATPermanenceNone {
		t.Errorf("a permanent token nobody requested reads as %q; that invents an approval", got.PermanenceState)
	}
}

// The state survives a write and comes back through all three read paths. Three separate SELECT
// lists is how a column comes to be written, and read back empty from the one listing nobody
// checked -- the reason they now share patColumns.
func TestThePermanenceStateComesBackThroughEveryReadPath(t *testing.T) {
	database := setupTestDB(t)
	aTokenUser(t, database, "dev")
	expiry := time.Now().Add(24 * time.Hour).UTC()
	pat := aToken(t, database, "dev", "ci", &expiry)

	if err := database.SetPATPermanenceState(pat.ID, PATPermanencePending); err != nil {
		t.Fatalf("setting the state: %v", err)
	}

	byID, err := database.GetPATByID(pat.ID)
	if err != nil {
		t.Fatalf("GetPATByID: %v", err)
	}
	byHash, err := database.GetPATByHash(pat.TokenHash)
	if err != nil {
		t.Fatalf("GetPATByHash: %v", err)
	}
	listed, err := database.ListPATs("dev")
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListPATs: %v (%d)", err, len(listed))
	}
	all, err := database.ListAllPATs()
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAllPATs: %v (%d)", err, len(all))
	}

	for name, got := range map[string]*PersonalAccessToken{
		"GetPATByID": byID, "GetPATByHash": byHash, "ListPATs": listed[0], "ListAllPATs": all[0],
	} {
		if got.PermanenceState != PATPermanencePending {
			t.Errorf("%s returned %q, want pending", name, got.PermanenceState)
		}
	}
}

// The queue holds pending requests only, oldest first, and drops a revoked token's.
func TestTheRequestQueueHoldsOnlyLivePendingRequests(t *testing.T) {
	database := setupTestDB(t)
	aTokenUser(t, database, "dev")
	expiry := time.Now().Add(24 * time.Hour).UTC()

	pending := aToken(t, database, "dev", "pending", &expiry)
	granted := aToken(t, database, "dev", "granted", &expiry)
	denied := aToken(t, database, "dev", "denied", &expiry)
	revoked := aToken(t, database, "dev", "revoked", &expiry)
	untouched := aToken(t, database, "dev", "untouched", &expiry)

	for id, state := range map[int64]string{
		pending.ID: PATPermanencePending,
		granted.ID: PATPermanenceGranted,
		denied.ID:  PATPermanenceDenied,
		revoked.ID: PATPermanencePending,
	} {
		if err := database.SetPATPermanenceState(id, state); err != nil {
			t.Fatalf("setting state on %d: %v", id, err)
		}
	}
	if err := database.RevokePAT(revoked.ID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	queue, err := database.ListPATPermanenceRequests()
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(queue) != 1 {
		var names []string
		for _, q := range queue {
			names = append(names, q.Name)
		}
		t.Fatalf("want only the pending, live request; got %v", names)
	}
	if queue[0].ID != pending.ID {
		t.Errorf("queue holds %q, want %q", queue[0].Name, pending.Name)
	}

	// The token nobody touched is the anti-vacuity half: a query that returned everything
	// would still have satisfied "the pending one is in there".
	untouchedRow, err := database.GetPATByID(untouched.ID)
	if err != nil {
		t.Fatalf("reading the untouched token: %v", err)
	}
	if untouchedRow.PermanenceState != PATPermanenceNone {
		t.Errorf("the untouched token reads %q", untouchedRow.PermanenceState)
	}
}

// Setting the state on a token that is not there must say so rather than succeed silently --
// the failure that turns "the admin clicked grant" into "nothing happened and nobody was told".
func TestSettingTheStateOnAMissingTokenIsNotFound(t *testing.T) {
	database := setupTestDB(t)

	if err := database.SetPATPermanenceState(99999, PATPermanenceGranted); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if _, err := database.GetPATByID(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPATByID on a missing id: got %v, want ErrNotFound", err)
	}
}

// CONTROL. GetPATByID must agree with the listing it replaces for the caller that used to filter
// in Go, or the permanence flow acts on a different row than the queue displayed.
func TestGetPATByIDAgreesWithTheListing(t *testing.T) {
	database := setupTestDB(t)
	aTokenUser(t, database, "dev")
	expiry := time.Now().Add(24 * time.Hour).UTC()
	a := aToken(t, database, "dev", "a", &expiry)
	b := aToken(t, database, "dev", "b", nil)

	listed, err := database.ListPATs("dev")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	byID := map[int64]*PersonalAccessToken{}
	for _, p := range listed {
		byID[p.ID] = p
	}

	for _, want := range []*PersonalAccessToken{a, b} {
		got, err := database.GetPATByID(want.ID)
		if err != nil {
			t.Fatalf("GetPATByID(%d): %v", want.ID, err)
		}
		fromList := byID[want.ID]
		if fromList == nil {
			t.Fatalf("token %d is missing from the listing", want.ID)
		}
		if got.Name != fromList.Name || got.TokenPrefix != fromList.TokenPrefix {
			t.Errorf("token %d: by id %q/%q, from listing %q/%q", want.ID,
				got.Name, got.TokenPrefix, fromList.Name, fromList.TokenPrefix)
		}
		if (got.ExpiresAt == nil) != (fromList.ExpiresAt == nil) {
			t.Errorf("token %d: by id expires %v, from listing %v", want.ID, got.ExpiresAt, fromList.ExpiresAt)
		}
	}
}
