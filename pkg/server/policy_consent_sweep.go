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
	version := s.cfg.PolicyVersion

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
		if claimed {
			s.sendPolicyConsentWarningEmail(user, state, r)
		}
	}

	if !s.cfg.PolicyConsentStopsActiveTunnels {
		return
	}
	s.terminateExpiredConsentTunnels()
}

// sendPolicyConsentWarningEmail is the one message a user who lives entirely on the CLI
// may see before their tunnels stop.
//
// Sent as transactional rather than as a notification, so notification_prefs = "disabled"
// does not suppress it. That matches email_policy.go's own criterion: silence here makes
// the loss -- tunnel access cut off with no warning -- irreversible for the user, and the
// mail is not marketing, it is the last notice before enforcement.
func (s *Server) sendPolicyConsentWarningEmail(user *db.User, state ConsentState, r *http.Request) {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	if !shouldSendTo(user, emailTransactional) {
		return
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

	data := map[string]interface{}{
		"Name":       user.FirstName,
		"Deadline":   deadline,
		"Remaining":  remaining,
		"PortalLink": portalLink,
		"PolicyURL":  state.PolicyURL,
		"CookieURL":  state.CookieURL,
	}
	body, err := s.renderEmailTemplate(user.LanguagePreference, "policy_consent_reminder.html", data)
	if err != nil {
		slog.Info(fmt.Sprintf("[Consent] Failed to render the policy reminder template: %v", err))
		return
	}
	subject := "Action required: accept the updated Privacy Policy"
	plain := fmt.Sprintf(
		"Hi %s,\n\nThe Privacy Policy and Cookie Disclosure have been updated. Your acceptance is due within %s (by %s).\n\nAfter that, new tunnels will be refused until you accept. Tunnels already running are not interrupted.\n\nAccept here: %s\n",
		user.FirstName, remaining, deadline, portalLink,
	)
	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }() //nolint:errcheck
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
