package server

import (
	"fmt"
	"log/slog"
	"strings"
)

// Every notification email the gateway sends goes through this file.
//
// It exists because the same three-line block had been written eleven times and got it wrong
// eight of them. The shapes found in the tree on 2026-09-08 were:
//
//   - `go func() { _ = s.notifications.Sender().Send(...) }()` -- seven sites. The error was
//     discarded outright, so a failed send left no log line, no audit row, nothing anywhere.
//   - `slog.Info(...)` on failure -- four sites. Invisible to `journalctl -p err`, which is
//     exactly why #1824 stayed hidden across a whole deployment: an error-level search of the
//     production logs came back clean while the mail had never been arriving.
//   - ERROR plus an audit row -- four sites, the ones #1824 and #1832 had already reached.
//
// Fixing the eight instances would have left the twelfth site free to be written wrong again,
// so the behaviour is defined once, here, and asserted as a property rather than as instances:
// TestEveryNotificationSendGoesThroughTheFunnel parses every Go file in the repository and
// fails if a four-argument Send call appears anywhere but this file (#1732).

// The two audit actions this file writes.
//
// One canonical action for every kind of notification, deliberately -- not
// `user.magic_link.notify_failed`, `user.invite.notify_failed` and fourteen more. An owner asking
// "is there anybody we cannot email?" has to be able to ask it once. Both portal arms already
// render the admin audit log and the API already filters it by action and target, so
// `/api/admin/audit?action=notification.send_failed` answers that question today, in both arms,
// with no new surface to build. Sixteen action names would have meant knowing all sixteen.
//
// Which notification it was is not lost: it is the first word of the details string, and the
// V2 audit page's search covers details.
const (
	// ActionNotifySendFailed is written once per failed send, naming the recipient, the kind
	// of notification, what the recipient loses by not receiving it, and the underlying error.
	ActionNotifySendFailed = "notification.send_failed"

	// ActionNotifyRepeatedFailures is written once when a single address crosses
	// notifyRepeatThreshold consecutive failures. This is the "repeated" half of #1732: an
	// address that rejects mail permanently -- a departed employee's mailbox, a typo'd domain,
	// a hard bounce -- produces one of these, not one per attempt per hour forever.
	ActionNotifyRepeatedFailures = "notification.send_failing_repeatedly"
)

// notifyRepeatThreshold is the number of consecutive failures to one address that turns "a
// send failed" into "this address is unreachable".
//
// Three rather than one because a single failure is routinely transient -- a greylisting
// deferral, a restarting relay -- and an owner who is paged for those stops reading them. Three
// rather than ten because the policy-reminder sweep runs hourly against a grace window measured
// in days (#1707), so three is hours of warning, not days.
const notifyRepeatThreshold = 3

// notificationKind names a notification and, more importantly, states what the recipient loses
// when it never arrives.
//
// The impact sentence is the reason this is a table and not a bare string argument. An audit row
// reading "send failed" is satisfied by every failure mode there is and tells the operator
// nothing about whether to care; "so this user cannot sign in and has not been told" tells them
// immediately. Keeping the sentences here also makes this table the only inventory of what the
// gateway emails anyone -- which is what made the eleven-call-site count knowable at all.
type notificationKind struct {
	name   string
	impact string
}

var (
	notifyRegistrationVerification = notificationKind{
		"registration_verification",
		"this registration cannot be completed and the applicant has no other way in",
	}
	notifyAdminRegistrationNotice = notificationKind{
		"admin_registration_notice",
		"nobody has been told there is a registration waiting to be approved",
	}
	notifyRegistrationApproved = notificationKind{
		"registration_approved",
		"the approval stands but this user has not been told and has no claim link",
	}
	notifyRegistrationRejected = notificationKind{
		"registration_rejected",
		"the rejection stands but this user has not been told, so they will keep waiting for an " +
			"answer that has already been given",
	}
	notifyMagicLink = notificationKind{
		"magic_link",
		"this user cannot sign in and has not been told",
	}
	notifyInvite = notificationKind{
		"invite",
		"this account was created but the person it belongs to has never heard of it",
	}
	notifyAccessSuspended = notificationKind{
		"access_suspended",
		"this user's access was suspended with no notice and their tunnels simply stopped",
	}
	notifyRoleChanged = notificationKind{
		"role_changed",
		"this user's role changed without them being told",
	}
	notifyPolicyReminder = notificationKind{
		"policy_reminder",
		"this user gets no warning at all before new tunnels are refused at the consent deadline",
	}
	notifyVanityDomainHookFailed = notificationKind{
		"vanity_domain_hook_failed",
		"this user has not been told their custom domain may have no certificate and be unreachable",
	}
	notifySubdomainExpired = notificationKind{
		"subdomain_expired",
		"this user has no warning before their subdomain is released to the public pool",
	}
	notifySubdomainExpiring = notificationKind{
		"subdomain_expiring",
		"this user has no warning before their reservation lapses",
	}
	notifyAccountDeleted = notificationKind{
		"account_deleted",
		"this user has no confirmation that their data was erased, which is the record a GDPR erasure entitles them to",
	}
	notifySubdomainReserved = notificationKind{
		"subdomain_reserved",
		"this user is not told the reservation they asked for succeeded",
	}
	notifyExtensionApproved = notificationKind{
		"extension_approved",
		"this user is not told their extension was approved",
	}
	notifySubdomainDemoted = notificationKind{
		"subdomain_demoted",
		"this user is not told their permanent reservation was demoted",
	}
	notifyAdminAlert = notificationKind{
		"admin_alert",
		"the owner is not being told about the thing this alert was raised for",
	}
)

// errNoMailSender is returned when there is nothing configured to send with. Deliberately not
// audited: nothing was attempted, so nothing failed, and a gateway running without SMTP would
// otherwise fill its own audit log with rows describing its own configuration.
var errNoMailSender = fmt.Errorf("no mail sender is configured")

// sendNotification sends one notification email on the calling goroutine and records the
// outcome. The send error is returned for callers that need it (the policy sweep releases its
// claim on it; checkExpiringReservations declines to advance its warning stage on it) and can be
// ignored by the rest -- it has already been logged and audited by the time it is returned.
//
// Use sendNotificationAsync from a request handler. This form blocks.
func (s *Server) sendNotification(kind notificationKind, recipient, subject, htmlBody, plainBody string) error {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return errNoMailSender
	}

	if err := s.notifications.Sender().Send(recipient, subject, htmlBody, plainBody); err != nil {
		// ERROR, not INFO. This is the whole of #1824's invisibility: four of these sites logged
		// at INFO, so `journalctl -p err` reported a clean deployment while no mail was arriving.
		slog.Error(fmt.Sprintf("[Notify] Failed to send the %s email to %s: %v", kind.name, recipient, err))

		s.writeAudit(recipient, ActionNotifySendFailed, "notification", recipient,
			fmt.Sprintf("%s email to %s failed, so %s: %v", kind.name, recipient, kind.impact, err), nil)

		s.recordNotificationFailure(kind, recipient, err)
		return err
	}

	s.clearNotificationFailures(recipient)
	return nil
}

// sendNotificationAsync is sendNotification on its own goroutine, for the request paths.
//
// Asynchronous on purpose and unchanged from what it replaces: an SMTP timeout must not block an
// HTTP response, and on the magic-link path a blocking send would turn SMTP latency into a timing
// oracle for whether an address exists. What changes is that the outcome is now recorded either
// way, which is the entire point -- "fire and forget" had quietly come to mean "forget".
func (s *Server) sendNotificationAsync(kind notificationKind, recipient, subject, htmlBody, plainBody string) {
	// The one discarded send error in the package, and the only place one is defensible: there
	// is no caller left to return it to, and sendNotification has already logged it at ERROR,
	// written the audit row and counted it towards the repeated-failure threshold. That is the
	// difference between this line and the eleven it replaces.
	go func() { _ = s.sendNotification(kind, recipient, subject, htmlBody, plainBody) }() //nolint:errcheck
}

// recordNotificationFailure counts consecutive failures per address and writes exactly one
// escalation row when an address crosses the threshold.
//
// #1732 rejected two cheaper answers and it was right to:
//
//   - Firing an admin alert from the failure path makes the broken channel report on itself.
//     When SMTP is down globally, every alert about a failed send is itself a failed send, so
//     the one case where an owner most needs to hear about it is precisely the case where email
//     cannot tell them. Nothing here sends mail; it writes rows.
//   - A row per attempt has no dedup. A gateway-wide outage during a policy rollout would
//     produce one row per user per hourly sweep, indefinitely, and an owner cannot read that.
//     The escalation fires on the crossing only, so a permanently-dead address costs one row
//     per streak rather than one per hour.
//
// The counter is in memory and the durable evidence is the audit rows, not this map. A restart
// resets the streaks, which costs at most one repeated escalation row per address per restart --
// the per-failure trail is complete either way, and the alternative was a schema migration
// carrying state whose only consumer is the threshold test below.
func (s *Server) recordNotificationFailure(kind notificationKind, recipient string, sendErr error) {
	// Addresses are matched case-insensitively so one mailbox cannot hide below the threshold
	// by being spelled two ways -- which is not hypothetical, since a user's stored address and
	// an admin-configured one reach this from different places.
	key := strings.ToLower(strings.TrimSpace(recipient))

	s.notifyFailuresMu.Lock()
	if s.notifyFailures == nil {
		s.notifyFailures = make(map[string]int)
	}
	s.notifyFailures[key]++
	count := s.notifyFailures[key]
	s.notifyFailuresMu.Unlock()

	if count != notifyRepeatThreshold {
		return
	}

	slog.Error(fmt.Sprintf("[Notify] %d consecutive notification emails to %s have failed; "+
		"this address appears unreachable", count, recipient))
	s.writeAudit(recipient, ActionNotifyRepeatedFailures, "notification", recipient,
		fmt.Sprintf("%d consecutive notification emails to %s have failed, most recently the %s "+
			"email: %v. Every notice sent to this address is being lost, including the ones that "+
			"warn about losing access.", count, recipient, kind.name, sendErr), nil)
}

// clearNotificationFailures resets an address's streak once anything reaches it. Consecutive,
// not cumulative: an address that fails twice a month and works in between is not the thing
// #1732 is about, and counting it would eventually escalate every address on the system.
func (s *Server) clearNotificationFailures(recipient string) {
	key := strings.ToLower(strings.TrimSpace(recipient))

	s.notifyFailuresMu.Lock()
	defer s.notifyFailuresMu.Unlock()
	if s.notifyFailures != nil {
		delete(s.notifyFailures, key)
	}
}

// sendAdminAlert dispatches an operational alert to the configured admin address, honouring the
// per-alert admin setting and the recipient's notification preference.
//
// The preference logic still lives on NotificationService; only the send moved here, so that the
// owner's own alert channel is covered by the same recording as everything else. It was the
// worst-placed of the INFO-only sites: an alert that never arrives is indistinguishable from
// nothing having happened, which is the failure mode alerts exist to prevent.
func (s *Server) sendAdminAlert(settingKey, subject, htmlBody string) {
	if s.notifications == nil {
		return
	}
	recipient := s.notifications.adminAlertRecipient(settingKey)
	if recipient == "" {
		return
	}
	s.sendNotificationAsync(notifyAdminAlert, recipient, subject, htmlBody, "An alert has been triggered.")
}
