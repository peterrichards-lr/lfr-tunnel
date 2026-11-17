package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// #2470: the identity provider registers exactly one redirect URI, and the flow cookies and the
// session are host-only, so a sign-in must happen entirely on one host. A login started on any
// other host the portal answers on is sent to the canonical one first.
func TestSSOLoginOnAnotherHostRedirectsToTheCanonicalHost(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st) // domains: example.com, so the canonical host is tunnel.example.com

	for _, host := range []string{"portal.example.com", "example.com"} {
		rec := httptest.NewRecorder()
		srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/auth/login?provider=stub&portal=v2", nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: %d, want 302", host, rec.Code)
		}
		if got, want := rec.Header().Get("Location"), "http://tunnel.example.com/api/auth/login?provider=stub&portal=v2"; got != want {
			t.Errorf("%s: redirected to %q, want %q", host, got, want)
		}
		// Nothing is started on the wrong host: its cookies would never come back to the callback.
		if c := rec.Result().Cookies(); len(c) != 0 {
			t.Errorf("%s: set %d flow cookie(s) on a host the callback will never see", host, len(c))
		}
	}
}

func TestSSOLoginOnTheCanonicalHostSendsTheCanonicalCallback(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)

	rec := httptest.NewRecorder()
	srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/auth/login?provider=stub", nil))
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusFound {
		t.Fatalf("login on the canonical host: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, want := loc.Query().Get("redirect_uri"), "http://tunnel.example.com/api/auth/callback?provider=stub"; got != want {
		t.Errorf("redirect_uri %q, want %q -- the one URI the provider registers", got, want)
	}
}
