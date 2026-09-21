package server

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// Submitting a passcode landed on a 404 and no passcode could ever be entered (#2108).
//
// Reported: "I am prompted for a passcode but when I enter it I see a URL of
// https://tunnel.lfr-demo.se/lfr-tunnel-verify and a 404 page."
//
// injectBaseTag rewrites every proxied page with <base href> pointing at the PORTAL, so shared
// assets load from one place. A <base> also re-points ROOT-RELATIVE urls, so the form's
// action="/lfr-tunnel-verify" resolved against the control plane -- which does not route it,
// because the endpoint is intercepted inside the proxy path, per tunnel host.

func servedPasscodePage(t *testing.T, host string, tls bool) string {
	t.Helper()

	p := &ProxyHandler{config: config.DefaultServerConfig()}
	p.config.Domains = []string{"lfr-demo.se"}

	scheme := "http"
	if tls {
		scheme = "https"
	}
	r := httptest.NewRequest("GET", scheme+"://"+host+"/", nil)
	if tls {
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	w := httptest.NewRecorder()

	p.servePasscodePage(w, r, host, "/", "")
	return w.Body.String()
}

// The reported defect: the action must not be a bare root-relative path, because the base tag
// on the very same page re-points it.
func TestThePasscodeFormPostsToTheTunnelHost(t *testing.T) {
	page := servedPasscodePage(t, "peters.lfr-demo.se", true)

	action := regexp.MustCompile(`<form[^>]*action="([^"]+)"`).FindStringSubmatch(page)
	if action == nil {
		t.Fatal("the passcode page has no form action at all")
	}

	if !strings.HasPrefix(action[1], "https://peters.lfr-demo.se/") {
		t.Errorf("form posts to %q.\n"+
			"The page carries <base href> pointing at the portal, which re-points root-relative "+
			"URLs -- so this has to be absolute to the tunnel host or the passcode submission "+
			"404s on the control plane.", action[1])
	}
	if !strings.HasSuffix(action[1], "/lfr-tunnel-verify") {
		t.Errorf("form posts to %q, which is not the verification endpoint", action[1])
	}
}

// PREMISE: the base tag really is on this page. If it stopped being injected the assertion
// above would still pass, but for a reason that no longer holds -- and the next person would
// not know the constraint existed.
func TestThePasscodePageStillCarriesTheBaseTagThatCausedThis(t *testing.T) {
	page := servedPasscodePage(t, "peters.lfr-demo.se", true)

	if !strings.Contains(page, "<base href=") {
		t.Skip("no base tag is injected any more; the absolute action is now belt-and-braces")
	}
	base := regexp.MustCompile(`<base href="([^"]+)"`).FindStringSubmatch(page)
	if base != nil && strings.Contains(base[1], "peters.") {
		t.Errorf("the base points at the tunnel host (%s), so this test is not exercising the "+
			"portal-base case the defect came from", base[1])
	}
}

// The E2E stack and any plain-http deployment must not be handed an https action they cannot
// reach. Hardcoding the scheme was the obvious shortcut and would have broken them silently.
func TestTheSchemeFollowsTheRequest(t *testing.T) {
	page := servedPasscodePage(t, "peters.lfr-demo.local", false)

	action := regexp.MustCompile(`<form[^>]*action="([^"]+)"`).FindStringSubmatch(page)
	if action == nil {
		t.Fatal("no form action")
	}
	if !strings.HasPrefix(action[1], "http://") {
		t.Errorf("an http request produced the action %q -- a plain-http deployment would be "+
			"sent to a scheme it does not serve", action[1])
	}
}
