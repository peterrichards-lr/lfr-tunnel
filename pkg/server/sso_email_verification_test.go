package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// #2472: email_verification decides who vouches for the address an ID token asserts. "gateway"
// accepts email_verified: true, and otherwise emails a one-time link and finishes the sign-in only
// when it is used.

func gatewayModeServer(t *testing.T, st *oidcStub) (*Server, *mockMailSender) {
	t.Helper()
	srv := ssoTestServer(t, st)
	srv.cfg.SSOProviders[0].EmailVerification = config.EmailVerificationGateway
	m := &mockMailSender{}
	srv.notifications = NewNotificationService(m, srv.db, srv.cfg)
	return srv, m
}

var confirmTokenRe = regexp.MustCompile(`sso-confirm\?token=([0-9a-f]+)`)

// confirmationToken waits for the (asynchronous) confirmation email to addr and returns its token.
func confirmationToken(t *testing.T, m *mockMailSender, addr string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range m.getSentEmails() {
			if e.To == addr {
				if g := confirmTokenRe.FindStringSubmatch(e.TextBody + e.HtmlBody); g != nil {
					return g[1]
				}
				t.Fatalf("the confirmation email carries no link:\n%s", e.TextBody)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no confirmation email reached %s", addr)
	return ""
}

func serveConfirm(srv *Server, method, token string) *httptest.ResponseRecorder {
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(http.MethodGet, "http://tunnel.example.com"+ssoConfirmPath+"?token="+url.QueryEscape(token), nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "http://tunnel.example.com"+ssoConfirmPath, strings.NewReader("token="+url.QueryEscape(token)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func attemptSignIn(t *testing.T, srv *Server, st *oidcStub) *httptest.ResponseRecorder {
	t.Helper()
	cookies, state := startSignIn(t, srv, st)
	return finishSignIn(srv, cookies, state)
}

func TestGatewayModeSignsInAVerifiedAddressDirectly(t *testing.T) {
	st := newOIDCStub(t)
	srv, m := gatewayModeServer(t, st)
	if rec := attemptSignIn(t, srv, st); rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("email_verified true: want a session, got %d (%s)", rec.Code, rec.Body.String())
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(m.getSentEmails()); n != 0 {
		t.Errorf("a verified address was sent %d confirmation email(s)", n)
	}
}

func TestGatewayModeConfirmsAnUnverifiedAddressByEmail(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verified *bool
	}{
		{"email_verified false", new(bool)},
		{"email_verified absent", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newOIDCStub(t)
			st.emailVerified = tc.verified
			srv, m := gatewayModeServer(t, st)
			addr := st.idTokenEmail()

			rec := attemptSignIn(t, srv, st)
			if hasSession(rec) {
				t.Fatal("an unconfirmed address was given a session")
			}
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
				t.Fatalf("want the check-your-email page, got %d (%s)", rec.Code, rec.Body.String())
			}
			if _, err := srv.db.GetUserByEmail(addr); err != db.ErrNotFound {
				t.Errorf("an account was created before the address was confirmed (err %v)", err)
			}
			token := confirmationToken(t, m, addr)

			// Opening the link -- or a mail scanner fetching it -- does not spend it.
			for i := 0; i < 2; i++ {
				if g := serveConfirm(srv, http.MethodGet, token); g.Code != http.StatusOK || !strings.Contains(g.Body.String(), `method="post"`) || hasSession(g) {
					t.Fatalf("GET %d: want the confirm button and no session, got %d (%s)", i, g.Code, g.Body.String())
				}
			}

			done := serveConfirm(srv, http.MethodPost, token)
			if done.Code != http.StatusFound || !hasSession(done) || done.Header().Get("Location") != "/portal/dashboard" {
				t.Fatalf("confirming: want a session and a redirect to /portal/dashboard, got %d %q (%s)", done.Code, done.Header().Get("Location"), done.Body.String())
			}
			user, err := srv.db.GetUserByEmail(addr)
			if err != nil {
				t.Fatalf("no account after confirming: %v", err)
			}
			if ok, err := srv.db.EmailConfirmed(user.ID); err != nil || !ok {
				t.Errorf("the confirmation was not recorded (%v, %v)", ok, err)
			}

			// Single use.
			if again := serveConfirm(srv, http.MethodPost, token); again.Code != http.StatusGone || hasSession(again) {
				t.Errorf("a used link worked again: %d", again.Code)
			}

			// Recorded on the account: the next sign-in does not ask again.
			before := len(m.getSentEmails())
			if next := attemptSignIn(t, srv, st); next.Code != http.StatusFound || !hasSession(next) {
				t.Errorf("a confirmed account was asked again: %d (%s)", next.Code, next.Body.String())
			}
			time.Sleep(100 * time.Millisecond)
			if after := len(m.getSentEmails()); after != before {
				t.Errorf("a confirmed account was sent another email")
			}
		})
	}
}

func TestGatewayModeReturnsToThePortalTheSignInStartedFrom(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = new(bool)
	srv, m := gatewayModeServer(t, st)
	cookies, state := startSignIn(t, srv, st)
	for _, c := range cookies {
		if c.Name == ssoCLICookie {
			c.Value = "1"
		}
	}
	finishSignIn(srv, cookies, state)
	done := serveConfirm(srv, http.MethodPost, confirmationToken(t, m, st.idTokenEmail()))
	if got := done.Header().Get("Location"); got != "/portal/dashboard?cli_login=1" {
		t.Errorf("confirmed sign-in went to %q, want the V2 dashboard with the CLI marker", got)
	}
}

func TestGatewayModeTrustsAnAddressAlreadyProvenHere(t *testing.T) {
	for _, tc := range []struct {
		method string
		proven bool
	}{
		{"registration", true},
		{"Magic Link", true},
		{"invite", false},
		{"sso - stub", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			st := newOIDCStub(t)
			st.emailVerified = new(bool)
			srv, m := gatewayModeServer(t, st)
			addr := st.idTokenEmail()
			if err := srv.db.CreateUser(&db.User{ID: addr, Email: addr, Role: "user", Status: db.UserStatusApproved, AuthMethod: tc.method}); err != nil {
				t.Fatal(err)
			}
			rec := attemptSignIn(t, srv, st)
			if hasSession(rec) != tc.proven {
				t.Errorf("%s account: session = %v, want %v (%d %s)", tc.method, hasSession(rec), tc.proven, rec.Code, rec.Body.String())
			}
			if !tc.proven {
				confirmationToken(t, m, addr)
			}
		})
	}
}

func TestGatewayModeChecksTheDomainBeforeSendingAnything(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = new(bool)
	srv, m := gatewayModeServer(t, st)
	srv.cfg.AllowedEmailDomains = []string{"liferay.com"}
	if rec := attemptSignIn(t, srv, st); hasSession(rec) || rec.Code != http.StatusForbidden {
		t.Errorf("an address outside allowed_email_domains: %d", rec.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(m.getSentEmails()); n != 0 {
		t.Errorf("an address outside allowed_email_domains was emailed (%d)", n)
	}
}

func TestGatewayModeLinkExpires(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = new(bool)
	srv, m := gatewayModeServer(t, st)
	attemptSignIn(t, srv, st)
	token := confirmationToken(t, m, st.idTokenEmail())
	key := ssoConfirmKey(token)
	v, _ := srv.portalMap.Load(key)
	pending := v.(pendingSSOConfirmation)
	pending.ExpiresAt = time.Now().Add(-time.Second)
	srv.portalMap.Store(key, pending)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rec := serveConfirm(srv, method, token); rec.Code != http.StatusGone || hasSession(rec) {
			t.Errorf("%s on an expired link = %d, want 410 and no session", method, rec.Code)
		}
	}
	if rec := serveConfirm(srv, http.MethodPost, "not-a-token"); rec.Code != http.StatusGone || hasSession(rec) {
		t.Errorf("an unknown token = %d", rec.Code)
	}
}

func TestEmailVerificationModes(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		verified *bool
		session  bool
	}{
		{config.EmailVerificationProvider, new(bool), false},
		{config.EmailVerificationProvider, nil, false},
		{config.EmailVerificationTrust, new(bool), true},
		{config.EmailVerificationTrust, nil, true},
	} {
		st := newOIDCStub(t)
		st.emailVerified = tc.verified
		srv := ssoTestServer(t, st)
		srv.cfg.SSOProviders[0].EmailVerification = tc.mode
		if rec := attemptSignIn(t, srv, st); hasSession(rec) != tc.session {
			t.Errorf("%s, email_verified %v: session = %v, want %v", tc.mode, tc.verified, hasSession(rec), tc.session)
		}
	}
}

// A magic-link sign-in proves the mailbox, so an invited user who has used one is not asked to
// confirm the address again through SSO.
func TestAMagicLinkSignInRecordsTheConfirmedAddress(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = new(bool)
	srv, m := gatewayModeServer(t, st)
	addr := st.idTokenEmail()
	token := issueLink(t, srv, addr)
	u, err := srv.db.GetUser(addr)
	if err != nil {
		t.Fatal(err)
	}
	u.AuthMethod = "invite"
	if err := srv.db.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if rec := verifyRequest(srv, token); rec.Code != http.StatusOK {
		t.Fatalf("magic link: %d %s", rec.Code, rec.Body.String())
	}
	if ok, err := srv.db.EmailConfirmed(addr); err != nil || !ok {
		t.Fatalf("a magic-link sign-in did not record the address (%v, %v)", ok, err)
	}
	if rec := attemptSignIn(t, srv, st); !hasSession(rec) {
		t.Errorf("an invited user who used a magic link was asked to confirm again: %d", rec.Code)
	}
	time.Sleep(100 * time.Millisecond)
	for _, e := range m.getSentEmails() {
		if e.To == addr && strings.Contains(e.Subject, "Confirm your email") {
			t.Error("a confirmation email was sent")
		}
	}
}

// The link in the email points at the gateway's own host, never one the request named: a link to
// an attacker's host would hand them the token, and with it a session as the address's owner.
func TestGatewayModeLinkIgnoresARequestedHost(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = new(bool)
	srv, m := gatewayModeServer(t, st)
	srv.cfg.Domains = []string{"example.com"}
	cookies, state := startSignIn(t, srv, st)
	req := httptest.NewRequest(http.MethodGet, "http://evil.test/api/auth/callback?provider=stub&code=the-code&state="+url.QueryEscape(state), nil)
	req.Header.Set("X-Forwarded-Host", "evil.test")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	srv.handleSSOCallback(httptest.NewRecorder(), req)
	confirmationToken(t, m, st.idTokenEmail())
	for _, e := range m.getSentEmails() {
		if strings.Contains(e.TextBody+e.HtmlBody, "evil.test") {
			t.Fatalf("the confirmation link names the requested host:\n%s", e.TextBody)
		}
		if !strings.Contains(e.TextBody+e.HtmlBody, "https://tunnel.example.com"+ssoConfirmPath) {
			t.Errorf("the confirmation link is not on the gateway's host:\n%s", e.TextBody)
		}
	}
}
