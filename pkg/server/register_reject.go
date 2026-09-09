package server

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"lfr-tunnel/pkg/db"
)

// The admin-initiated rejection half of the registration lifecycle (#1830).
//
// Before this, a registration the owner did not want to grant had exactly one outcome: it sat at
// "pending" forever and the applicant was never told anything. That is #1824's failure mode
// arrived at from the other direction -- there an approval email never sent, here a decision was
// never communicated at all.
//
// The owner settled the product question on 2026-09-08 and the reasoning is worth keeping next to
// the code, because the obvious instinct is to reject silently:
//
//   - The "do not confirm an address, do not assist enumeration" argument is sound in general and
//     is ALREADY implemented upstream here. handleRegisterRequest drops an out-of-domain address
//     with an audit row and no mail, and handleAdminMagicLink answers {"status":"ok"} to one. So
//     an explicit admin rejection can only ever be aimed at somebody who already passed the
//     domain gate -- a colleague. Silence towards that person buys no security.
//   - It costs what #1824 cost: two people waited weeks for a message that was never sent. With
//     nothing telling them not to, a rejected applicant also re-registers indefinitely.
//
// So: notify by default, with an optional reason and an explicit "reject silently" opt-out for
// the rare bad actor who cleared the domain gate. The domain-gate rejection stays silent and is
// not touched by any of this.

const (
	// statusRejected is the status an admin-rejected registration is left in.
	//
	// The row is KEPT rather than deleted, which is the design question #1830 left open. Both
	// answers are defensible on friendliness grounds -- deletion lets a colleague who fixed
	// whatever the problem was simply register again -- but only one of them is safe, and the
	// reason is in sso.go:
	//
	// handleSSOCallback auto-provisions an unknown address as a fully APPROVED user on first
	// sign-in (that is deliberate: once Liferay SSO is configured the identity provider is the
	// access decision). Deleting the row on rejection therefore hands the rejected person the
	// approval directly -- sign in through SSO, be unknown, be created approved. Rejection would
	// last exactly as long as it took them to click "Sign in with Liferay".
	//
	// Keeping the row is what gives every path something to check, and approveOnSSOSignIn now
	// refuses it. The admin is not locked out of reversing the decision: PATCH /api/admin/users/<email>
	// sets status directly, so un-rejecting is one dropdown change in the portal.
	statusRejected = "rejected"

	// statusPending is the status a verified registration waits in for a decision. Named here
	// rather than spelled as a literal because goconst counts the package's nine occurrences
	// against whichever file is newest, and because the two halves of the decision have to agree
	// on exactly which status they act on.
	statusPending = "pending"

	// fallbackGreetingName is the salutation for a registration that gave no usable name. Same
	// reason as above; the literal appears six times across this package's email builders.
	fallbackGreetingName = "there"

	// ActionUserRejected is the audit action for an admin declining a registration.
	//
	// Written unconditionally, before the email is attempted and regardless of whether it
	// succeeds or was deliberately suppressed. The audit entry is the record; the email is the
	// courtesy. A row that only exists when the mail went is a record of the mail, not of the
	// decision.
	ActionUserRejected = "user.rejected"

	// ActionUserApproved is the audit action for an admin approving a registration through the
	// emailed link.
	//
	// It did not exist before #1830. approveOnSSOSignIn has written "user.approved.sso" since
	// #1824 specifically because "pending -> approved is the most security-relevant transition a
	// user can undergo and it left no trace naming what did it" -- and that was equally true of
	// the admin link, which is the path most registrations actually take. Adding the rejection
	// row without this one would have left the audit log able to answer "who was declined?" and
	// not "who was let in?".
	ActionUserApproved = "user.approved"

	// actorApprovalLink is the actor recorded for both halves of the emailed decision.
	//
	// Neither endpoint requires a session, by design: the token in the link is the entire
	// credential, and it is sent to admin_notification_email. So the honest answer to "who did
	// this" is "whoever held the approval link", not a named person, and naming the configured
	// admin mailbox would assert an identity nothing verified. The IP address on the row is the
	// part that distinguishes one link-holder from another.
	actorApprovalLink = "admin-approval-link"

	// maxRejectionReasonLen bounds the free-text reason. It is echoed into an email and an audit
	// row, both of which an operator has to be able to read.
	maxRejectionReasonLen = 1000
)

// handleRejectUser handles admin clicks on rejection links.
//
// Deliberately the mirror image of handleApproveUser, including its GET/POST split: a GET only
// describes what would happen and a POST performs it. That split exists because these links are
// emailed to admin_notification_email, which on this deployment is a Slack email-to-channel
// bridge -- link previews, crawlers and prefetchers all follow URLs in a channel, and any of them
// fetching a rejection link would decline a registration nobody looked at (#1143 is the same
// finding on the approve side).
func (s *Server) handleRejectUser(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database storage not enabled", http.StatusNotImplemented)
		return
	}

	email := r.URL.Query().Get("email")
	token := r.URL.Query().Get("token")
	reason := ""
	silent := false
	if r.Method == http.MethodPost {
		// Hidden fields on the confirmation form, keeping the token out of a URL the browser
		// would retain in history or leak in a Referer.
		if err := r.ParseForm(); err == nil {
			if v := r.PostFormValue("email"); v != "" {
				email = v
			}
			if v := r.PostFormValue("token"); v != "" {
				token = v
			}
			reason = strings.TrimSpace(r.PostFormValue("reason"))
			silent = r.PostFormValue("silent") != ""
		}
	}

	if email == "" || token == "" {
		http.Error(w, "Missing email or token parameters", http.StatusBadRequest)
		return
	}

	if len([]rune(reason)) > maxRejectionReasonLen {
		reason = string([]rune(reason)[:maxRejectionReasonLen])
	}

	user, err := s.db.GetUser(email)
	if err != nil {
		http.Error(w, "User request not found", http.StatusNotFound)
		return
	}

	// Same guard as the approve half, and it is what stops a rejection landing on an already
	// approved account: only a pending registration holding this exact token can be declined.
	if user.Status != statusPending || user.ApprovalToken != token {
		http.Error(w, "Invalid rejection link or request already processed", http.StatusGone)
		return
	}

	if r.Method != http.MethodPost {
		s.renderRejectionConfirmation(w, user, email, token)
		return
	}

	user.Status = statusRejected
	// Consumed by this decision. Leaving it live would leave the emailed APPROVE link armed
	// against a user who has just been rejected -- the stale-link defect #1824 found on the SSO
	// path, in the one place where following it would silently reverse an admin's decision.
	user.ApprovalToken = ""

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, "Failed to update user status", http.StatusInternalServerError)
		return
	}
	s.invalidateUserCache(user.Email)

	// Before the send, and not in its error path. #1830 point 2: "the audit row must not be
	// conditional on the send succeeding". A test drives this handler with a sender that fails
	// every send and requires the row anyway.
	s.writeAudit(actorApprovalLink, ActionUserRejected, "user", user.Email,
		rejectionAuditDetails(reason, silent), r)

	mailConfigured := s.notifications != nil && s.notifications.Sender() != nil
	var emailErr error
	if !silent && mailConfigured {
		subject := "[Liferay Tunnel] Your registration request was not approved"
		htmlBody, plainBody := rejectionEmailBodies(user, reason)
		// Synchronous for the same reason the approve half is (#1824): emailErr decides what the
		// page below tells the admin, and a page cannot report an outcome that has not happened
		// yet. sendNotification has already logged at ERROR and written the notification.send_failed
		// row by the time it returns.
		emailErr = s.sendNotification(notifyRegistrationRejected, user.Email, subject, htmlBody, plainBody)
	}

	s.renderRejectionOutcome(w, user, silent, mailConfigured, emailErr)
}

// rejectionAuditDetails is the sentence an operator reads six months later. It has to say what
// was decided, whether the person was told, and why -- "rejected" alone is satisfied by every
// rejection there has ever been and answers none of the questions that get asked afterwards.
func rejectionAuditDetails(reason string, silent bool) string {
	notice := "the applicant was emailed"
	if silent {
		notice = "no email was sent (the admin chose to reject silently)"
	}
	if reason == "" {
		return fmt.Sprintf("Registration rejected via the emailed admin link; no reason was given; %s", notice)
	}
	return fmt.Sprintf("Registration rejected via the emailed admin link; reason: %q; %s", reason, notice)
}

// rejectionEmailBodies builds the notice sent to the applicant.
//
// Short, and it does not dead-end: the last line is a route back to a human. That is the whole
// point of notifying rather than staying silent -- a rejection with no next step leaves the
// person chasing the owner over Slack, which is the outcome this replaces.
//
// Everything interpolated is escaped. The name comes from an unverified registration form and
// the reason is free text an admin just typed into a browser.
func rejectionEmailBodies(user *db.User, reason string) (htmlBody, plainBody string) {
	name := strings.TrimSpace(user.PreferredName)
	if name == "" {
		name = strings.TrimSpace(user.FirstName)
	}
	if name == "" {
		name = fallbackGreetingName
	}

	reasonHTML := ""
	reasonPlain := ""
	if reason != "" {
		reasonHTML = fmt.Sprintf("<p><strong>Reason given:</strong> %s</p>", html.EscapeString(reason))
		reasonPlain = fmt.Sprintf("Reason given: %s\n\n", reason)
	}

	htmlBody = fmt.Sprintf("<p>Hi %s,</p>"+
		"<p>Your request for access to the Liferay Tunnel gateway has not been approved, so no "+
		"account has been created for you.</p>"+
		"%s"+
		"<p>If you think this is a mistake, just reply to this email and we will take another "+
		"look.</p>",
		html.EscapeString(name), reasonHTML)

	plainBody = fmt.Sprintf("Hi %s,\n\n"+
		"Your request for access to the Liferay Tunnel gateway has not been approved, so no "+
		"account has been created for you.\n\n"+
		"%s"+
		"If you think this is a mistake, just reply to this email and we will take another look.\n",
		name, reasonPlain)

	return htmlBody, plainBody
}

// rejectionURL builds the link that goes in the admin's registration email, alongside the approve
// one. Exported to the two call sites in server.go rather than formatted twice, because the two
// admin emails there had already drifted apart in every other respect.
func rejectionURL(scheme, host, email, approvalToken string) string {
	return fmt.Sprintf("%s://%s/api/admin/reject?email=%s&token=%s",
		scheme, host, url.QueryEscape(email), url.QueryEscape(approvalToken))
}

// renderRejectionConfirmation shows what a submission would do, and is where the two admin
// choices #1830 asks for actually live: an optional reason, and an explicit "reject silently".
//
// Silence is a checkbox rather than the default so that it is a decision. Everything is escaped:
// the name and email come from an unverified registration.
func (s *Server) renderRejectionConfirmation(w http.ResponseWriter, user *db.User, email, token string) {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = email
	}

	page := fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="robots" content="noindex,nofollow">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Reject registration</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#f5f6f8;margin:0;padding:2rem;color:#1f2933}
.card{max-width:32rem;margin:3rem auto;background:#fff;border-radius:8px;padding:2rem;box-shadow:0 1px 3px rgba(0,0,0,.12)}
h1{font-size:1.25rem;margin:0 0 1rem}
dl{margin:0 0 1.5rem}dt{font-size:.75rem;text-transform:uppercase;color:#616e7c;margin-top:.75rem}
dd{margin:.15rem 0 0;font-weight:600}
label{display:block;font-size:.8rem;color:#616e7c;margin:0 0 .35rem}
textarea{width:100%%;box-sizing:border-box;font:inherit;padding:.5rem;border:1px solid #cbd2d9;border-radius:6px;min-height:5rem}
.check{display:flex;gap:.5rem;align-items:flex-start;margin:1rem 0}
.check label{margin:0;color:#1f2933;font-size:.85rem}
button{background:#c1272d;color:#fff;border:0;border-radius:6px;padding:.65rem 1.25rem;font-size:1rem;cursor:pointer}
p.note{color:#616e7c;font-size:.8rem;margin-top:1.25rem}
</style></head>
<body><div class="card">
<h1>Reject this registration?</h1>
<dl>
<dt>Name</dt><dd>%s</dd>
<dt>Email</dt><dd>%s</dd>
</dl>
<form method="POST" action="/api/admin/reject">
<input type="hidden" name="email" value="%s">
<input type="hidden" name="token" value="%s">
<label for="reason">Reason (optional &mdash; it is included in the email they receive)</label>
<textarea id="reason" name="reason" maxlength="%d"></textarea>
<div class="check">
<input type="checkbox" id="silent" name="silent" value="1">
<label for="silent">Reject <strong>silently</strong> &mdash; send them nothing. For a genuine bad
actor who got past the email-domain check; the rejection is still recorded in the audit log.</label>
</div>
<button type="submit">Reject</button>
</form>
<p class="note">Rejecting refuses this request and blocks re-registration with the same address.
Unless you tick the box above, the person is emailed a short notice inviting them to reply if it
is a mistake. Close this page to do nothing.</p>
</div></body></html>`,
		html.EscapeString(name),
		html.EscapeString(email),
		html.EscapeString(email),
		html.EscapeString(token),
		maxRejectionReasonLen,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if _, err := w.Write([]byte(page)); err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to write rejection confirmation: %v", err))
	}
}

// renderRejectionOutcome tells the admin what actually happened, which is the #1824 lesson
// applied to the new path rather than re-learned on it: the approval page used to claim "an email
// has been sent" unconditionally, so a failed send left the approver believing the applicant had
// been told. The rejection ITSELF has happened in all four branches below; only the notice is in
// doubt, and saying which is what lets an admin follow up by hand.
func (s *Server) renderRejectionOutcome(w http.ResponseWriter, user *db.User, silent, mailConfigured bool, emailErr error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var page string
	switch {
	case silent:
		page = "<h1>Registration Rejected &mdash; silently</h1>" +
			"<p>The request has been rejected and <strong>no email was sent</strong>, as you " +
			"asked. The rejection is recorded in the audit log.</p>" +
			"<p>This address can no longer register again; change the user's status from the " +
			"admin portal if you want to reverse it.</p>"
	case !mailConfigured:
		page = "<h1>Registration Rejected &mdash; but nothing could be emailed</h1>" +
			"<p>The request <strong>has been rejected</strong>. This gateway has no mail sender " +
			"configured, so <strong>the applicant has not been told</strong>.</p>" +
			"<p>Contact them directly. The rejection is recorded in the audit log.</p>"
	case emailErr != nil:
		page = "<h1>Registration Rejected &mdash; but the email did NOT send</h1>" +
			"<p>The request <strong>has been rejected</strong>. However the notification email " +
			"failed, so <strong>they have not been told</strong> and are still waiting for an " +
			"answer.</p>" +
			"<p>Contact them directly. Both the rejection and the failed send are recorded in " +
			"the audit log.</p>"
	default:
		page = "<h1>Registration Rejected</h1>" +
			"<p>The request has been rejected and the applicant has been emailed a short notice " +
			"inviting them to reply if it is a mistake.</p>" +
			"<p>This address can no longer register again; change the user's status from the " +
			"admin portal if you want to reverse it.</p>"
	}

	if _, err := w.Write([]byte(page)); err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to write rejection outcome for %s: %v", user.Email, err))
	}
}
