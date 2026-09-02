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

// backgroundPollHeader marks a request the portal made on a timer rather than one a person
// caused. Requests carrying it do not extend the session (#1676).
const backgroundPollHeader = "X-Background-Poll"

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
//
// expiresAt is passed in rather than computed here so the cookie cannot outlive the session it
// represents: with an absolute cap configured (#1679) the session may end sooner than
// now+PortalSessionDuration, and a cookie that survived it would leave the browser presenting
// credentials the server has already retired.
func (s *Server) newSessionCookie(r *http.Request, token string, mode http.SameSite, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: mode,
	}
}

// sessionDeadline is when a session actually ends: the earlier of its sliding idle expiry and
// its absolute cap (#1679).
//
// The idle timeout slides on use, so a continuously-used session renews forever -- and since
// #1676 that sliding is suppressed by a client-supplied header, which means the idle bound
// depends on the client behaving. The cap does not: it is measured from the session's creation
// and nothing sent by a client can move it.
//
// Returns the idle expiry unchanged when no cap is configured (the default) or when CreatedAt is
// unknown, which is the case for a session that predates this. Such a session is bounded by the
// idle timeout alone -- the behaviour before this change -- rather than being capped from an
// assumed start time, which would sign people out early on deploy.
func (s *Server) sessionDeadline(data PortalSessionData) time.Time {
	if s.cfg.PortalSessionMaxLifetime <= 0 || data.CreatedAt.IsZero() {
		return data.ExpiresAt
	}
	cap := data.CreatedAt.Add(s.cfg.PortalSessionMaxLifetime)
	if cap.Before(data.ExpiresAt) {
		return cap
	}
	return data.ExpiresAt
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
	// A background poll is not activity (#1676). Both portals refresh /api/me every ten
	// seconds, so before this an open tab renewed its own session forever and the configured
	// duration measured whether a tab was open rather than whether anyone was there --
	// unenforceable in exactly the case an idle timeout exists for, someone away from an
	// unlocked machine.
	//
	// The header is client-supplied, and that is safe in this direction only: honouring it can
	// only ever let a session expire sooner, never extend one. A client that omits it, or an
	// attacker who strips it, gets the old behaviour of a slide -- which is why the absolute
	// cap below does not depend on it.
	if r.Header.Get(backgroundPollHeader) != "" {
		return
	}

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

	// Never past the absolute cap. Without this the slide would push ExpiresAt beyond a
	// deadline the session can no longer reach, and the pre-expiry warning -- which reads the
	// effective deadline -- would promise time that does not exist (#1679).
	extended := time.Now().Add(s.cfg.PortalSessionDuration)
	if capped := s.sessionDeadline(PortalSessionData{
		ExpiresAt: extended,
		CreatedAt: data.CreatedAt,
	}); capped.Before(extended) {
		extended = capped
	}

	// Already at the cap: nothing to extend, and re-issuing a cookie that expires at the same
	// instant is noise on every request for the rest of the session.
	if !extended.After(data.ExpiresAt) {
		return
	}

	data.ExpiresAt = extended
	s.sessionStore().storePortalSession(cookie.Value, data)
	http.SetCookie(w, s.newSessionCookie(r, cookie.Value, mode, extended))
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
