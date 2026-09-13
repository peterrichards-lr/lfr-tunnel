package db

import "strings"

// The user status vocabulary (#1851).
//
// Before this, the vocabulary lived in six places and was checked in none: ~160 bare literals in
// pkg/server, two unexported constants in pkg/server/register_reject.go, the statusOptions array
// in ui/src/pages/AdminUsers.tsx, two badge ternaries that defaulted in OPPOSITE directions,
// portal V1's filters in pkg/server/static/dashboard.js, and ten Language*.properties bundles.
// Adding a status therefore cost an edit in every place that happened to know the vocabulary, and
// misses were silent.
//
// #1847 is the proof: #1830 added "rejected", the admin portal did not learn about it, and a
// rejected user could not be filtered for or -- the part that mattered -- un-rejected, since that
// dropdown is the control reversing a rejection is meant to use. Nothing failed. A human asked.
//
// It lives in pkg/db rather than pkg/server because this is the vocabulary of the User row itself
// (models.go's Status field), and because pkg/server imports pkg/db rather than the reverse.
// Exported so scripts/check-status-vocabulary.cjs can read it: the portal's list is checked
// against this set rather than agreeing with it by convention.
//
// The literals were replaced in #1870 and the type became distinct in #1881, in that order and
// deliberately: the substitution had to land first or both diffs would have been unreviewable.
//
// UserStatus is a named type rather than a bare string so that a typo is a COMPILE error instead
// of a silent access decision. `user.Status = "aproved"` used to build cleanly and deny that
// person access forever; it no longer builds. database/sql scans and writes a named string type
// unchanged, so neither the column nor the JSON on the wire is affected -- verified before this
// change was attempted.
type UserStatus string

const (
	// UserStatusUnverified is a registration whose email has not been confirmed yet. The
	// applicant has requested an account; nobody has been asked to approve anything.
	UserStatusUnverified UserStatus = "unverified"

	// UserStatusPending is a verified registration waiting for an admin decision.
	UserStatusPending UserStatus = "pending"

	// UserStatusApproved is a usable account. handleAdminMagicLink gates on exactly this value,
	// so it is the one that decides whether someone can sign in.
	UserStatusApproved UserStatus = "approved"

	// UserStatusRejected is a registration an admin declined (#1830).
	//
	// The row is KEPT rather than deleted, and that is a safety property rather than a courtesy:
	// handleSSOCallback auto-provisions an UNKNOWN address as fully approved on first sign-in, so
	// deleting the row would hand the rejected person the approval back -- sign in via SSO, be
	// unknown, be created approved. The row is what gives every path something to check.
	UserStatusRejected UserStatus = "rejected"

	// UserStatusRevoked is an account whose access has been withdrawn after the fact, as opposed
	// to a registration that was never accepted.
	UserStatusRevoked UserStatus = "revoked"
)

// UserStatuses is the complete vocabulary, in lifecycle order: a registration arrives
// unverified, becomes pending on email confirmation, and then reaches one of the three terminal
// states. Order is the portal's dropdown order, so this slice is the list rather than a set that
// something else has to sort.
//
// Anything that enumerates statuses -- a filter, a dropdown, a badge mapping -- must derive from
// this. scripts/check-status-vocabulary.cjs fails the build if the portal's copy disagrees.
var UserStatuses = []UserStatus{
	UserStatusUnverified,
	UserStatusPending,
	UserStatusApproved,
	UserStatusRejected,
	UserStatusRevoked,
}

// UserStatusList renders the vocabulary for an error message a human has to act on.
//
// Exists because strings.Join cannot take []UserStatus, and the alternative -- converting at
// each call site -- would put the same three lines wherever the list is printed.
func UserStatusList() string {
	out := make([]string, 0, len(UserStatuses))
	for _, s := range UserStatuses {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

// IsValidUserStatus reports whether s is a known user status.
//
// Worth having even though nothing validates today: a typo in a bare literal -- "aproved" --
// compiles, passes review, and then silently denies or grants access depending on which side of
// the comparison it lands on. That is the failure mode the enum exists to remove.
func IsValidUserStatus(s string) bool {
	for _, known := range UserStatuses {
		if s == string(known) {
			return true
		}
	}
	return false
}
