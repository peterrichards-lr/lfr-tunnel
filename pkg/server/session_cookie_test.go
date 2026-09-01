package server

import (
	"net/http"
	"net/http/httptest"
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
