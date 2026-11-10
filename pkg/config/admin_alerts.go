package config

import (
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
)

// SlackAlertMethod is how admin alerts reach Slack, IN ADDITION to the admin inbox (#2435).
//
// Slack has two integrations and they are either/or: email sent to the channel's own address, or
// the incoming webhook. Running both posts every alert to the channel twice. Email to
// admin_notification_email is not part of this choice and is unaffected by it.
type SlackAlertMethod string

const (
	// SlackAlertMethodOff sends nothing to Slack. The default, so a gateway that says nothing
	// keeps doing exactly what it did before the switch existed.
	SlackAlertMethodOff SlackAlertMethod = "off"
	// SlackAlertMethodEmail emails admin_alerts.slack.channel_email, the address Slack gives a
	// channel under Integrations > Send emails to this channel.
	SlackAlertMethodEmail SlackAlertMethod = "email"
	// SlackAlertMethodWebhook posts to webhooks.slack_url (and webhooks.teams_url if set).
	SlackAlertMethodWebhook SlackAlertMethod = "webhook"
)

// SlackAlertMethods is the vocabulary, in the order the portals offer it.
var SlackAlertMethods = []SlackAlertMethod{SlackAlertMethodOff, SlackAlertMethodEmail, SlackAlertMethodWebhook}

// ParseSlackAlertMethod accepts a known method, case-insensitively. Empty is not a method; the
// caller decides what an unset value means.
func ParseSlackAlertMethod(s string) (SlackAlertMethod, bool) {
	m := SlackAlertMethod(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range SlackAlertMethods {
		if m == known {
			return m, true
		}
	}
	return "", false
}

// SlackAlertsConfig is `admin_alerts.slack`. The owner can override Method from System Settings;
// this is the value that applies when they have not.
type SlackAlertsConfig struct {
	Method       SlackAlertMethod `yaml:"method"`
	ChannelEmail string           `yaml:"channel_email"`
}

// AdminAlertsConfig is the `admin_alerts` block.
type AdminAlertsConfig struct {
	Slack SlackAlertsConfig `yaml:"slack"`
}

// HasURL reports whether the webhook route has anywhere to post.
func (w WebhookConfig) HasURL() bool {
	return strings.TrimSpace(w.SlackURL) != "" || strings.TrimSpace(w.TeamsURL) != ""
}

// SlackMethodAvailable reports whether the gateway has what a method needs to deliver. Off always
// can; the other two need their destination configured.
func (c *ServerConfig) SlackMethodAvailable(m SlackAlertMethod) bool {
	switch m {
	case SlackAlertMethodOff:
		return true
	case SlackAlertMethodEmail:
		return strings.TrimSpace(c.AdminAlerts.Slack.ChannelEmail) != ""
	case SlackAlertMethodWebhook:
		return c.Webhooks.HasURL()
	}
	return false
}

// validateAdminAlerts normalises the Slack method and refuses one the gateway cannot honour.
//
// Refused at load rather than degraded at send time: a gateway told to send alerts to Slack with
// no address for it would otherwise start, look healthy, and deliver nothing to the channel --
// and an alert that never arrives is indistinguishable from nothing having happened.
func validateAdminAlerts(cfg *ServerConfig) error {
	slack := &cfg.AdminAlerts.Slack
	if addr := strings.TrimSpace(slack.ChannelEmail); addr != "" {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("admin_alerts.slack.channel_email %q is not an email address: %v", addr, err)
		}
		slack.ChannelEmail = addr
	}

	raw := string(slack.Method)
	if strings.TrimSpace(raw) == "" {
		slack.Method = SlackAlertMethodOff
	} else {
		m, ok := ParseSlackAlertMethod(raw)
		if !ok {
			return fmt.Errorf("admin_alerts.slack.method is %q; it must be one of: off, email, webhook", raw)
		}
		slack.Method = m
	}
	if !cfg.SlackMethodAvailable(slack.Method) {
		switch slack.Method {
		case SlackAlertMethodEmail:
			return fmt.Errorf("admin_alerts.slack.method is \"email\" but admin_alerts.slack.channel_email is not set")
		case SlackAlertMethodWebhook:
			return fmt.Errorf("admin_alerts.slack.method is \"webhook\" but neither webhooks.slack_url nor webhooks.teams_url is set")
		}
	}

	// webhooks.enabled used to send every alert to the webhook alongside email. The Slack
	// method replaces it; say so rather than let an operator believe the old switch still
	// does something.
	if cfg.Webhooks.Enabled && slack.Method != SlackAlertMethodWebhook {
		slog.Warn("[Config] webhooks.enabled no longer sends alerts on its own. " +
			"Set admin_alerts.slack.method: webhook (or choose it in System Settings) to post alerts to the webhook.")
	}
	return nil
}
