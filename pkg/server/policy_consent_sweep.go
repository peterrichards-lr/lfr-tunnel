package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"lfr-tunnel/pkg/db"
)

// runPolicyConsentSweep is the part of re-consent enforcement that cannot be driven by a
// request (#1707).
//
// Two jobs, in this order:
//
//  1. Email each user once when they enter the warning window. The portal banner and the
//     client's startup warning both require the user to show up; somebody who neither opens
//     the portal nor starts a tunnel during the whole window would otherwise be cut off
//     having had no notice through any channel they actually saw.
//  2. If -- and only if -- policy_consent_stops_active_tunnels is set, drop the tunnels of
//     users whose window has expired. Off by default: refusing the NEXT tunnel already
//     enforces the policy completely, because no tunnel survives a restart, and dropping a
//     live one is the outcome the warning exists to prevent.
//
// Runs on the existing hourly prune ticker rather than a timer of its own. Hourly is fine
// for a deadline measured in days, and a quiet server would never notice the boundary if
// this hung off registrations instead.
func (s *Server) runPolicyConsentSweep(r *http.Request) {
	if s.cfg == nil || s.db == nil || s.cfg.PolicyVersion == "" {
		return
	}

	// A gateway with no mail sender skips the warning phase rather than working through
	// it: the claim below is a compare-and-set with no second reader, so claiming when
	// nothing can be sent would record warnings that never left the building and leave
	// them for nobody to retry. Enforcement still runs -- it does not depend on mail.
	if s.notifications != nil && s.notifications.Sender() != nil {
		s.warnUsersEnteringConsentWindow(s.cfg.PolicyVersion, r)
	}

	if !s.cfg.PolicyConsentStopsActiveTunnels {
		return
	}
	s.terminateExpiredConsentTunnels()
}

// warnUsersEnteringConsentWindow emails each user once, on entering the warning window.
//
// The ordering is claim, then send, then release if the send failed. Each step is there
// for a reason and the reasons pull against each other:
//
//   - Claiming BEFORE the send is what makes this safe to run on every tick and on more
//     than one gateway process. MarkWarningNotified is a compare-and-set; the loser gets
//     false and stays quiet. Sending first and marking afterwards -- the pattern
//     checkExpiringReservations uses -- would let two sweeps both send.
//   - Releasing AFTER a failed send is what stops that claim becoming a lie (#1724).
//     Without it the user is permanently recorded as warned having never been warned,
//     and under #1707 their clients stop at expiry with no notice through any channel
//     they saw.
//
// The two coexist because the release only ever happens on the path where nothing was
// sent. The row returns to NULL exactly when nobody has been mailed, so a racing sweep
// that claims it next is retrying the send rather than repeating it.
func (s *Server) warnUsersEnteringConsentWindow(version string, r *http.Request) {
	// Anyone whose first sight is older than (grace - warning) has entered the warning
	// window. The query narrows the scan; whether each user has since accepted is decided
	// below, per user, because acceptance lives in a different table.
	cutoff := time.Now().UTC().Add(-time.Duration(s.consentGraceDays()-s.consentWarningDays()) * 24 * time.Hour)
	candidates, err := s.db.ListPendingWarnings(PolicyDocumentID, version, cutoff)
	if err != nil {
		slog.Error("[Consent] Failed to list users due a policy warning", "error", err)
		return
	}

	for _, userID := range candidates {
		user, err := s.db.GetUser(userID)
		if err != nil || user == nil {
			continue
		}
		state := s.policyConsentState(user, false)
		if !state.Required {
			continue
		}
		// Claim the send before sending. MarkWarningNotified is the compare-and-set: a
		// second sweep, or a second gateway process, gets false and stays quiet.
		claimed, err := s.db.MarkWarningNotified(userID, PolicyDocumentID, version)
		if err != nil {
			slog.Error("[Consent] Failed to claim the policy warning email", "user", userID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendPolicyConsentWarningEmail(user, state, r); err != nil {
			slog.Error("[Consent] Failed to send the policy reminder", "user", userID, "error", err)
			if relErr := s.db.ClearWarningNotified(userID, PolicyDocumentID, version); relErr != nil {
				// Mail and the database are failing at the same time. There is nothing
				// better to do from here -- a retry against a database that has just
				// refused a write has no better prospect, and holding the loop open makes
				// the rest of the sweep wait on it -- so state the consequence plainly
				// instead of logging a bare error. This is the one path where the record
				// still ends up claiming a warning that did not happen, and the log line
				// is the only thing that will ever say so.
				slog.Error("[Consent] Could not release the policy warning claim: this user is recorded as warned but was not, and no later sweep will retry",
					"user", userID, "error", relErr)
			}
		}
	}
}

// sendPolicyConsentWarningEmail is the one message a user who lives entirely on the CLI
// may see before their tunnels stop.
//
// Sent as transactional rather than as a notification, so notification_prefs = "disabled"
// does not suppress it. That matches email_policy.go's own criterion: silence here makes
// the loss -- tunnel access cut off with no warning -- irreversible for the user, and the
// mail is not marketing, it is the last notice before enforcement.
func (s *Server) sendPolicyConsentWarningEmail(user *db.User, state ConsentState, r *http.Request) error {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return fmt.Errorf("no mail sender is configured")
	}
	if !shouldSendTo(user, emailTransactional) {
		// Declining to send is a decision, not a failure: a retry next hour would decide
		// the same thing, forever. nil so the caller keeps the claim.
		return nil
	}

	portalLink := state.PortalURL
	if portalLink == "" {
		portalLink = s.getPortalBaseURL(r) + "/portal"
	}
	deadline := state.Deadline
	if deadline == "" {
		deadline = "shortly"
	}
	remaining := formatConsentRemaining(state.SecondsRemaining)

	body, err := s.renderEmailTemplate(user.LanguagePreference, "policy_consent_reminder.html", policyReminderEmail{
		Name:       user.FirstName,
		Deadline:   deadline,
		Remaining:  remaining,
		PortalLink: portalLink,
		PolicyURL:  state.PolicyURL,
		CookieURL:  state.CookieURL,
	})
	if err != nil {
		return fmt.Errorf("rendering the policy reminder template: %w", err)
	}
	subject := "Action required: accept the updated Privacy Policy"
	plain := fmt.Sprintf(
		"Hi %s,\n\nThe Privacy Policy and Cookie Disclosure have been updated. Your acceptance is due within %s (by %s).\n\nAfter that, new tunnels will be refused until you accept. Tunnels already running are not interrupted.\n\nAccept here: %s\n",
		user.FirstName, remaining, deadline, portalLink,
	)
	// Returned, not logged-and-swallowed, and sent on this goroutine rather than a
	// detached one (#1724). The caller claimed this send before making it, and a claim can
	// only be released by somebody who observes the outcome -- a fire-and-forget `go func`
	// has no outcome to hand back. The reminder is the only warning a CLI-only user gets
	// before their client stops at expiry, so the caller needs to know.
	//
	// Blocking the sweep is affordable: it runs on the hourly prune ticker, where
	// checkExpiringReservations already sends synchronously for the same reason, and
	// mail.SMTPClient dials with its own timeout.
	return s.notifications.Sender().Send(user.Email, subject, body, plain)
}

// policyReminderEmail is the template context for policy_consent_reminder.html. A named
// type rather than a map so a renamed field is a compile error rather than a blank in
// somebody's inbox.
type policyReminderEmail struct {
	Name       string
	Deadline   string
	Remaining  string
	PortalLink string
	PolicyURL  string
	CookieURL  string
}

// terminateExpiredConsentTunnels drops the live tunnels of users past their deadline.
//
// Only reached when policy_consent_stops_active_tunnels is on. Kept in its own function
// so the default path can be read as "this never happens" rather than as a condition
// buried in a loop.
//
// Both halves of the fleet are covered, which is a requirement rather than thoroughness:
// an edge node keeps its own in-memory registry and knows nothing about consent, so
// kicking only the control plane's copy would leave a split brain where the edge kept
// serving a tunnel central believed was gone (see the edge-sync rules).
func (s *Server) terminateExpiredConsentTunnels() {
	expired := make(map[string]bool)
	blocking := func(userID string) bool {
		if seen, ok := expired[userID]; ok {
			return seen
		}
		user, err := s.db.GetUser(userID)
		if err != nil || user == nil {
			// Leases key on the user id, but some historical rows carry the email.
			user, err = s.db.GetUserByEmail(userID)
			if err != nil || user == nil {
				expired[userID] = false
				return false
			}
		}
		result := s.policyConsentState(user, false).Blocking()
		expired[userID] = result
		return result
	}

	for _, lease := range s.registry.ListLeases() {
		if blocking(lease.UserID) {
			slog.Warn("[Consent] Dropping an active tunnel: the policy grace window has expired",
				"user", lease.UserID, "subdomain", lease.SubdomainPrefix)
			s.registry.KickLease(lease.SubdomainPrefix)
		}
	}

	type edgeTarget struct {
		nodeID    string
		subdomain string
		userID    string
	}
	var targets []edgeTarget
	s.edgeLeasesMu.RLock()
	for _, userLeases := range s.edgeLeases {
		for _, el := range userLeases {
			targets = append(targets, edgeTarget{nodeID: el.NodeID, subdomain: el.Subdomain, userID: el.UserID})
		}
	}
	s.edgeLeasesMu.RUnlock()

	// Outside the lock: sendEdgeWSKick writes to the edge control channel, and holding a
	// registry lock across a network write is how a broadcast deadlocks a gateway.
	for _, t := range targets {
		if !blocking(t.userID) {
			continue
		}
		slog.Warn("[Consent] Dropping an active edge tunnel: the policy grace window has expired",
			"user", t.userID, "subdomain", t.subdomain, "node", t.nodeID)
		s.sendEdgeWSKick(t.nodeID, t.subdomain)
	}
}
