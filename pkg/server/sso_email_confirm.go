package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// #2472: email_verification: gateway. When an SSO provider does not assert email_verified: true,
// the gateway confirms the address itself: it emails a one-time link and completes the sign-in
// only when that link is used. The mailbox, not the provider, then vouches for the address.

// ActionSSOConfirmationSent is written when an SSO sign-in waits on a confirmation link.
const ActionSSOConfirmationSent = "user.sso_confirmation_sent"

// How a confirmed mailbox was proven, as recorded in user_email_confirmations.
const (
	emailConfirmedBySSOLink   = "sso_link"
	emailConfirmedByMagicLink = "magic_link"
)

// ssoConfirmTTL is how long a confirmation link works, as long as a magic link does by default.
const ssoConfirmTTL = 15 * time.Minute

// ssoConfirmKeyPrefix keys a pending sign-in in portalMap by the hash of its token, so the token
// itself is held only by the mailbox it was sent to.
const ssoConfirmKeyPrefix = "sso_confirm_"

const ssoConfirmPath = "/api/auth/sso-confirm"

// pendingSSOConfirmation is an SSO sign-in that has passed every check except the address.
type pendingSSOConfirmation struct {
	ProviderID string
	Identity   ssoIdentity
	Return     ssoReturn
	ExpiresAt  time.Time
}

var notifySSOConfirmation = notificationKind{
	"sso_email_confirmation",
	"this user cannot finish signing in through SSO and has not been told",
}

func ssoConfirmKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return ssoConfirmKeyPrefix + hex.EncodeToString(h[:])
}

// mailboxProven reports whether an account's address is already known to reach its owner, so an
// SSO sign-in need not confirm it again: the gateway confirmed it before, or the account came from
// a path that only completes by opening a link sent to it -- email registration (verified before
// approval) or a magic-link sign-in. An invited or SSO-created account counts only once confirmed.
func (s *Server) mailboxProven(user *db.User) bool {
	if user == nil {
		return false
	}
	if ok, err := s.db.EmailConfirmed(user.ID); err != nil {
		slog.Error(fmt.Sprintf("[SSO] Could not read the address confirmation for %s: %v", user.Email, err))
	} else if ok {
		return true
	}
	if user.Status != db.UserStatusApproved {
		return false
	}
	switch user.AuthMethod {
	case "registration", "Magic Link", "":
		return true
	}
	return false
}

// startSSOEmailConfirmation holds the sign-in and emails a link that completes it.
func (s *Server) startSSOEmailConfirmation(w http.ResponseWriter, r *http.Request, p *config.SSOProviderConfig, id ssoIdentity, rt ssoReturn, newAccount bool) {
	// Refused now rather than after the person has gone to their mailbox for nothing.
	if newAccount && s.cfg.DisableNewRegistrations && !strings.EqualFold(id.Email, s.cfg.Owner.UserID) {
		http.Error(w, "New registrations are currently closed.", http.StatusForbidden)
		return
	}
	if s.notifications == nil || s.notifications.Sender() == nil {
		slog.Error(fmt.Sprintf("[SSO] %s: email_verification is gateway but no mail sender is configured, so %s cannot be confirmed", p.ID, id.Email))
		http.Error(w, "This gateway cannot confirm your email address right now. Please contact the gateway administrator.", http.StatusServiceUnavailable)
		return
	}
	token, err := generateSecureToken()
	if err != nil {
		http.Error(w, "Failed to start confirmation", http.StatusInternalServerError)
		return
	}
	key := ssoConfirmKey(token)
	s.portalMap.Store(key, pendingSSOConfirmation{
		ProviderID: p.ID,
		Identity:   id,
		Return:     rt,
		ExpiresAt:  time.Now().Add(ssoConfirmTTL),
	})
	// Expired entries would otherwise stay until a restart.
	time.AfterFunc(ssoConfirmTTL, func() { s.portalMap.Delete(key) })

	link := s.ssoConfirmBase(r) + ssoConfirmPath + "?token=" + token
	name := id.GivenName
	if name == "" {
		name = fallbackGreetingName
	}
	htmlBody := fmt.Sprintf(`<p>Hi %s,</p>
<p>You are signing in to Liferay Tunnel through %s. To finish, confirm that this email address is yours:</p>
<p><a href="%s">Confirm and sign in</a></p>
<p>This link works once and expires in 15 minutes. If you did not just try to sign in, ignore this email; nobody can sign in as you without it.</p>`,
		html.EscapeString(name), html.EscapeString(p.Name), html.EscapeString(link))
	plainBody := fmt.Sprintf("Hi %s,\n\nYou are signing in to Liferay Tunnel through %s. To finish, confirm that this email address is yours:\n\n%s\n\nThis link works once and expires in 15 minutes. If you did not just try to sign in, ignore this email; nobody can sign in as you without it.\n",
		name, p.Name, link)
	s.sendNotificationAsync(notifySSOConfirmation, id.Email, "Confirm your email to sign in to Liferay Tunnel", htmlBody, plainBody)

	s.writeAudit(id.Email, ActionSSOConfirmationSent, "user", id.Email,
		fmt.Sprintf("Sign-in through %s is waiting for the address to be confirmed: the identity provider did not verify it", p.ID), r)

	s.renderSSOConfirmPage(w, r, http.StatusOK, "Check your email",
		"Your identity provider did not confirm your email address, so we have sent a link to "+id.Email+
			". Open it to finish signing in. It works once and expires in 15 minutes.", "")
}

// ssoConfirmBase is the scheme and host the link points at: the canonical portal host, where the
// session it issues will be used. Never a host the request names when the configuration names one:
// the request's Host -- and X-Forwarded-Host even more so -- is the client's to choose, and a link
// to an attacker's host would hand them the token, and with it a session as the address's owner.
func (s *Server) ssoConfirmBase(r *http.Request) string {
	if u := s.ssoCanonicalBase(r); u != nil {
		return u.Scheme + "://" + u.Host
	}
	if len(s.cfg.Domains) > 0 && strings.TrimSpace(s.cfg.Domains[0]) != "" {
		return schemeHTTPS + "://tunnel." + strings.TrimSpace(s.cfg.Domains[0])
	}
	// No domain configured at all (a local stack): the host this request reached.
	return s.getPortalBaseURL(r)
}

// handleSSOConfirm serves the link. GET only shows a button: mail scanners fetch links, and one
// that consumed the token would spend it before the person did. POST spends it and signs in --
// in whichever browser opened the link, which is the one that proved the mailbox.
func (s *Server) handleSSOConfirm(w http.ResponseWriter, r *http.Request) {
	const expired = "This link has expired or has already been used. Sign in again to get a new one."
	if r.Method == http.MethodGet {
		token := r.URL.Query().Get("token")
		v, ok := s.portalMap.Load(ssoConfirmKey(token))
		if token == "" || !ok || time.Now().After(v.(pendingSSOConfirmation).ExpiresAt) {
			s.renderSSOConfirmPage(w, r, http.StatusGone, "Link expired", expired, "")
			return
		}
		form := `<form method="post" action="` + ssoConfirmPath + `"><input type="hidden" name="token" value="` +
			html.EscapeString(token) + `"><button type="submit" class="btn">Confirm and sign in</button></form>`
		s.renderSSOConfirmPage(w, r, http.StatusOK, "Confirm your email",
			"Confirm that "+v.(pendingSSOConfirmation).Identity.Email+" is your address to finish signing in.", form)
		return
	}

	token := r.PostFormValue("token")
	v, ok := s.portalMap.LoadAndDelete(ssoConfirmKey(token))
	if token == "" || !ok {
		s.renderSSOConfirmPage(w, r, http.StatusGone, "Link expired", expired, "")
		return
	}
	pending := v.(pendingSSOConfirmation)
	if time.Now().After(pending.ExpiresAt) {
		s.renderSSOConfirmPage(w, r, http.StatusGone, "Link expired", expired, "")
		return
	}
	var p *config.SSOProviderConfig
	for i := range s.cfg.SSOProviders {
		if s.cfg.SSOProviders[i].ID == pending.ProviderID {
			p = &s.cfg.SSOProviders[i]
		}
	}
	if p == nil {
		http.Error(w, "This sign-in method is no longer available.", http.StatusGone)
		return
	}
	slog.Info(fmt.Sprintf("[SSO] %s: %s confirmed by email link", p.ID, pending.Identity.Email))
	s.finishSSOSignIn(w, r, p, pending.Identity, pending.Return, true)
}

func (s *Server) renderSSOConfirmPage(w http.ResponseWriter, r *http.Request, status int, title, message, form string) {
	page, err := staticFS.ReadFile("static/sso_confirm.html")
	if err != nil {
		http.Error(w, message, status)
		return
	}
	out := strings.ReplaceAll(string(page), "{{.Title}}", html.EscapeString(title))
	out = strings.ReplaceAll(out, "{{.Message}}", html.EscapeString(message))
	out = strings.ReplaceAll(out, "{{.Form}}", form)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(s.injectBaseTag([]byte(out), r)); err != nil {
		slog.Debug(fmt.Sprintf("[SSO] Writing the confirmation page: %v", err))
	}
}
