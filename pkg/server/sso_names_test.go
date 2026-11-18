package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2515: an SSO account's first and last name come from the identity provider on every sign-in,
// and a session that signed in through it cannot change them. Preferred Name stays the user's.

func ssoSignInSession(t *testing.T, srv *Server, st *oidcStub) string {
	t.Helper()
	cookies, state := startSignIn(t, srv, st)
	rec := finishSignIn(srv, cookies, state)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lfr_session" && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session: %d %s", rec.Code, rec.Body.String())
	return ""
}

func mustUser(t *testing.T, srv *Server, email string) *db.User {
	t.Helper()
	u, err := srv.db.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("reading %s: %v", email, err)
	}
	return u
}

func putMe(srv *Server, session, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "http://tunnel.example.com/api/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestEverySSOSignInRefreshesTheNames(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	ssoSignInSession(t, srv, st)

	// The user picks a preferred name; the directory then renames them.
	u, err := srv.db.GetUserByEmail(st.idTokenEmail())
	if err != nil {
		t.Fatal(err)
	}
	u.PreferredName = "Chosen"
	if err := srv.db.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	srv.invalidateUserCache(u.Email)
	st.givenName, st.familyName = "Renamed", "Directory"
	ssoSignInSession(t, srv, st)

	u = mustUser(t, srv, st.idTokenEmail())
	if u.FirstName != "Renamed" || u.LastName != "Directory" {
		t.Errorf("after a sign-in carrying new names the account reads %q %q", u.FirstName, u.LastName)
	}
	if u.PreferredName != "Chosen" {
		t.Errorf("the sign-in overwrote the preferred name with %q", u.PreferredName)
	}

	// A token without the claims leaves the stored names alone.
	st.omitNames = true
	ssoSignInSession(t, srv, st)
	u = mustUser(t, srv, st.idTokenEmail())
	if u.FirstName != "Renamed" || u.LastName != "Directory" {
		t.Errorf("a token with no name claims blanked the names: %q %q", u.FirstName, u.LastName)
	}
}

func TestAnSSOSessionCannotChangeTheProvidersNames(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	session := ssoSignInSession(t, srv, st)

	body := meBody(t, srv, session)
	if body["names_from_provider"] != true || body["names_provider_name"] != "Stub" {
		t.Errorf("/api/me names_from_provider=%v names_provider_name=%v, want true and the provider", body["names_from_provider"], body["names_provider_name"])
	}

	if rec := putMe(srv, session, `{"first_name":"Someone","last_name":"Else"}`); rec.Code != http.StatusConflict {
		t.Errorf("changing the provider's names: %d %s, want 409", rec.Code, rec.Body.String())
	}
	// The portals send every field on save: the same names, with a new preferred name, is fine.
	if rec := putMe(srv, session, `{"first_name":"Sso","last_name":"Person","preferred_name":"Pref"}`); rec.Code != http.StatusOK {
		t.Errorf("saving unchanged names with a new preferred name: %d %s", rec.Code, rec.Body.String())
	}
	u := mustUser(t, srv, st.idTokenEmail())
	if u.FirstName != "Sso" || u.PreferredName != "Pref" {
		t.Errorf("account reads %q / preferred %q", u.FirstName, u.PreferredName)
	}
}

func TestNamesStayEditableWhereTheProviderDoesNotOwnThem(t *testing.T) {
	t.Run("a magic-link session of the same account", func(t *testing.T) {
		st := newOIDCStub(t)
		srv := ssoTestServer(t, st)
		ssoSignInSession(t, srv, st)
		token := "magic-link-session-2515"
		srv.sessionStore().storePortalSession(token, PortalSessionData{
			Email: st.idTokenEmail(), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().UTC(),
		})
		if meBody(t, srv, token)["names_from_provider"] == true {
			t.Error("a magic-link session reports provider-owned names")
		}
		if rec := putMe(srv, token, `{"first_name":"Own","last_name":"Name"}`); rec.Code != http.StatusOK {
			t.Errorf("a magic-link session could not edit the names: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("a provider that sends no names", func(t *testing.T) {
		st := newOIDCStub(t)
		st.omitNames = true
		srv := ssoTestServer(t, st)
		session := ssoSignInSession(t, srv, st)
		if meBody(t, srv, session)["names_from_provider"] == true {
			t.Error("a session whose token carried no names locks them")
		}
		if rec := putMe(srv, session, `{"first_name":"Own","last_name":"Name"}`); rec.Code != http.StatusOK {
			t.Errorf("names the provider never sent could not be edited: %d", rec.Code)
		}
	})
}

// The flag survives what a session goes through: a restart (it is persisted) and the TOTP
// challenge (#2517), which issues the session later, from mfa-verify.
func TestTheNamesFlagSurvivesARestartAndTheTOTPChallenge(t *testing.T) {
	t.Run("restart", func(t *testing.T) {
		st := newOIDCStub(t)
		srv := ssoTestServer(t, st)
		session := ssoSignInSession(t, srv, st)
		srv.portalMap.Delete(portalSessionKeyPrefix + session) // as a restart would leave it
		if meBody(t, srv, session)["names_from_provider"] != true {
			t.Error("the flag was lost when the session was reloaded from the database")
		}
	})
	t.Run("TOTP challenge", func(t *testing.T) {
		st := newOIDCStub(t)
		srv := ssoTestServer(t, st)
		code := enrolledTOTP(t, srv, st.idTokenEmail())
		st.givenName = "Challenged"
		cookies, state := startSignIn(t, srv, st)
		pending := pendingCookie(finishSignIn(srv, cookies, state))
		if pending == nil {
			t.Fatal("no TOTP challenge")
		}
		ok := verifyWithCookie(srv, pending, code)
		if !hasSession(ok) {
			t.Fatalf("verify: %d %s", ok.Code, ok.Body.String())
		}
		if !sessionFrom(t, srv, ok).NamesFromProvider {
			t.Error("the session issued after the TOTP challenge lost the names flag")
		}
		if u := mustUser(t, srv, st.idTokenEmail()); u.FirstName != "Challenged" {
			t.Errorf("the names were not refreshed on a challenged sign-in: %q", u.FirstName)
		}
	})
}
