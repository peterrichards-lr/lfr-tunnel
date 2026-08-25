package server

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func newSessionTestStore(t *testing.T) (*portalSessionStore, *db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() }) //nolint:errcheck
	return newPortalSessionStore(&sync.Map{}, database), database, path
}

// The case the whole change exists for: a session created before a restart still
// authenticates after one. Simulated by dropping the in-memory cache while keeping the
// database, which is exactly what a process restart does.
func TestPortalSession_SurvivesARestart(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("cookie-value", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
		ClientIP:  "203.0.113.9",
	})

	// The restart: a brand-new process, same database, empty cache.
	restarted := newPortalSessionStore(&sync.Map{}, database)

	got, ok := restarted.loadPortalSession("cookie-value")
	if !ok {
		t.Fatal("the session did not survive a restart, so every logged-in user was signed out")
	}
	if got.Email != "admin@lfr-demo.se" {
		t.Errorf("email = %q, want the stored one", got.Email)
	}
	if got.ClientIP != "203.0.113.9" {
		t.Errorf("client IP = %q, want the stored one", got.ClientIP)
	}
}

// View As lives on the session, and it decides the effective role, so losing it on a restart
// would silently return an owner to full privileges mid-preview.
func TestPortalSession_ViewAsRoleSurvivesARestart(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("cookie-value", PortalSessionData{
		Email:      "owner@lfr-demo.se",
		ExpiresAt:  time.Now().Add(time.Hour),
		ViewAsRole: "user",
	})

	restarted := newPortalSessionStore(&sync.Map{}, database)
	got, ok := restarted.loadPortalSession("cookie-value")
	if !ok {
		t.Fatal("session lost across restart")
	}
	if got.ViewAsRole != "user" {
		t.Errorf("ViewAsRole = %q, want \"user\" -- a preview must not silently become full access", got.ViewAsRole)
	}
}

func TestPortalSession_ExpiredIsNotAccepted(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("stale", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	if _, ok := store.loadPortalSession("stale"); ok {
		t.Error("an expired session was accepted from the cache")
	}

	restarted := newPortalSessionStore(&sync.Map{}, database)
	if _, ok := restarted.loadPortalSession("stale"); ok {
		t.Error("an expired session was accepted from the database")
	}
}

func TestPortalSession_LogoutRemovesItEverywhere(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("cookie-value", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	store.deletePortalSession("cookie-value")

	if _, ok := store.loadPortalSession("cookie-value"); ok {
		t.Error("session still present after logout")
	}
	restarted := newPortalSessionStore(&sync.Map{}, database)
	if _, ok := restarted.loadPortalSession("cookie-value"); ok {
		t.Error("a logged-out session came back after a restart")
	}
}

// Strict concurrency: logging in elsewhere ends the older session. If that only happened in
// memory, a restart would resurrect the session that was deliberately killed and both devices
// would be logged in again.
func TestPortalSession_TakeoverSurvivesARestart(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("first-device", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if killed := store.killPortalSessionsFor("admin@lfr-demo.se"); !killed {
		t.Error("expected the takeover to report that a session was ended")
	}

	restarted := newPortalSessionStore(&sync.Map{}, database)
	if _, ok := restarted.loadPortalSession("first-device"); ok {
		t.Error("a killed session was resurrected by a restart")
	}
}

// The takeover has to be reported even when the previous session exists only in the database
// -- the case immediately after a restart, where the cache is empty but the user is still
// logged in on another device.
func TestPortalSession_TakeoverReportedWhenOnlyPersisted(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("first-device", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	restarted := newPortalSessionStore(&sync.Map{}, database)
	if killed := restarted.killPortalSessionsFor("admin@lfr-demo.se"); !killed {
		t.Error("a takeover of a session known only to the database went unreported")
	}
}

// The cookie value must never be written down; only a hash of it.
func TestPortalSession_StoresOnlyAHashOfTheToken(t *testing.T) {
	store, database, _ := newSessionTestStore(t)

	store.storePortalSession("super-secret-cookie", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if row, err := database.GetPortalSession("super-secret-cookie"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} else if row != nil {
		t.Fatal("the raw cookie value is usable as a database key, so it was stored verbatim")
	}

	row, err := database.GetPortalSession(hashSessionToken("super-secret-cookie"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row == nil {
		t.Fatal("the session was not persisted under its hash")
	}
	if row.TokenHash == "super-secret-cookie" {
		t.Error("token stored verbatim")
	}
}

// A gateway with no database at all -- an edge in stateless mode -- must still work, falling
// back to memory-only sessions exactly as before this existed.
func TestPortalSession_WorksWithoutADatabase(t *testing.T) {
	store := newPortalSessionStore(&sync.Map{}, nil)

	store.storePortalSession("cookie-value", PortalSessionData{
		Email:     "admin@lfr-demo.se",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if _, ok := store.loadPortalSession("cookie-value"); !ok {
		t.Error("a memory-only session should still authenticate within the process")
	}
	store.deletePortalSession("cookie-value")
	if _, ok := store.loadPortalSession("cookie-value"); ok {
		t.Error("logout failed without a database")
	}
}
