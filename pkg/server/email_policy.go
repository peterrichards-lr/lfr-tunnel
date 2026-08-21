package server

import "lfr-tunnel/pkg/db"

// emailClass says whether a recipient is allowed to mute a given email.
//
// The intent has always been that people receive everything unless they opt out, except
// for a set of messages that must arrive regardless. That policy was never expressed as
// a mechanism, so each send site re-derived it from `NotificationPrefs == "disabled"` and
// they drifted apart: the two registration approval paths ended up disagreeing, and an
// admin who unsubscribed silently broke registration for every future user because the
// approve link stopped being sent to anyone (issue #1135).
//
// Classifying at the send site keeps the decision in one place. Changing whether a
// particular email can be muted is now a one-word edit at its call site rather than a
// hunt through six independent conditions.
type emailClass int

const (
	// emailNotification is news about something that has already happened, which the
	// recipient can reasonably decide they do not want. Muting it loses them nothing
	// they cannot see in the portal.
	emailNotification emailClass = iota

	// emailTransactional carries the only means of acting on something, or warns of a
	// loss that silence would make irreversible. Muting it does not spare the recipient
	// noise, it breaks a workflow -- for them or for somebody else. These are sent
	// regardless of preference.
	//
	// Magic links and verification emails are transactional in the same sense but never
	// consulted NotificationPrefs in the first place, which is why they kept arriving
	// while admin alerts went silent.
	emailTransactional
)

// shouldSendTo reports whether an email of the given class should be delivered to user.
// A nil user means the recipient is not a known account -- an address configured by the
// operator rather than a registered person -- and there is no preference to honour.
func shouldSendTo(user *db.User, class emailClass) bool {
	if class == emailTransactional {
		return true
	}
	if user == nil {
		return true
	}
	return user.NotificationPrefs != "disabled"
}
