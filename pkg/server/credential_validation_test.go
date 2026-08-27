package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Credential validation, asserted through BOTH PAT entry points (#1308).
//
// isValidToken and requireAdmin's PAT branch each carried their own copy of "is this credential
// currently good?" -- hash, look up, check revocation, check expiry, check the user is approved.
// Both were correct. #1304 is the evidence of what happens next: two paths resolved a portal
// session, only one checked expiry, and an expired session still resolved a user on every /api
// path routed through the other. Nobody wrote that deliberately; the two drifted because there
// were two.
//
// These tests assert the NEGATIVE cases, deliberately. A positive-path test passes happily against
// a copy that has forgotten a check, which is exactly how the drift survived.

// testUserID hands out unique ids. CreateUser inserts u.ID verbatim rather than letting SQLite
// assign one, so several users in a single test collide on the primary key unless each is given
// its own.
var testUserID = 9000

// makePAT stores a PAT for a user with the given properties and returns the raw token.
func makePAT(t *testing.T, srv *Server, status, role string, revoked bool, expires *time.Time) string {
	t.Helper()

	testUserID++
	user := &db.User{
		ID:     fmt.Sprintf("test-user-%d", testUserID),
		Email:  "pat-" + generateToken(6) + "@example.com",
		Role:   role,
		Status: status,
	}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	raw := "lfr_pat_" + generateToken(24)
	h := sha256.Sum256([]byte(raw))
	pat := &db.PersonalAccessToken{
		UserID:      user.ID,
		Name:        "test",
		TokenHash:   hex.EncodeToString(h[:]),
		TokenPrefix: "lfr_pat_",
		ExpiresAt:   expires,
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("create pat: %v", err)
	}
	if revoked {
		now := time.Now().UTC().Add(-time.Hour)
		pat.RevokedAt = &now
		if err := srv.db.RevokePAT(pat.ID); err != nil {
			t.Fatalf("revoke pat: %v", err)
		}
	}
	return raw
}

// adminAccepts reports whether requireAdmin authenticates this token, without asserting on the
// response body -- the question here is only "did this credential get in".
func adminAccepts(srv *Server, token string) bool {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/admin/anything", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	_, _, ok := srv.requireAdmin(rec, req)
	return ok
}

// TestPATRejectionIsConsistentAcrossEntryPoints is the point of #1308: a credential that one path
// refuses must be refused by the other. Before the shared validator, each path decided for itself.
func TestPATRejectionIsConsistentAcrossEntryPoints(t *testing.T) {
	srv := setupTestServerForAPI(t)

	past := time.Now().UTC().Add(-time.Hour)

	cases := []struct {
		name  string
		token string
	}{
		{"revoked", makePAT(t, srv, "approved", "admin", true, nil)},
		{"expired", makePAT(t, srv, "approved", "admin", false, &past)},
		{"user not approved", makePAT(t, srv, "pending", "admin", false, nil)},
		{"unknown token", "lfr_pat_" + generateToken(24)},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := srv.isValidToken(tc.token); ok {
				t.Errorf("isValidToken accepted a %s credential", tc.name)
			}
			if adminAccepts(srv, tc.token) {
				t.Errorf("requireAdmin accepted a %s credential", tc.name)
			}
		})
	}
}

// TestPATAcceptedWhenGood is the control. Without it the test above passes against a validator
// that rejects everything, which would be a worse bug than the one being prevented.
func TestPATAcceptedWhenGood(t *testing.T) {
	srv := setupTestServerForAPI(t)

	future := time.Now().UTC().Add(time.Hour)
	token := makePAT(t, srv, "approved", "admin", false, &future)

	if _, ok := srv.isValidToken(token); !ok {
		t.Error("isValidToken rejected a valid credential")
	}
	if !adminAccepts(srv, token) {
		t.Error("requireAdmin rejected a valid admin credential")
	}

	// A PAT with no expiry at all is valid indefinitely, which is a distinct branch from
	// "expires in the future".
	noExpiry := makePAT(t, srv, "approved", "owner", false, nil)
	if _, ok := srv.isValidToken(noExpiry); !ok {
		t.Error("isValidToken rejected a credential with no expiry")
	}
}

// TestRequireAdminStillEnforcesRole guards the one legitimate difference between the two paths.
// Folding the role check into the shared validator would have silently granted admin to every
// approved user.
func TestRequireAdminStillEnforcesRole(t *testing.T) {
	srv := setupTestServerForAPI(t)

	token := makePAT(t, srv, "approved", "user", false, nil)

	if _, ok := srv.isValidToken(token); !ok {
		t.Error("a plain user's PAT is a valid credential and isValidToken must accept it")
	}
	if adminAccepts(srv, token) {
		t.Error("requireAdmin accepted a plain user's PAT -- the role requirement was lost")
	}
}

// Edge tokens had four copies of the same comparison (#1308).

func TestAuthorisedEdgeNode(t *testing.T) {
	srv := setupTestServerForAPI(t)

	raw := "edge-token-value"
	h := sha256.Sum256([]byte(raw))
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hex.EncodeToString(h[:]), URL: "https://us.example.com"},
	})

	node, ok := srv.authorisedEdgeNode(raw)
	if !ok {
		t.Fatal("a matching edge token was rejected")
	}
	// Returning the node is the point: two call sites used to re-derive the ID separately.
	if node.ID != "edge-us" {
		t.Errorf("expected the node back so callers need not re-derive it, got %q", node.ID)
	}

	for _, bad := range []string{"", "wrong-token", hex.EncodeToString(h[:])} {
		if _, ok := srv.authorisedEdgeNode(bad); ok {
			t.Errorf("accepted a bad edge token %q", bad)
		}
	}
}

// TestAuthorisedEdgeNodeWithNoNodesConfigured — a gateway with no edges must reject every token
// rather than matching a zero value.
func TestAuthorisedEdgeNodeWithNoNodesConfigured(t *testing.T) {
	srv := setupTestServerForAPI(t)
	setEdgeNodesForTest(t, srv, nil)

	if _, ok := srv.authorisedEdgeNode("anything"); ok {
		t.Error("accepted an edge token with no edge nodes configured")
	}
	// The empty-token case matters separately: an unconfigured node has TokenHash "", and a
	// caller presenting "" must not match it.
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{{ID: "edge-broken", TokenHash: ""}})
	if _, ok := srv.authorisedEdgeNode(""); ok {
		t.Error("an empty token matched an edge node with an empty TokenHash")
	}
}

// A rotation authenticates on both tokens at once (#1491), so edges can be rolled one at a time
// rather than all together during a restart.
func TestAuthorisedEdgeNodeDuringRotation(t *testing.T) {
	srv := setupTestServerForAPI(t)

	oldRaw, newRaw := "edge-token-old", "edge-token-new"
	oldH := sha256.Sum256([]byte(oldRaw))
	newH := sha256.Sum256([]byte(newRaw))

	// Step 1 -- before the rotation starts, only the current token works.
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hex.EncodeToString(oldH[:])},
	})
	if _, ok := srv.authorisedEdgeNode(oldRaw); !ok {
		t.Error("the current token must authenticate")
	}
	if _, ok := srv.authorisedEdgeNode(newRaw); ok {
		t.Error("a token that is not configured must not authenticate")
	}

	// Step 2 -- the incoming hash is added. BOTH work, which is what removes the flag day.
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{{
		ID:                    "edge-us",
		TokenHash:             hex.EncodeToString(oldH[:]),
		AdditionalTokenHashes: []string{hex.EncodeToString(newH[:])},
	}})
	if _, ok := srv.authorisedEdgeNode(oldRaw); !ok {
		t.Error("the old token must keep working while edges are still being rolled")
	}
	if _, ok := srv.authorisedEdgeNode(newRaw); !ok {
		t.Error("the incoming token must authenticate once configured")
	}

	// Step 3 -- the old hash is removed. That is the step that actually revokes it.
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{{
		ID:                    "edge-us",
		AdditionalTokenHashes: []string{hex.EncodeToString(newH[:])},
	}})
	if _, ok := srv.authorisedEdgeNode(oldRaw); ok {
		t.Error("the old token must stop working once removed -- otherwise the rotation revokes nothing")
	}
	if _, ok := srv.authorisedEdgeNode(newRaw); !ok {
		t.Error("the new token must still authenticate after the old one is removed")
	}

	// An empty token must never match, including against the now-empty TokenHash field.
	if _, ok := srv.authorisedEdgeNode(""); ok {
		t.Error("an empty token matched a node whose TokenHash is empty")
	}
}
