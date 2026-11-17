package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2466: an identity provider returns the email claim in whatever case its directory stores it,
// and the gateway may hold the same person under another case. Both must resolve to one account.

func usersWithEmail(t *testing.T, srv *Server, email string) []*db.User {
	t.Helper()
	all, err := srv.db.ListUsers()
	if err != nil {
		t.Fatalf("listing users: %v", err)
	}
	var out []*db.User
	for _, u := range all {
		if strings.EqualFold(u.Email, email) {
			out = append(out, u)
		}
	}
	return out
}

func TestSSOSignInFindsAnExistingUserWhoseStoredEmailDiffersInCase(t *testing.T) {
	st := newOIDCStub(t)
	st.email = "Sso.Person@Example.com" // as the directory stores it
	srv := ssoTestServer(t, st)
	// Already here, as a magic-link user written before every path lowercased.
	if err := srv.db.CreateUser(&db.User{ID: "SSO.person@example.COM", Email: "SSO.person@example.COM",
		Role: "user", Status: db.UserStatusApproved}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	cookies, state := startSignIn(t, srv, st)
	if rec := finishSignIn(srv, cookies, state); rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body.String())
	}
	if got := usersWithEmail(t, srv, "sso.person@example.com"); len(got) != 1 {
		t.Fatalf("%d accounts for one person after an SSO sign-in, want 1: %+v", len(got), got)
	}
}

// The serious form: the owner, configured in mixed case and seeded that way, signs in through SSO
// and must not be given a second account carrying the owner role.
func TestSSOSignInByAMixedCaseOwnerMakesNoSecondOwner(t *testing.T) {
	st := newOIDCStub(t)
	st.email = "owner.person@example.com"
	srv := ssoTestServer(t, st)
	srv.cfg.Owner.UserID = "Owner.Person@Example.com"
	if err := srv.db.CreateUser(&db.User{ID: "Owner.Person@Example.com", Email: "Owner.Person@Example.com",
		Role: "owner", Status: db.UserStatusApproved}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	cookies, state := startSignIn(t, srv, st)
	if rec := finishSignIn(srv, cookies, state); rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body.String())
	}
	owners := 0
	for _, u := range usersWithEmail(t, srv, "owner.person@example.com") {
		if u.Role == "owner" {
			owners++
		}
	}
	if got := len(usersWithEmail(t, srv, "owner.person@example.com")); got != 1 || owners != 1 {
		t.Fatalf("%d accounts and %d owners after the owner signed in, want 1 and 1", got, owners)
	}
}

// The magic-link handler decided "is this the owner" with ==, so a mixed-case owner.user_id lost the
// owner's exemption from the domain allowlist.
func TestMagicLinkRecognisesAMixedCaseOwner(t *testing.T) {
	srv, sender, cleanup := setupTestServer(t)
	defer cleanup()
	srv.cfg.Owner.UserID = "Owner@Elsewhere.example"
	srv.cfg.AllowedEmailDomains = []string{"example.com"}

	body := strings.NewReader(`{"email":"owner@elsewhere.example"}`)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://example.com/api/auth/magic-link", body))
	if w.Code != http.StatusOK {
		t.Fatalf("magic link: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	// Sent asynchronously (so SMTP latency cannot reveal whether an address exists): wait for it.
	for deadline := time.Now().Add(3 * time.Second); len(sender.getSentEmails()) == 0 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if len(sender.getSentEmails()) == 0 {
		t.Errorf("the owner, outside the allowed domains, got no magic link: the allowlist did not recognise them")
	}
}

// The control for the test above: a stranger outside the allowed domains really gets nothing, so
// "the owner got an email" cannot be satisfied by the allowlist simply being off.
func TestMagicLinkStillRefusesAStrangerOutsideTheAllowedDomains(t *testing.T) {
	srv, sender, cleanup := setupTestServer(t)
	defer cleanup()
	srv.cfg.Owner.UserID = "Owner@Elsewhere.example"
	srv.cfg.AllowedEmailDomains = []string{"example.com"}

	body := strings.NewReader(`{"email":"stranger@elsewhere.example"}`)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://example.com/api/auth/magic-link", body))
	time.Sleep(500 * time.Millisecond) // long past the owner case above, which arrives in milliseconds
	if n := len(sender.getSentEmails()); n != 0 {
		t.Errorf("a stranger outside the allowed domains was sent %d email(s)", n)
	}
}
