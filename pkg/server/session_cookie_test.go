package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func testServerForCookies(t *testing.T) *Server {
	t.Helper()
	return &Server{
		cfg: &config.ServerConfig{PortalSessionDuration: 24 * time.Hour},
	}
}

// The mode has to survive the round trip, or a slide cannot re-issue what login created.
func TestSameSiteRoundTrip(t *testing.T) {
	for _, mode := range []http.SameSite{
		http.SameSiteStrictMode, http.SameSiteLaxMode, http.SameSiteNoneMode,
	} {
		stored := sameSiteToStored(mode)
		got, known := sameSiteFromStored(stored)
		if !known {
			t.Errorf("%v stored as %q and came back unknown", mode, stored)
		}
		if got != mode {
			t.Errorf("%v stored as %q came back as %v", mode, stored, got)
		}
	}
}

// A session created before the column existed has an empty mode. It must be reported unknown so
// the caller leaves the cookie alone rather than re-issuing a Strict session as Lax (#1661).
func TestSameSiteFromStoredRejectsUnknown(t *testing.T) {
	for _, stored := range []string{"", "lax", "strict", "nonsense"} {
		if _, known := sameSiteFromStored(stored); known {
			t.Errorf("%q was accepted; an unrecognised mode must be reported unknown so the "+
				"cookie is left alone rather than downgraded", stored)
		}
	}
}

// The security attributes are the part that must not drift. A refresh that quietly dropped
// HttpOnly or Secure would be a worse bug than the one being fixed.
func TestNewSessionCookieAttributes(t *testing.T) {
	s := testServerForCookies(t)

	r := httptest.NewRequest(http.MethodGet, "https://example.com/api/me", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	c := s.newSessionCookie(r, "tok", http.SameSiteStrictMode)

	if c.Name != sessionCookieName {
		t.Errorf("name = %q, want %q", c.Name, sessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be set -- the session must not be readable from JavaScript")
	}
	if !c.Secure {
		t.Error("Secure must be set when the request arrived over https")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict -- the caller's mode must be honoured", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Expires.Before(time.Now().Add(23 * time.Hour)) {
		t.Errorf("Expires = %v, want ~24h out", c.Expires)
	}
}

// Secure must follow the scheme, not be hardcoded: a plain-http local run has to keep working.
func TestNewSessionCookieSecureFollowsScheme(t *testing.T) {
	s := testServerForCookies(t)

	plain := httptest.NewRequest(http.MethodGet, "http://example.com/api/me", nil)
	if s.newSessionCookie(plain, "tok", http.SameSiteLaxMode).Secure {
		t.Error("Secure must not be set for a plain http request")
	}

	proxied := httptest.NewRequest(http.MethodGet, "http://example.com/api/me", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if !s.newSessionCookie(proxied, "tok", http.SameSiteLaxMode).Secure {
		t.Error("Secure must be set when a proxy reports https")
	}
}

// findSetCookie returns the lfr_session cookie a response set, or nil.
func findSetCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// slideFixture builds a server holding one session with the given remaining life and mode.
func slideFixture(t *testing.T, remaining time.Duration, mode string) (*Server, *http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	s := testServerForCookies(t)
	s.sessionStore().storePortalSession("tok", PortalSessionData{
		Email:     "someone@example.com",
		ExpiresAt: time.Now().Add(remaining),
		SameSite:  mode,
	})
	r := httptest.NewRequest(http.MethodGet, "https://example.com/api/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	return s, r, httptest.NewRecorder()
}

// The bug: the server slid ExpiresAt but never re-issued the cookie, so the browser dropped it
// 24h after login no matter how active the user was.
func TestSlidePortalSessionReissuesTheCookie(t *testing.T) {
	// Well past the refresh window -- 1h left of a 24h session.
	s, r, rec := slideFixture(t, time.Hour, "Lax")

	s.slidePortalSession(rec, r)

	c := findSetCookie(rec)
	if c == nil {
		t.Fatal("no cookie was re-issued -- the browser's copy would still expire at its original time")
	}
	if c.Expires.Before(time.Now().Add(23 * time.Hour)) {
		t.Errorf("re-issued cookie expires at %v, want ~24h out", c.Expires)
	}
	if !c.HttpOnly || !c.Secure {
		t.Error("the re-issued cookie must keep HttpOnly and Secure")
	}

	data, ok := s.sessionStore().loadPortalSession("tok")
	if !ok {
		t.Fatal("the session was lost")
	}
	if data.ExpiresAt.Before(time.Now().Add(23 * time.Hour)) {
		t.Errorf("server-side expiry = %v, want ~24h out", data.ExpiresAt)
	}
}

// Strict must not become Lax on refresh. This is the reason the mode is stored at all.
func TestSlidePortalSessionPreservesStrict(t *testing.T) {
	s, r, rec := slideFixture(t, time.Hour, "Strict")

	s.slidePortalSession(rec, r)

	c := findSetCookie(rec)
	if c == nil {
		t.Fatal("no cookie was re-issued")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict -- refreshing must not downgrade the admin session", c.SameSite)
	}
}

// A session predating the stored mode must be left alone rather than re-issued with a guess.
func TestSlidePortalSessionLeavesUnknownModeAlone(t *testing.T) {
	s, r, rec := slideFixture(t, time.Hour, "")

	s.slidePortalSession(rec, r)

	if c := findSetCookie(rec); c != nil {
		t.Errorf("a cookie was re-issued as %v for a session with no recorded mode -- that is a "+
			"guess, and would downgrade a Strict session", c.SameSite)
	}
}

// Not on every request: a freshly-issued session has nothing to gain and this would put a
// Set-Cookie on every response.
func TestSlidePortalSessionSkipsFreshSessions(t *testing.T) {
	s, r, rec := slideFixture(t, 24*time.Hour, "Lax")

	s.slidePortalSession(rec, r)

	if c := findSetCookie(rec); c != nil {
		t.Error("a fresh session was refreshed; the refresh window exists to avoid a Set-Cookie per request")
	}
}

// It must not create a session, and must not resurrect an expired one.
func TestSlidePortalSessionIgnoresAbsentAndExpired(t *testing.T) {
	s := testServerForCookies(t)

	noCookie := httptest.NewRequest(http.MethodGet, "https://example.com/api/version", nil)
	rec := httptest.NewRecorder()
	s.slidePortalSession(rec, noCookie)
	if findSetCookie(rec) != nil {
		t.Error("a cookie was set for a request that had none -- this must never mint a session")
	}

	s.sessionStore().storePortalSession("dead", PortalSessionData{
		Email:     "someone@example.com",
		ExpiresAt: time.Now().Add(-time.Minute),
		SameSite:  "Lax",
	})
	expired := httptest.NewRequest(http.MethodGet, "https://example.com/api/me", nil)
	expired.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "dead"})
	rec2 := httptest.NewRecorder()
	s.slidePortalSession(rec2, expired)
	if findSetCookie(rec2) != nil {
		t.Error("an expired session was re-issued a cookie -- expiry must be final")
	}
}

// Every login path must issue the same SameSite mode (#1661).
//
// They disagreed: the admin magic-link path used Strict where portal login and SSO used Lax. A
// browser does not send a Strict cookie on a cross-site navigation, and six email templates link
// users into /portal -- subdomain reserved, expiring, expired, demoted, extension approved and
// vanity hook failed -- so an admin clicking any of them arrived without their cookie and landed
// on the login page while holding a valid session.
//
// Asserted on the source rather than by driving three login flows: the flows need a database, a
// mail server and a magic-link round trip each, and the property under test is one literal per
// call site. A test that cannot run is worth less than one that reads the code.
func TestAllLoginPathsUseLaxSameSite(t *testing.T) {
	for _, file := range []string{"api.go", "server.go", "sso.go", "api_service_mfa.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if bytes.Contains(src, []byte("http.SameSiteStrictMode")) {
			t.Errorf("%s still names SameSiteStrictMode. Every login path issues Lax (#1661); "+
				"Strict is not sent on a cross-site navigation and breaks the PortalLink emails.", file)
		}
	}
}

// The clearing cookie must match what was set, or the browser keeps the original: a cookie is
// identified by name, path and domain, and a Secure/non-Secure mismatch behind a TLS-terminating
// proxy is enough to leave it in place.
func TestClearSessionCookieMatchesTheIssuedOne(t *testing.T) {
	s := testServerForCookies(t)
	r := httptest.NewRequest(http.MethodGet, "http://example.com/api/auth/logout", nil)
	r.Header.Set("X-Forwarded-Proto", "https")

	issued := s.newSessionCookie(r, "tok", http.SameSiteLaxMode)
	cleared := s.clearSessionCookie(r)

	if cleared.Name != issued.Name {
		t.Errorf("name %q != issued %q", cleared.Name, issued.Name)
	}
	if cleared.Path != issued.Path {
		t.Errorf("path %q != issued %q", cleared.Path, issued.Path)
	}
	if cleared.Secure != issued.Secure {
		t.Errorf("Secure %v != issued %v -- a mismatch behind a proxy can leave the cookie in place",
			cleared.Secure, issued.Secure)
	}
	if cleared.HttpOnly != issued.HttpOnly {
		t.Errorf("HttpOnly %v != issued %v", cleared.HttpOnly, issued.HttpOnly)
	}
	if cleared.SameSite != issued.SameSite {
		t.Errorf("SameSite %v != issued %v", cleared.SameSite, issued.SameSite)
	}
	if cleared.Value != "" || !cleared.Expires.Before(time.Now()) {
		t.Errorf("the clearing cookie must be empty and already expired, got value=%q expires=%v",
			cleared.Value, cleared.Expires)
	}
}

// Strict must still round-trip. Sessions created before #1661 are stored as Strict and have to
// be re-issued as Strict rather than silently changed mid-session; they age out within the
// session duration.
func TestStrictStillRoundTripsForExistingSessions(t *testing.T) {
	mode, known := sameSiteFromStored("Strict")
	if !known || mode != http.SameSiteStrictMode {
		t.Errorf("a session stored as Strict must still be re-issued as Strict, got (%v, %v)", mode, known)
	}
}

// A background poll must not extend the session (#1676).
//
// Both portals refresh /api/me every ten seconds. Before this, each of those requests slid the
// session, so an open tab renewed itself indefinitely and portal_session_duration measured
// whether a tab was open rather than whether anyone was there -- unenforceable in exactly the
// case an idle timeout exists for.
func TestBackgroundPollDoesNotSlideTheSession(t *testing.T) {
	s, r, rec := slideFixture(t, time.Hour, "Lax")
	r.Header.Set(backgroundPollHeader, "1")

	s.slidePortalSession(rec, r)

	if c := findSetCookie(rec); c != nil {
		t.Error("a background poll re-issued the cookie -- an open tab would renew itself forever")
	}

	data, ok := s.sessionStore().loadPortalSession("tok")
	if !ok {
		t.Fatal("the session was lost")
	}
	// Server-side expiry must be untouched too. Skipping only the cookie would leave the
	// session alive on the server while the browser's copy aged out -- the mismatch #1655 fixed,
	// reintroduced from the other side.
	if data.ExpiresAt.After(time.Now().Add(2 * time.Hour)) {
		t.Errorf("a background poll extended the server-side expiry to %v", data.ExpiresAt)
	}
}

// The same request without the header still slides, so ordinary use never expires under you.
// Paired with the test above deliberately: asserting only that polls are ignored would pass for
// an implementation that had stopped sliding altogether.
func TestUserRequestStillSlidesTheSession(t *testing.T) {
	s, r, rec := slideFixture(t, time.Hour, "Lax")

	s.slidePortalSession(rec, r)

	if findSetCookie(rec) == nil {
		t.Error("a user request did not slide the session -- active use must not expire")
	}
}

// An empty header value is not a marker. Browsers and proxies can send empty headers, and
// treating one as "this is a poll" would let a stray header shorten a real session.
func TestEmptyBackgroundPollHeaderIsIgnored(t *testing.T) {
	s, r, rec := slideFixture(t, time.Hour, "Lax")
	r.Header.Set(backgroundPollHeader, "")

	s.slidePortalSession(rec, r)

	if findSetCookie(rec) == nil {
		t.Error("an empty X-Background-Poll suppressed the slide; only a non-empty value marks a poll")
	}
}
