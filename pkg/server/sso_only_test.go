package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// With disable_email_login set, no emailed link signs anyone in or creates an account (#2449).
// Registration and requesting a link were already refused; an admin invite and redeeming a link
// were not. Each refusal has a control with the flag off, so a test cannot pass because the
// fixture was unusable rather than because the flag was honoured.

func inviteRequest(srv *Server, email string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body := `{"email":"` + email + `","first_name":"Ada","last_name":"Lovelace","language_preference":"en"}`
	srv.handleAdminInviteUser(rec, httptest.NewRequest(http.MethodPost, "/api/admin/invite", bytes.NewBufferString(body)), "owner@example.com")
	return rec
}

func TestInviteIsRefusedWhenEmailLoginIsDisabled(t *testing.T) {
	srv := setupTestServerForAPI(t)
	srv.cfg.DisableEmailLogin = true

	rec := inviteRequest(srv, "invitee@example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("invite with email login disabled: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := srv.db.GetUserByEmail("invitee@example.com"); err == nil {
		t.Error("a refused invite still created an account")
	}
	links, err := srv.db.ListMagicLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("a refused invite still issued %d sign-in link(s)", len(links))
	}
}

func TestInviteStillWorksWithEmailLogin(t *testing.T) {
	srv := setupTestServerForAPI(t)

	rec := inviteRequest(srv, "invitee@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("invite with email login on: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := srv.db.GetUserByEmail("invitee@example.com"); err != nil {
		t.Errorf("the invite did not create the account: %v", err)
	}
	links, err := srv.db.ListMagicLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Errorf("want the invite's one sign-in link, got %d", len(links))
	}
}

// issueLink stores a valid, unused magic link for an approved user and returns its raw token.
func issueLink(t *testing.T, srv *Server, email string) string {
	t.Helper()
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: db.UserStatusApproved,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	token := "test-token-" + email
	h := sha256.Sum256([]byte(token))
	if err := srv.db.CreateMagicLink(email, hex.EncodeToString(h[:]), "127.0.0.1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return token
}

func verifyRequest(srv *Server, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.handleAdminVerify(rec, httptest.NewRequest(http.MethodPost, "/api/auth/verify", bytes.NewBufferString(`{"token":"`+token+`"}`)))
	return rec
}

func TestVerifyRefusesMagicLinksWhenEmailLoginIsDisabled(t *testing.T) {
	srv := setupTestServerForAPI(t)
	// Issued while email login was still on, as a link sent just before the switch would be.
	token := issueLink(t, srv, "member@example.com")
	srv.cfg.DisableEmailLogin = true

	rec := verifyRequest(srv, token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("verify with email login disabled: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" {
			t.Errorf("a refused link still set cookie %q", c.Name)
		}
	}
}

func TestVerifyStillRedeemsMagicLinksWithEmailLogin(t *testing.T) {
	srv := setupTestServerForAPI(t)
	token := issueLink(t, srv, "member@example.com")

	rec := verifyRequest(srv, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify with email login on: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
}
