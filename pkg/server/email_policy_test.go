package server

import (
	"testing"

	"lfr-tunnel/pkg/db"
)

// TestShouldSendTo pins the policy #1135 was filed to make explicit: a recipient can mute
// notifications, and cannot mute transactional mail.
func TestShouldSendTo(t *testing.T) {
	muted := &db.User{Email: "admin@example.com", NotificationPrefs: "disabled"}
	subscribed := &db.User{Email: "admin@example.com", NotificationPrefs: "enabled"}
	// Users predate the preference column, so the zero value has to read as subscribed.
	unset := &db.User{Email: "admin@example.com"}

	cases := []struct {
		name  string
		user  *db.User
		class emailClass
		want  bool
	}{
		{"muted user does not get notifications", muted, emailNotification, false},
		{"muted user still gets transactional mail", muted, emailTransactional, true},
		{"subscribed user gets notifications", subscribed, emailNotification, true},
		{"subscribed user gets transactional mail", subscribed, emailTransactional, true},
		{"unset preference defaults to subscribed", unset, emailNotification, true},
		// The configured admin address need not correspond to a registered account.
		{"unknown recipient gets notifications", nil, emailNotification, true},
		{"unknown recipient gets transactional mail", nil, emailTransactional, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSendTo(tc.user, tc.class); got != tc.want {
				t.Errorf("shouldSendTo() = %v, want %v", got, tc.want)
			}
		})
	}
}
