package db

import "testing"

// The slice is what declares membership -- the gate reads it, not the const block -- so a
// constant that exists but was never added to it is exactly the half-added status this vocabulary
// exists to prevent. Assert the two cannot drift.
func TestUserStatusesContainsEveryConstant(t *testing.T) {
	declared := []string{
		UserStatusUnverified,
		UserStatusPending,
		UserStatusApproved,
		UserStatusRejected,
		UserStatusRevoked,
	}

	if len(UserStatuses) != len(declared) {
		t.Fatalf("UserStatuses has %d entries, %d constants are declared -- one was added without the other",
			len(UserStatuses), len(declared))
	}

	for _, s := range declared {
		if !IsValidUserStatus(s) {
			t.Errorf("%q is declared as a constant but missing from UserStatuses", s)
		}
	}
}

func TestUserStatusesHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range UserStatuses {
		if seen[s] {
			t.Errorf("%q appears twice in UserStatuses", s)
		}
		seen[s] = true
	}
}

func TestIsValidUserStatusRejectsTypos(t *testing.T) {
	// The failure the enum exists to remove: "aproved" compiles, passes review, and then denies
	// or grants access depending on which side of a comparison it lands.
	for _, bad := range []string{"aproved", "Approved", "APPROVED", "", "deleted", "active"} {
		if IsValidUserStatus(bad) {
			t.Errorf("IsValidUserStatus(%q) = true, want false", bad)
		}
	}
}

func TestUserStatusValuesAreStable(t *testing.T) {
	// These strings are persisted in the users table and compared by handleAdminMagicLink, so
	// renaming one is a migration, not a refactor. Pinned so that is a deliberate act.
	want := map[string]string{
		"unverified": UserStatusUnverified,
		"pending":    UserStatusPending,
		"approved":   UserStatusApproved,
		"rejected":   UserStatusRejected,
		"revoked":    UserStatusRevoked,
	}
	for literal, constant := range want {
		if literal != constant {
			t.Errorf("constant for %q is %q -- changing a persisted status value needs a migration", literal, constant)
		}
	}
}
