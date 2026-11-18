package server

import (
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2517: an SSO sign-in through a provider that does not perform MFA itself asks an account that
// enrolled the gateway's TOTP for its code before issuing a session, as a magic-link sign-in does.

// enrolledTOTP gives the stub's account a TOTP secret and returns a code valid now.
func enrolledTOTP(t *testing.T, srv *Server, email string) string {
	t.Helper()
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: db.UserStatusApproved, AuthMethod: "sso - stub",
		TOTPSecret: secret, TOTPEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	padded := secret + strings.Repeat("=", (8-len(secret)%8)%8)
	key, err := base32.StdEncoding.DecodeString(padded)
	if err != nil {
		t.Fatal(err)
	}
	return calculateTOTP(key, uint64(time.Now().Unix()/30))
}

func pendingCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == ssoMFACookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func verifyWithCookie(srv *Server, pending *http.Cookie, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://tunnel.example.com/api/auth/mfa-verify",
		strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if pending != nil {
		req.AddCookie(pending)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestSSOSignInAsksAnEnrolledAccountForItsTOTP(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st) // the stub provider does not set satisfies_mfa
	code := enrolledTOTP(t, srv, st.idTokenEmail())

	cookies, state := startSignIn(t, srv, st)
	rec := finishSignIn(srv, cookies, state)
	if hasSession(rec) {
		t.Fatal("an account with TOTP enrolled got a session from SSO alone, through a provider that does not perform MFA")
	}
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin?sso_mfa=1" {
		t.Fatalf("want a redirect to the V1 code prompt, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	pending := pendingCookie(rec)
	if pending == nil || !pending.HttpOnly || pending.Path != ssoMFACookiePath {
		t.Fatalf("the pending sign-in cookie is missing, readable by the page, or not scoped to %s: %+v", ssoMFACookiePath, pending)
	}

	if bad := verifyWithCookie(srv, pending, "000000"); bad.Code != http.StatusUnauthorized || hasSession(bad) {
		t.Errorf("a wrong code: %d, session %v", bad.Code, hasSession(bad))
	}
	if none := verifyWithCookie(srv, nil, code); none.Code != http.StatusUnauthorized || hasSession(none) {
		t.Errorf("the right code without the pending sign-in: %d, session %v", none.Code, hasSession(none))
	}

	ok := verifyWithCookie(srv, pending, code)
	if ok.Code != http.StatusOK || !hasSession(ok) {
		t.Fatalf("the right code: %d %s, session %v", ok.Code, ok.Body.String(), hasSession(ok))
	}
	data := sessionFrom(t, srv, ok)
	if data.AuthProvider != "stub" {
		t.Errorf("the session's AuthProvider = %q, want the SSO provider it came through", data.AuthProvider)
	}
	cleared := false
	for _, c := range ok.Result().Cookies() {
		if c.Name == ssoMFACookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the pending sign-in cookie was not cleared after the code was accepted")
	}

	// Single use: the same pending sign-in cannot be redeemed twice.
	if again := verifyWithCookie(srv, pending, code); again.Code != http.StatusUnauthorized || hasSession(again) {
		t.Errorf("the pending sign-in worked twice: %d", again.Code)
	}
}

func TestAnExpiredSSOChallengeIsRefused(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	code := enrolledTOTP(t, srv, st.idTokenEmail())
	cookies, state := startSignIn(t, srv, st)
	pending := pendingCookie(finishSignIn(srv, cookies, state))
	if pending == nil {
		t.Fatal("no challenge was started")
	}
	key := "pre_auth_" + pending.Value
	v, _ := srv.portalMap.Load(key)
	pre := v.(PortalSessionData)
	pre.ExpiresAt = time.Now().Add(-time.Second)
	srv.portalMap.Store(key, pre)
	if rec := verifyWithCookie(srv, pending, code); rec.Code != http.StatusUnauthorized || hasSession(rec) {
		t.Errorf("an expired challenge: %d, session %v", rec.Code, hasSession(rec))
	}
}

func TestSSOSignInIsNotChallengedWhenItNeedNotBe(t *testing.T) {
	t.Run("the provider performs MFA itself", func(t *testing.T) {
		st := newOIDCStub(t)
		srv := ssoTestServer(t, st)
		srv.cfg.SSOProviders[0].SatisfiesMFA = true
		enrolledTOTP(t, srv, st.idTokenEmail())
		cookies, state := startSignIn(t, srv, st)
		if rec := finishSignIn(srv, cookies, state); !hasSession(rec) || pendingCookie(rec) != nil {
			t.Errorf("a satisfies_mfa provider was challenged: %d %q", rec.Code, rec.Header().Get("Location"))
		}
	})
	t.Run("the account has no TOTP", func(t *testing.T) {
		st := newOIDCStub(t)
		srv := ssoTestServer(t, st)
		cookies, state := startSignIn(t, srv, st)
		if rec := finishSignIn(srv, cookies, state); !hasSession(rec) || pendingCookie(rec) != nil {
			t.Errorf("an account without TOTP was challenged: %d %q", rec.Code, rec.Header().Get("Location"))
		}
	})
}

func TestTheChallengeReturnsToThePortalItStartedFrom(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	enrolledTOTP(t, srv, st.idTokenEmail())
	cookies, state := startSignIn(t, srv, st)
	for _, c := range cookies {
		switch c.Name {
		case ssoPortalCookie:
			c.Value = ssoPortalV2
		case ssoCLICookie:
			c.Value = "1"
		}
	}
	rec := finishSignIn(srv, cookies, state)
	if got := rec.Header().Get("Location"); got != "/portalv2/login?sso_mfa=1&cli_login=1" {
		t.Errorf("challenge redirect = %q, want V2's prompt with the CLI marker", got)
	}
}
