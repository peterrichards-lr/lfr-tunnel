package server

import "testing"

// #2520: Okta returns profile claims from userinfo, not in the ID token, when an access token is
// issued too. The stub's namesInUserinfo reproduces that -- the state production is actually in,
// which the earlier tests' ID-token names never described.

func TestNamesComeFromUserinfoWhenTheIDTokenHasNone(t *testing.T) {
	st := newOIDCStub(t)
	st.namesInUserinfo = true
	st.givenName, st.familyName = "Peter", "Richards"
	srv := ssoTestServer(t, st)
	session := ssoSignInSession(t, srv, st)

	u := mustUser(t, srv, st.idTokenEmail())
	if u.FirstName != "Peter" || u.LastName != "Richards" {
		t.Errorf("names from userinfo were not used: %q %q", u.FirstName, u.LastName)
	}
	if u.PreferredName != "Peter" {
		t.Errorf("a first sign-in did not default the preferred name from userinfo: %q", u.PreferredName)
	}
	if meBody(t, srv, session)["names_from_provider"] != true {
		t.Error("a session whose names came from userinfo is not marked names_from_provider")
	}
	if rec := putMe(srv, session, `{"first_name":"wewewe","last_name":"Richards"}`); rec.Code != 409 {
		t.Errorf("a session with names from userinfo could change them: %d", rec.Code)
	}
}

func TestUserinfoForAnotherSubjectIsNotUsed(t *testing.T) {
	st := newOIDCStub(t)
	st.namesInUserinfo = true
	st.userinfoSub = "someone-else"
	st.givenName, st.familyName = "Mallory", "Other"
	srv := ssoTestServer(t, st)
	session := ssoSignInSession(t, srv, st)

	if u := mustUser(t, srv, st.idTokenEmail()); u.FirstName != "" || u.LastName != "" {
		t.Errorf("userinfo for a different sub set the names: %q %q", u.FirstName, u.LastName)
	}
	if meBody(t, srv, session)["names_from_provider"] == true {
		t.Error("names from a mismatched userinfo marked the session")
	}
}

func TestNamesInTheIDTokenNeedNoUserinfoCall(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	ssoSignInSession(t, srv, st)
	if n := st.userinfoCalls.Load(); n != 0 {
		t.Errorf("userinfo was called %d time(s) although the ID token carried the names", n)
	}
	if u := mustUser(t, srv, st.idTokenEmail()); u.FirstName != "Sso" {
		t.Errorf("ID-token names: %q", u.FirstName)
	}
}
