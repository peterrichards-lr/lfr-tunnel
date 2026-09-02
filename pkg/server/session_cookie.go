package server

import (
	"net/http"
	"time"
)

// The session cookie's construction, in one place (#1655).
//
// It used to be written out three times -- portal login, admin magic-link login and SSO -- and
// the copies had already drifted: the admin path sets SameSite=Strict where the other two set
// Lax (#1661). Sliding expiry has to re-issue the cookie, which makes a fourth copy the point at
// which that drift turns into a silent downgrade, so there is now exactly one definition.
const sessionCookieName = "lfr_session"

// schemeHTTPS is the value X-Forwarded-Proto carries when a proxy terminated TLS. A constant
// because goconst counts this literal across the package and the comparison now lives in one
// place rather than three.
const schemeHTTPS = "https"

// cookieSecure mirrors what the three login paths already did: trust TLS directly, or the
// proxy's forwarded scheme when one is in front.
func cookieSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS
}

// sameSiteToStored and sameSiteFromStored convert between http.SameSite and the string kept on
// the session. Stored as a word rather than the enum's integer so a row stays readable and does
// not silently change meaning if the constants are ever renumbered.
func sameSiteToStored(mode http.SameSite) string {
	switch mode {
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}

// sameSiteFromStored returns the mode and whether it was known.
//
// An unknown or empty value returns false, and the caller must then leave the cookie alone
// rather than re-issue it with a default. That is the conservative half of #1655: a session
// created before the mode was recorded keeps its original cookie, which expires at its original
// time. It stops sliding, which is the old behaviour, rather than being downgraded -- and it
// resolves itself at the user's next login.
func sameSiteFromStored(stored string) (http.SameSite, bool) {
	switch stored {
	case "Strict":
		return http.SameSiteStrictMode, true
	case "Lax":
		return http.SameSiteLaxMode, true
	case "None":
		return http.SameSiteNoneMode, true
	default:
		return http.SameSiteDefaultMode, false
	}
}

// newSessionCookie builds the cookie for a session token. Every attribute other than SameSite is
// identical across all callers; SameSite is passed in because the login paths disagree and the
// disagreement has to survive a refresh until #1661 settles it.
func (s *Server) newSessionCookie(r *http.Request, token string, mode http.SameSite) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(s.cfg.PortalSessionDuration),
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: mode,
	}
}

// refreshWindow is how much of a session's life must have elapsed before a request re-issues
// its cookie.
//
// Not on every request: a Set-Cookie on every response is bytes on the wire and a write per
// call, and the useful property -- that the cookie does not expire while someone is working --
// is fully served by refreshing well before the deadline. A quarter of the duration is 6h at
// the default 24h, so an active session is refreshed a handful of times a day at most.
const refreshWindow = 4

// slidePortalSession extends a live session and re-issues its cookie, so the browser's copy
// expires no sooner than the server's (#1655).
//
// The server already slid ExpiresAt on the admin path, but nothing ever re-issued the cookie --
// which is set only at login. So the browser dropped it 24h after login regardless of activity,
// the sliding renewal could never change the outcome, and the two halves disagreed: server-side
// state said the session was alive while the client no longer had the cookie to prove it.
//
// Called once per request from ServeHTTP rather than from each authenticated handler, so there
// is one rule instead of a rule per entry point -- the previous arrangement slid on requireAdmin
// only, so an ordinary portal user's session was never extended at all.
//
// Does nothing, deliberately, when:
//   - there is no cookie or the session is not live: nothing to extend, and this must not create
//     a session or resurrect an expired one;
//   - less than refreshWindow of the life has elapsed: not worth a write and a header;
//   - the session predates the recorded SameSite mode: re-issuing would have to guess, and a
//     guess would downgrade an admin session from Strict to Lax (#1661). Such a session keeps
//     its original cookie and stops sliding -- the behaviour before this change -- and fixes
//     itself at the next login.
func (s *Server) slidePortalSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return
	}

	data, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok {
		return
	}

	mode, known := sameSiteFromStored(data.SameSite)
	if !known {
		return
	}

	remaining := time.Until(data.ExpiresAt)
	if remaining > s.cfg.PortalSessionDuration-s.cfg.PortalSessionDuration/refreshWindow {
		return
	}

	data.ExpiresAt = time.Now().Add(s.cfg.PortalSessionDuration)
	s.sessionStore().storePortalSession(cookie.Value, data)
	http.SetCookie(w, s.newSessionCookie(r, cookie.Value, mode))
}

// clearSessionCookie expires the session cookie.
//
// Same attributes as newSessionCookie, because a browser identifies a cookie by name, path and
// domain, and a mismatch can leave the original in place. There were two hand-written copies of
// this and they disagreed with each other and with the login paths (#1661): one carried
// SameSite=Strict where the session was set Lax, and the other set `Secure: r.TLS != nil`
// without the X-Forwarded-Proto check every other site uses -- so behind a TLS-terminating
// proxy it tried to clear a Secure cookie with a non-Secure one.
func (s *Server) clearSessionCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	}
}
