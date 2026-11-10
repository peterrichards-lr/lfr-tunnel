package config

import (
	"fmt"
	"log/slog"
	"strings"
)

// AdminAlertMethod is the one route admin alerts take (#2435).
//
// Email and the Slack/Teams webhook used to be independent, so an operator whose email already
// forwarded into a Slack channel and who then configured the webhook got every alert twice. The
// method is a choice of ONE, not a set of toggles: the owner asked for a switch between two
// integrations that each carry every alert, not a second copy.
type AdminAlertMethod string

const (
	// AdminAlertMethodEmail sends to admin_notification_email. The default, so a gateway that
	// says nothing keeps doing what it did before the switch existed.
	AdminAlertMethodEmail AdminAlertMethod = "email"
	// AdminAlertMethodWebhook posts to webhooks.slack_url and/or webhooks.teams_url.
	AdminAlertMethodWebhook AdminAlertMethod = "webhook"
)

// AdminAlertMethods is the vocabulary, in the order the portals offer it.
var AdminAlertMethods = []AdminAlertMethod{AdminAlertMethodEmail, AdminAlertMethodWebhook}

// ParseAdminAlertMethod accepts a known method, case-insensitively. Empty is not a method; the
// caller decides what an unset value means.
func ParseAdminAlertMethod(s string) (AdminAlertMethod, bool) {
	m := AdminAlertMethod(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range AdminAlertMethods {
		if m == known {
			return m, true
		}
	}
	return "", false
}

// AdminAlertsConfig is the `admin_alerts` block. The owner can override Method from System
// Settings; this is the value that applies when they have not.
type AdminAlertsConfig struct {
	Method AdminAlertMethod `yaml:"method"`
}

// HasURL reports whether the webhook route has anywhere to post.
func (w WebhookConfig) HasURL() bool {
	return strings.TrimSpace(w.SlackURL) != "" || strings.TrimSpace(w.TeamsURL) != ""
}

// validateAdminAlerts normalises the method and refuses one the gateway cannot honour.
//
// Refused at load rather than degraded at send time: a gateway told to post alerts to a webhook
// it has no address for would otherwise start, look healthy, and deliver nothing -- and an alert
// that never arrives is indistinguishable from nothing having happened.
func validateAdminAlerts(cfg *ServerConfig) error {
	raw := string(cfg.AdminAlerts.Method)
	if strings.TrimSpace(raw) == "" {
		cfg.AdminAlerts.Method = AdminAlertMethodEmail
	} else {
		m, ok := ParseAdminAlertMethod(raw)
		if !ok {
			return fmt.Errorf("admin_alerts.method is %q; it must be one of: email, webhook", raw)
		}
		cfg.AdminAlerts.Method = m
	}
	if cfg.AdminAlerts.Method == AdminAlertMethodWebhook && !cfg.Webhooks.HasURL() {
		return fmt.Errorf("admin_alerts.method is \"webhook\" but neither webhooks.slack_url nor webhooks.teams_url is set")
	}

	// webhooks.enabled used to send every alert by webhook IN ADDITION to email. The method
	// replaces it; say so rather than let an operator believe the old switch still does
	// something.
	if cfg.Webhooks.Enabled && cfg.AdminAlerts.Method == AdminAlertMethodEmail {
		slog.Warn("[Config] webhooks.enabled no longer sends alerts on its own -- admin alerts go by email. " +
			"Set admin_alerts.method: webhook (or choose it in System Settings) to send them to the webhook instead.")
	}
	return nil
}
