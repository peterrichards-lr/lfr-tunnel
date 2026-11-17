package server

import (
	"net/http"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// #2471: an SSO sign-in from outside allowed_email_domains gets a page saying why and what to do
// instead of a bare 403 -- configurable, and switchable off. Driven through the real callback: the
// stub provider asserts sso.person@example.com, and the gateway admits only liferay.com.
func refusedSignIn(t *testing.T, refusal config.DomainRefusalConfig) (int, string, string, bool) {
	t.Helper()
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	srv.cfg.AllowedEmailDomains = []string{"liferay.com"}
	srv.cfg.DomainRefusal = refusal
	cookies, state := startSignIn(t, srv, st)
	rec := finishSignIn(srv, cookies, state)
	return rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), hasSession(rec)
}

func TestSSODomainRefusalPageExplainsAndPointsAtTheSource(t *testing.T) {
	code, ctype, body, session := refusedSignIn(t, config.DomainRefusalConfig{})
	if code != http.StatusForbidden || session {
		t.Fatalf("refused sign-in: %d, session %v; want 403 and no session", code, session)
	}
	if !strings.HasPrefix(ctype, "text/html") {
		t.Errorf("Content-Type %q, want the page", ctype)
	}
	for _, want := range []string{"MIT", "your own", config.DefaultRepositoryURL} {
		if !strings.Contains(body, want) {
			t.Errorf("the default page does not mention %q", want)
		}
	}
}

func TestSSODomainRefusalUsesTheConfiguredMessageEscaped(t *testing.T) {
	_, _, body, _ := refusedSignIn(t, config.DomainRefusalConfig{Message: "Liferay staff only <b>sorry</b>\nRun your own."})
	if !strings.Contains(body, "Liferay staff only &lt;b&gt;sorry&lt;/b&gt;<br>Run your own.") {
		t.Errorf("configured message not shown escaped with its line break kept:\n%s", body)
	}
	if strings.Contains(body, "<b>sorry</b>") {
		t.Errorf("configured message was injected as markup")
	}
	if strings.Contains(body, defaultDomainRefusalMessage[:40]) {
		t.Errorf("the built-in message was shown as well as the configured one")
	}
}

func TestSSODomainRefusalCanBeSwitchedOff(t *testing.T) {
	off := false
	code, ctype, body, _ := refusedSignIn(t, config.DomainRefusalConfig{Enabled: &off, Message: "never shown"})
	if code != http.StatusForbidden || strings.HasPrefix(ctype, "text/html") || strings.TrimSpace(body) != domainRefusalShort {
		t.Errorf("switched off: %d %q %q, want the short plain refusal", code, ctype, body)
	}
}
