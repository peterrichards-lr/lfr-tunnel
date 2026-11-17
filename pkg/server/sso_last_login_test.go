package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #2490: SSO users read "Last Seen: Never" in the admin Users list however often they signed in.
// Only approveOnSSOSignIn wrote last_login_at, and it runs only for an account that was not yet
// approved -- never for a new SSO account (created approved) or a returning one.

func ssoSignIn(t *testing.T, srv *Server, st *oidcStub) *httptest.ResponseRecorder {
	t.Helper()
	cookies, state := startSignIn(t, srv, st)
	rec := finishSignIn(srv, cookies, state)
	if rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("SSO sign-in: %d, session %v (%s)", rec.Code, hasSession(rec), rec.Body.String())
	}
	return rec
}

func sessionFrom(t *testing.T, srv *Server, rec *httptest.ResponseRecorder) PortalSessionData {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lfr_session" && c.Value != "" {
			if data, ok := srv.sessionStore().loadPortalSession(c.Value); ok {
				return data
			}
		}
	}
	t.Fatal("no stored portal session for the sign-in")
	return PortalSessionData{}
}

func TestSSOSignInRecordsLastLogin(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)

	// First sign-in creates the account, already approved -- the path that never stamped it.
	before := time.Now().UTC().Add(-time.Second)
	first := ssoSignIn(t, srv, st)
	user, err := srv.db.GetUserByEmail(st.idTokenEmail())
	if err != nil {
		t.Fatalf("the SSO account was not created: %v", err)
	}
	if user.LastLoginAt == nil || user.LastLoginAt.Before(before) {
		t.Fatalf("a new SSO account's last_login_at = %v, want this sign-in -- the admin list shows \"Never\"", user.LastLoginAt)
	}
	if user.LastLoginIP == "" {
		t.Error("last_login_ip was not recorded")
	}
	if prev := sessionFrom(t, srv, first).PreviousLoginAt; prev != nil {
		t.Errorf("a first sign-in has no previous one, but the session carries %v", prev)
	}
	firstLogin := *user.LastLoginAt

	// A returning, already-approved user: last_login_at moves on, and the session carries the
	// previous sign-in for the portal's "last login" notice, as a magic-link session does.
	time.Sleep(10 * time.Millisecond)
	second := ssoSignIn(t, srv, st)
	user, err = srv.db.GetUserByEmail(st.idTokenEmail())
	if err != nil {
		t.Fatal(err)
	}
	if user.LastLoginAt == nil || !user.LastLoginAt.After(firstLogin) {
		t.Errorf("a returning SSO user's last_login_at = %v, want later than the first sign-in %v", user.LastLoginAt, firstLogin)
	}
	if prev := sessionFrom(t, srv, second).PreviousLoginAt; prev == nil || !prev.Equal(firstLogin) {
		t.Errorf("session PreviousLoginAt = %v, want the first sign-in %v", prev, firstLogin)
	}
}
