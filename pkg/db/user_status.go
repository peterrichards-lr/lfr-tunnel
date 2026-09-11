package db

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
// NOTE the ~160 existing literals are deliberately NOT replaced here. That is a large mechanical
// diff and belongs in its own PR; burying it alongside the introduction of the enum would make
// both unreviewable, which is the same reason #1851 says not to bundle it with a feature.
const (
	// UserStatusUnverified is a registration whose email has not been confirmed yet. The
	// applicant has requested an account; nobody has been asked to approve anything.
	UserStatusUnverified = "unverified"

	// UserStatusPending is a verified registration waiting for an admin decision.
	UserStatusPending = "pending"

	// UserStatusApproved is a usable account. handleAdminMagicLink gates on exactly this value,
	// so it is the one that decides whether someone can sign in.
	UserStatusApproved = "approved"

	// UserStatusRejected is a registration an admin declined (#1830).
	//
	// The row is KEPT rather than deleted, and that is a safety property rather than a courtesy:
	// handleSSOCallback auto-provisions an UNKNOWN address as fully approved on first sign-in, so
	// deleting the row would hand the rejected person the approval back -- sign in via SSO, be
	// unknown, be created approved. The row is what gives every path something to check.
	UserStatusRejected = "rejected"

	// UserStatusRevoked is an account whose access has been withdrawn after the fact, as opposed
	// to a registration that was never accepted.
	UserStatusRevoked = "revoked"
)

// UserStatuses is the complete vocabulary, in lifecycle order: a registration arrives
// unverified, becomes pending on email confirmation, and then reaches one of the three terminal
// states. Order is the portal's dropdown order, so this slice is the list rather than a set that
// something else has to sort.
//
// Anything that enumerates statuses -- a filter, a dropdown, a badge mapping -- must derive from
// this. scripts/check-status-vocabulary.cjs fails the build if the portal's copy disagrees.
var UserStatuses = []string{
	UserStatusUnverified,
	UserStatusPending,
	UserStatusApproved,
	UserStatusRejected,
	UserStatusRevoked,
}

// IsValidUserStatus reports whether s is a known user status.
//
// Worth having even though nothing validates today: a typo in a bare literal -- "aproved" --
// compiles, passes review, and then silently denies or grants access depending on which side of
// the comparison it lands on. That is the failure mode the enum exists to remove.
func IsValidUserStatus(s string) bool {
	for _, known := range UserStatuses {
		if s == known {
			return true
		}
	}
	return false
}
