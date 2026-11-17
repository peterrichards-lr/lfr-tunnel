package db

import (
	"testing"
	"time"
)

// #2482: a session remembers the SSO provider that created it, and a later write that does not
// carry it -- the sliding-expiry refresh writes the whole row back -- does not erase it.
func TestPortalSessionKeepsItsAuthProvider(t *testing.T) {
	database := setupTestDB(t)
	sess := &PortalSession{TokenHash: "h-2482", Email: "a@example.com", ExpiresAt: time.Now().Add(time.Hour), AuthProvider: "liferay"}
	if err := database.UpsertPortalSession(sess); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	refresh := &PortalSession{TokenHash: "h-2482", Email: "a@example.com", ExpiresAt: time.Now().Add(2 * time.Hour)}
	if err := database.UpsertPortalSession(refresh); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err := database.GetPortalSession("h-2482")
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.AuthProvider != "liferay" {
		t.Errorf("auth provider %q after a refresh, want liferay", got.AuthProvider)
	}
}
