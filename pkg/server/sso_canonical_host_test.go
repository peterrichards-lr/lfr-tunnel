package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// Review of #2473: every configured domain canonicalises to the FIRST domain's tunnel. host --
// the one the provider registers -- and behind nginx the redirect is https.
func TestSSOLoginOnASecondDomainGoesToThePrimaryDomain(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	srv.cfg.Domains = []string{"example.com", "example.online"}

	for _, host := range []string{"tunnel.example.online", "portal.example.online", "example.online"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/auth/login?provider=stub", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		srv.handleSSOLogin(rec, req)
		if got, want := rec.Header().Get("Location"), "https://tunnel.example.com/api/auth/login?provider=stub"; rec.Code != http.StatusFound || got != want {
			t.Errorf("%s: %d %q, want 302 to %q", host, rec.Code, got, want)
		}
	}
}

// A control_plane_url carrying the default port must not redirect the canonical host to itself.
func TestSSOLoginDoesNotLoopOnADefaultPort(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	srv.cfg.ControlPlaneURL = "https://tunnel.example.com:443"

	req := httptest.NewRequest(http.MethodGet, "https://tunnel.example.com/api/auth/login?provider=stub", nil)
	rec := httptest.NewRecorder()
	srv.handleSSOLogin(rec, req)
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "tunnel.example.com:443/api/auth/login") {
		t.Errorf("the canonical host was redirected to itself: %q", loc)
	}
}
