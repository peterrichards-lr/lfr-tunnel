package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// ssoServerWithMail is ssoTestServer with a mailer and an admin address, so admin alerts can be
// observed. The stub provider asserts sso.person@example.com, given_name "Sso", family_name "Person".
func ssoServerWithMail(t *testing.T, st *oidcStub) (*Server, *mockMailSender) {
	t.Helper()
	srv, mail, cleanup := setupTestServer(t)
	t.Cleanup(cleanup)
	srv.cfg.AdminNotificationEmail = "admin@example.com"
	srv.cfg.SSOProviders = []config.SSOProviderConfig{{
		ID: "stub", Name: "Stub", ClientID: "c", ClientSecret: "s", IssuerURL: st.srv.URL,
	}}
	return srv, mail
}

func signInOnce(t *testing.T, srv *Server, st *oidcStub) {
	t.Helper()
	cookies, state := startSignIn(t, srv, st)
	if rec := finishSignIn(srv, cookies, state); rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body.String())
	}
}

// ssoAlerts counts admin alerts about SSO accounts, waiting briefly: alerts are sent asynchronously.
func ssoAlerts(mail *mockMailSender, settle time.Duration) int {
	count := func() int {
		n := 0
		for _, e := range mail.getSentEmails() {
			if e.To == "admin@example.com" && strings.Contains(e.Subject, "New User Registration") {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(settle)
	for count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return count()
}

// #2481: a first SSO sign-in's account gets the preferred name email signup would give it.
func TestSSOFirstSignInDefaultsThePreferredNameToGivenName(t *testing.T) {
	st := newOIDCStub(t)
	srv, _ := ssoServerWithMail(t, st)
	signInOnce(t, srv, st)

	u, err := srv.db.GetUserByEmail("sso.person@example.com")
	if err != nil || u == nil {
		t.Fatalf("no account created: %v", err)
	}
	if u.PreferredName != "Sso" {
		t.Errorf("preferred name %q, want the given_name %q", u.PreferredName, "Sso")
	}
}

// #2481: admins hear about an account SSO created -- once, when it is created -- and never about
// ordinary sign-ins, which would be noise.
func TestSSOAccountCreationAlertsAdminsOnce(t *testing.T) {
	st := newOIDCStub(t)
	srv, mail := ssoServerWithMail(t, st)

	signInOnce(t, srv, st)
	if n := ssoAlerts(mail, 2*time.Second); n != 1 {
		t.Fatalf("%d admin alerts after the account was created, want 1", n)
	}
	var body string
	for _, e := range mail.getSentEmails() {
		if strings.Contains(e.Subject, "New User Registration") {
			body = e.HtmlBody + e.TextBody
		}
	}
	for _, want := range []string{"sso.person@example.com", "Stub"} {
		if !strings.Contains(body, want) {
			t.Errorf("the alert does not mention %q:\n%s", want, body)
		}
	}

	signInOnce(t, srv, st) // the same person again
	if n := ssoAlerts(mail, 300*time.Millisecond); n != 1 {
		t.Errorf("%d admin alerts after a second sign-in, want still 1", n)
	}
}

// An existing account signing in through SSO for the first time is not a new registration.
func TestSSOSignInByAnExistingAccountSendsNoAlert(t *testing.T) {
	st := newOIDCStub(t)
	srv, mail := ssoServerWithMail(t, st)
	if err := srv.db.CreateUser(&db.User{ID: "sso.person@example.com", Email: "sso.person@example.com",
		Role: "user", Status: db.UserStatusApproved}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	signInOnce(t, srv, st)
	if n := ssoAlerts(mail, 300*time.Millisecond); n != 0 {
		t.Errorf("%d admin alerts for an existing account, want 0", n)
	}
}
