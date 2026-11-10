package config

import (
	"strings"
	"testing"
)

// TestAdminAlertMethodLoads covers admin_alerts.method (#2435): unset means email, so a gateway
// that predates the switch keeps sending alerts where it always did, and both methods load.
func TestAdminAlertMethodLoads(t *testing.T) {
	cases := []struct {
		name string
		body string
		want AdminAlertMethod
	}{
		{"unset defaults to email", "owner:\n  user_id: owner@example.com\n", AdminAlertMethodEmail},
		{"email", "admin_alerts:\n  method: email\n", AdminAlertMethodEmail},
		{"webhook with a slack url", "admin_alerts:\n  method: webhook\nwebhooks:\n  slack_url: https://hooks.slack.com/services/x\n", AdminAlertMethodWebhook},
		{"webhook with only a teams url", "admin_alerts:\n  method: webhook\nwebhooks:\n  teams_url: https://example.webhook.office.com/x\n", AdminAlertMethodWebhook},
		{"case-insensitive", "admin_alerts:\n  method: Webhook\nwebhooks:\n  slack_url: https://hooks.slack.com/services/x\n", AdminAlertMethodWebhook},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeServerConfig(t, tc.body))
			if err != nil {
				t.Fatalf("LoadServerConfig failed: %v", err)
			}
			if cfg.AdminAlerts.Method != tc.want {
				t.Errorf("method = %q, want %q", cfg.AdminAlerts.Method, tc.want)
			}
		})
	}
}

// TestAdminAlertMethodRefused: a method the gateway cannot honour fails the load rather than
// starting a gateway that delivers alerts nowhere.
func TestAdminAlertMethodRefused(t *testing.T) {
	cases := []struct {
		name, body, wantInErr string
	}{
		{"unknown method", "admin_alerts:\n  method: pigeon\n", "must be one of"},
		{"webhook with no url", "admin_alerts:\n  method: webhook\n", "neither webhooks.slack_url nor webhooks.teams_url"},
		{"webhook with a blank url", "admin_alerts:\n  method: webhook\nwebhooks:\n  slack_url: \"  \"\n", "neither webhooks.slack_url nor webhooks.teams_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeServerConfig(t, tc.body))
			if err == nil {
				t.Fatal("LoadServerConfig accepted a method it cannot honour")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantInErr)
			}
		})
	}
}
