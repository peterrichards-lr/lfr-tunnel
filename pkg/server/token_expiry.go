package server

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Telling a Personal Access Token's holder that it is about to stop working (#2344).
//
// A token used to expire with no notice of any kind. The first sign was a tunnel that would not
// start -- reported as a client fault in #2342. Everything here goes only to someone who has
// proved they hold the token: the account's verified address, or a client that has just
// registered successfully with it. The refusal path is untouched, so nothing here lets an
// unauthenticated caller tell one token failure from another.

// loginCommand is the remedy the warnings name. It writes ~/.lfr-tunnel/token, which is where
// most holders keep the token; the client's own warning qualifies that by where the token was
// actually read from (cmd/lfr-tunnel), which the server cannot know.
const loginCommand = "lfr-tunnel login"

// The template fields every expiry email shares. Named because goconst counts the literals across
// the package and attributes them to the newest file to use them.
const (
	tmplName       = "Name"
	tmplExpiresAt  = "ExpiresAt"
	tmplPortalLink = "PortalLink"
)

// expiryEmailTimeFormat is how an expiry date is written in an email, matching the reservation
// emails so the two read alike. Always rendered in UTC: expires_at keeps whatever offset its writer
// passed, and two tokens should not read "+0530" and "UTC" in the same inbox.
const expiryEmailTimeFormat = "2006-01-02 15:04:05 MST"

// tokenExpiryWarningDays is token_expiry_warning_days, or the default when unset or not positive.
func (s *Server) tokenExpiryWarningDays() int {
	if s.cfg == nil || s.cfg.TokenExpiryWarningDays <= 0 {
		return config.DefaultTokenExpiryWarningDays
	}
	return s.cfg.TokenExpiryWarningDays
}

// checkExpiringTokens warns the holder of every live token entering the warning window, once.
//
// The same shape as checkExpiringReservations, on the same hourly ticker: the stage advances only
// after a successful send, so a failed email is retried by the next sweep instead of being
// recorded as delivered (#1724).
func (s *Server) checkExpiringTokens() {
	if s.db == nil || s.notifications == nil || s.notifications.Sender() == nil {
		return
	}

	now := time.Now()
	due, err := s.db.ListPATsDueExpiryWarning(now, now.AddDate(0, 0, s.tokenExpiryWarningDays()))
	if err != nil {
		slog.Error(fmt.Sprintf("[Server] Failed to list tokens due an expiry warning: %v", err))
		return
	}

	for _, pat := range due {
		user, err := s.db.GetUser(pat.UserID)
		if err != nil || user == nil {
			slog.Info(fmt.Sprintf("[Server] Failed to retrieve user %s for expiring token %d: %v", pat.UserID, pat.ID, err))
			continue
		}
		// Only a usable account. validatePAT already refuses every token of a suspended or
		// rejected user, so "replace it before it expires" would be advice about a credential
		// that does not work anyway. Left unmarked, so a reinstated user is still warned.
		if user.Status != db.UserStatusApproved {
			continue
		}
		// Transactional: this is the only notice before a credential stops working. Muting it
		// does not spare the holder noise, it costs them a tunnel at the moment they need it.
		if !shouldSendTo(user, emailTransactional) {
			continue
		}

		if err := s.sendTokenExpiringEmail(user, pat); err != nil {
			continue
		}
		// Re-read before marking: a send can take seconds, and an admin who extended the token
		// meanwhile has re-armed the warning for a NEW date. Marking it now would record that
		// date as warned when the email named the old one.
		if current, err := s.db.GetPATByID(pat.ID); err != nil || current.ExpiresAt == nil || !current.ExpiresAt.Equal(*pat.ExpiresAt) {
			continue
		}
		if err := s.db.MarkPATExpiryWarned(pat.ID); err != nil {
			slog.Error(fmt.Sprintf("[Server] Failed to record the expiry warning for token %d: %v", pat.ID, err))
		}
	}
}

// sendTokenExpiringEmail sends one token_expiring email, synchronously, and returns the send
// error so the sweep can decline to advance the stage on it.
func (s *Server) sendTokenExpiringEmail(user *db.User, pat *db.PersonalAccessToken) error {
	portalLink := s.getPortalBaseURL(nil) + "/portal"
	expiresStr := pat.ExpiresAt.UTC().Format(expiryEmailTimeFormat)

	body, err := s.renderEmailTemplate(user.LanguagePreference, "token_expiring.html", map[string]interface{}{
		tmplName:       user.FirstName,
		"TokenName":    pat.Name,
		"TokenPrefix":  pat.TokenPrefix,
		tmplExpiresAt:  expiresStr,
		"LoginCommand": loginCommand,
		tmplPortalLink: portalLink,
	})
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to render token_expiring email template: %v", err))
		body = fmt.Sprintf("<p>Hi %s,</p>"+
			"<p>Your access token <strong>%s</strong> (%s) expires on <strong>%s</strong>. After that, tunnels started with it will be refused.</p>"+
			"<p>To replace it, run <code>%s</code>, or create a new token in the Liferay Tunnel Portal.</p>"+
			"<p><a href=\"%s\">Go to Portal</a></p>",
			html.EscapeString(user.FirstName), html.EscapeString(pat.Name), html.EscapeString(pat.TokenPrefix),
			expiresStr, loginCommand, portalLink)
	}

	plain := fmt.Sprintf("Hi %s,\n\nYour access token %s (%s) expires on %s. After that, tunnels started with it will be refused.\n\nTo replace it, run:\n  %s\n\nor create a new token in the portal:\n%s",
		user.FirstName, pat.Name, pat.TokenPrefix, expiresStr, loginCommand, portalLink)
	subject := fmt.Sprintf("Access Token Expiring Soon: %s", pat.Name)

	return s.sendNotification(notifyTokenExpiring, user.Email, subject, body, plain)
}

// sendTokenPermanenceDecisionEmail tells a token's holder what an admin decided about their
// request for it never to expire (#2344).
//
// Before this neither outcome was announced. A denial matters most: under the `approval` policy a
// request for "never" creates a token that expires in defaultRequestedPATExpiryDays, and a holder
// who heard nothing has every reason to think it will not.
func (s *Server) sendTokenPermanenceDecisionEmail(r *http.Request, pat *db.PersonalAccessToken, granted bool) {
	if s.db == nil {
		return
	}
	user, err := s.db.GetUser(pat.UserID)
	if err != nil || user == nil {
		slog.Info(fmt.Sprintf("[Server] Failed to retrieve user %s for token permanence decision: %v", pat.UserID, err))
		return
	}
	// Transactional: on a denial it is the only statement that the token will still expire.
	if !shouldSendTo(user, emailTransactional) {
		return
	}

	portalLink := s.getPortalBaseURL(r) + "/portal"
	expiresStr := ""
	// A denial may land on a token that lapsed while it sat in the queue -- the decision path
	// allows a DENY there so an admin can clear the row (#2280). "It still expires on <a past
	// date>, you will be reminded" would be false, so a lapsed token is told it has expired.
	expired := false
	if pat.ExpiresAt != nil {
		expiresStr = pat.ExpiresAt.UTC().Format(expiryEmailTimeFormat)
		expired = !pat.ExpiresAt.After(time.Now())
	}

	body, err := s.renderEmailTemplate(user.LanguagePreference, "token_permanence_decided.html", map[string]interface{}{
		tmplName:       user.FirstName,
		"TokenName":    pat.Name,
		"TokenPrefix":  pat.TokenPrefix,
		"Granted":      granted,
		"Expired":      expired,
		tmplExpiresAt:  expiresStr,
		tmplPortalLink: portalLink,
	})

	var subject, plain string
	switch {
	case granted:
		subject = fmt.Sprintf("Access Token Will Not Expire: %s", pat.Name)
		plain = fmt.Sprintf("Hi %s,\n\nYour request for the access token %s (%s) never to expire has been approved. It no longer expires.\n\nPortal: %s",
			user.FirstName, pat.Name, pat.TokenPrefix, portalLink)
	case expired:
		subject = fmt.Sprintf("Access Token Request Declined: %s", pat.Name)
		plain = fmt.Sprintf("Hi %s,\n\nYour request for the access token %s (%s) never to expire was not approved, and the token has already expired. To keep using tunnels, run:\n  %s\n\nor create a new token in the portal:\n%s",
			user.FirstName, pat.Name, pat.TokenPrefix, loginCommand, portalLink)
	case expiresStr == "":
		subject = fmt.Sprintf("Access Token Request Declined: %s", pat.Name)
		plain = fmt.Sprintf("Hi %s,\n\nYour request for the access token %s (%s) never to expire was not approved. It keeps its current expiry, shown in the portal:\n%s",
			user.FirstName, pat.Name, pat.TokenPrefix, portalLink)
	default:
		subject = fmt.Sprintf("Access Token Will Still Expire: %s", pat.Name)
		plain = fmt.Sprintf("Hi %s,\n\nYour request for the access token %s (%s) never to expire was not approved. It still expires on %s.\n\nYou will be reminded before it does. Portal: %s",
			user.FirstName, pat.Name, pat.TokenPrefix, expiresStr, portalLink)
	}
	if err != nil {
		// The same fallback the expiry email has, so a broken template override drops
		// formatting rather than the decision itself.
		slog.Info(fmt.Sprintf("[Server] Failed to render token_permanence_decided email: %v", err))
		body = "<p>" + strings.ReplaceAll(html.EscapeString(plain), "\n", "<br>") + "</p>"
	}

	s.sendNotificationAsync(notifyTokenPermanenceDecided, user.Email, subject, body, plain)
}

// tokenExpiresAt renders a token's expiry for the registration response: RFC 3339 in UTC, or empty
// for a token that never expires, which omits the field.
func tokenExpiresAt(pat *db.PersonalAccessToken) string {
	if pat == nil || pat.ExpiresAt == nil {
		return ""
	}
	return pat.ExpiresAt.UTC().Format(time.RFC3339)
}
