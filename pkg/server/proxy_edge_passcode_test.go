package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// A gateway with NO database must both challenge for a passcode AND accept the right one.
//
// It used to do only the first. Enforcement reads lease.AccessControls(); verification read the
// reservation out of the database, and an edge has none — so passcodeRequired was always "",
// the comparison could never be true, and every correct passcode was answered with "Incorrect
// passcode. Please try again." The edge locked the door and held no key (#2125).
//
// Every case here runs against db == nil, because that is the configuration the defect lives in
// and the one nothing else covers.

const (
	edgeHost     = "peters.lfr-demo.se"
	goodPasscode = "s3cret-passcode"
	listedIP     = "203.0.113.5"
	unlistedIP   = "198.51.100.9"
	theWhitelist = "203.0.113.0/24"
)

// newEdgeProxy is a proxy handler with no database, as every regional edge runs.
func newEdgeProxy(t *testing.T) *ProxyHandler {
	t.Helper()
	p := NewProxyHandler(nil, &config.ServerConfig{Domains: []string{"lfr-demo.se"}})
	if p.db != nil {
		t.Fatal("fixture has a database; it would not exercise the defect")
	}
	return p
}

func edgeLease(mode, passcode, whitelist string) *TunnelLease {
	lease := &TunnelLease{SubdomainPrefix: "peters"}
	lease.SetAccessControls(passcode, whitelist, mode)
	return lease
}

// submitPasscode POSTs the form the challenge page renders, and reports the session cookie it
// was given, if any.
func submitPasscode(p *ProxyHandler, lease *TunnelLease, passcode, fromIP string) (*http.Cookie, int) {
	form := url.Values{"passcode": {passcode}, "redirect_uri": {"/"}}
	r := httptest.NewRequest(http.MethodPost, "/lfr-tunnel-verify", strings.NewReader(form.Encode()))
	r.Host = edgeHost
	r.RemoteAddr = fromIP + ":40000"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	p.checkAccessControls(w, r, lease, edgeHost)

	for _, c := range w.Result().Cookies() {
		if c.Name == "lfr_tunnel_session" {
			return c, w.Code
		}
	}
	return nil, w.Code
}

// visit reports whether a normal request is allowed through.
func visit(p *ProxyHandler, lease *TunnelLease, cookie *http.Cookie, fromIP string) bool {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = edgeHost
	r.RemoteAddr = fromIP + ":40001"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return p.checkAccessControls(httptest.NewRecorder(), r, lease, edgeHost)
}

// The defect itself: the correct passcode, on a gateway with no database.
func TestAnEdgeAcceptsTheCorrectPasscode(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("passcode", HashPasscode(goodPasscode), "")

	cookie, _ := submitPasscode(p, lease, goodPasscode, unlistedIP)
	if cookie == nil {
		t.Fatal("the correct passcode was refused by a gateway with no database -- this is the " +
			"reported defect: the edge challenges from the lease and verified from a database " +
			"it does not have")
	}
	if !visit(p, lease, cookie, unlistedIP) {
		t.Error("the session cookie issued for a correct passcode did not grant access")
	}
}

func TestAnEdgeStillRefusesTheWrongPasscode(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("passcode", HashPasscode(goodPasscode), "")

	if cookie, _ := submitPasscode(p, lease, "not-the-passcode", unlistedIP); cookie != nil {
		t.Fatal("a wrong passcode was issued a session cookie")
	}
}

// The owner's requirement: whitelisting must keep working, and must keep MEANING something.
func TestAnEdgeEnforcesTheWhitelistFromTheLease(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("whitelist", "", theWhitelist)

	if !visit(p, lease, nil, listedIP) {
		t.Error("a listed address was refused")
	}
	if visit(p, lease, nil, unlistedIP) {
		t.Error("an unlisted address was allowed in")
	}
}

// AND must stay AND. This is the case the fix sits directly on top of: reading the passcode from
// the lease touches the code immediately above the mode logic.
func TestAndModeStillNeedsBothFactorsOnAnEdge(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("and", HashPasscode(goodPasscode), theWhitelist)

	cookie, _ := submitPasscode(p, lease, goodPasscode, listedIP)
	if cookie == nil {
		t.Fatal("the correct passcode was refused outright")
	}

	if !visit(p, lease, cookie, listedIP) {
		t.Error("correct passcode AND a listed address was refused")
	}
	if visit(p, lease, cookie, unlistedIP) {
		t.Error("a correct passcode let an UNLISTED address in under 'and'; both factors are " +
			"supposed to be required, and the passcode half is what this change touched")
	}
}

// OR opens on either factor alone.
func TestOrModeOpensOnEitherFactorOnAnEdge(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("or", HashPasscode(goodPasscode), theWhitelist)

	if !visit(p, lease, nil, listedIP) {
		t.Error("a listed address was refused under 'or' with no passcode")
	}

	cookie, _ := submitPasscode(p, lease, goodPasscode, unlistedIP)
	if cookie == nil {
		t.Fatal("the correct passcode was refused under 'or'")
	}
	if !visit(p, lease, cookie, unlistedIP) {
		t.Error("a correct passcode did not open 'or' from an unlisted address")
	}
}

// Reading the passcode from the lease makes it non-empty even in a mode where it is not applied,
// so a direct POST now mints a cookie where it previously could not. Enforcement must still
// ignore it. Asserted rather than reasoned about, because it only holds while the two stay in
// step.
func TestAPasscodeCookieDoesNotOpenAWhitelistOnlyTunnel(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("whitelist", HashPasscode(goodPasscode), theWhitelist)

	cookie, _ := submitPasscode(p, lease, goodPasscode, unlistedIP)
	if visit(p, lease, cookie, unlistedIP) {
		t.Error("a passcode opened a tunnel whose mode says only the IP whitelist applies")
	}
}

// Public means neither factor is applied, whatever is stored (#2098).
func TestPublicStaysPublicOnAnEdge(t *testing.T) {
	p := newEdgeProxy(t)
	lease := edgeLease("public", HashPasscode(goodPasscode), theWhitelist)

	if !visit(p, lease, nil, unlistedIP) {
		t.Error("a public tunnel refused a visitor because values were retained but not applied")
	}
}
