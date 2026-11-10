package config

import (
	"strings"
	"testing"
)

// TestSlackAlertMethodLoads covers admin_alerts.slack (#2435): unset means off, so a gateway that
// predates the switch keeps sending alerts exactly where it always did, and every method loads.
func TestSlackAlertMethodLoads(t *testing.T) {
	cases := []struct {
		name string
		body string
		want SlackAlertMethod
	}{
		{"unset defaults to off", "owner:\n  user_id: owner@example.com\n", SlackAlertMethodOff},
		{"off", "admin_alerts:\n  slack:\n    method: off\n", SlackAlertMethodOff},
		{"channel email", "admin_alerts:\n  slack:\n    method: email\n    channel_email: chan-x@example.slack.com\n", SlackAlertMethodEmail},
		{"webhook with a slack url", "admin_alerts:\n  slack:\n    method: webhook\nwebhooks:\n  slack_url: https://hooks.slack.com/services/x\n", SlackAlertMethodWebhook},
		{"webhook with only a teams url", "admin_alerts:\n  slack:\n    method: webhook\nwebhooks:\n  teams_url: https://example.webhook.office.com/x\n", SlackAlertMethodWebhook},
		{"case-insensitive", "admin_alerts:\n  slack:\n    method: Webhook\nwebhooks:\n  slack_url: https://hooks.slack.com/services/x\n", SlackAlertMethodWebhook},
		{"name-formatted channel address", "admin_alerts:\n  slack:\n    method: email\n    channel_email: '\"lfr-tunnel-admin (Slack)\" <chan-x@example.slack.com>'\n", SlackAlertMethodEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeServerConfig(t, tc.body))
			if err != nil {
				t.Fatalf("LoadServerConfig failed: %v", err)
			}
			if cfg.AdminAlerts.Slack.Method != tc.want {
				t.Errorf("method = %q, want %q", cfg.AdminAlerts.Slack.Method, tc.want)
			}
		})
	}
}

// TestSlackAlertMethodRefused: a method the gateway cannot honour fails the load rather than
// starting a gateway that delivers nothing to the channel.
func TestSlackAlertMethodRefused(t *testing.T) {
	cases := []struct {
		name, body, wantInErr string
	}{
		{"unknown method", "admin_alerts:\n  slack:\n    method: pigeon\n", "must be one of"},
		{"email with no address", "admin_alerts:\n  slack:\n    method: email\n", "channel_email is not set"},
		{"email with a bad address", "admin_alerts:\n  slack:\n    method: email\n    channel_email: not-an-address\n", "is not an email address"},
		{"webhook with no url", "admin_alerts:\n  slack:\n    method: webhook\n", "neither webhooks.slack_url nor webhooks.teams_url"},
		{"webhook with a blank url", "admin_alerts:\n  slack:\n    method: webhook\nwebhooks:\n  slack_url: \"  \"\n", "neither webhooks.slack_url nor webhooks.teams_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeServerConfig(t, tc.body))
			if err == nil {
				t.Fatal("LoadServerConfig accepted a Slack method it cannot honour")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantInErr)
			}
		})
	}
}
