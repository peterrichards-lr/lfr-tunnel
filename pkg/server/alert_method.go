package server

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"lfr-tunnel/pkg/config"
)

// How admin alerts reach Slack (#2435).
//
// Every admin alert goes to admin_notification_email as it always has. It can ALSO go to a Slack
// channel, by one of two integrations: email to the channel's own address, or the incoming
// webhook. They are either/or -- with both on, the channel gets every alert twice -- so this is
// a single choice of off / email / webhook rather than two toggles.
//
// admin_alerts.slack.method in server-config.yaml is the default; the owner can override it from
// System Settings, and clearing that returns to the YAML. This mirrors the geo-IP vendor
// (geo_provider_settings.go), the existing setting with the same two sources, so an operator who
// has learned one reads the other the same way.

// slackAlertMethodSettingKey is the admin_settings row holding the owner's override.
const slackAlertMethodSettingKey = "admin_alert_slack_method"

// alertMethodSource names which configuration supplied the method in force.
type alertMethodSource string

const (
	alertMethodSourcePortal       alertMethodSource = "portal"
	alertMethodSourceServerConfig alertMethodSource = "server_config"
)

// configuredSlackMethod is the YAML's choice. LoadServerConfig has already refused one it cannot
// deliver, so this is always usable.
func (s *Server) configuredSlackMethod() config.SlackAlertMethod {
	if s.cfg != nil && s.cfg.AdminAlerts.Slack.Method != "" {
		return s.cfg.AdminAlerts.Slack.Method
	}
	return config.SlackAlertMethodOff
}

// storedSlackMethod is the owner's override as written, or "" when there is none.
func (s *Server) storedSlackMethod() (config.SlackAlertMethod, error) {
	if s.db == nil {
		return "", nil
	}
	stored, err := s.db.GetAdminSetting(slackAlertMethodSettingKey)
	if err != nil {
		return "", err
	}
	m, _ := config.ParseSlackAlertMethod(stored)
	return m, nil
}

// effectiveSlackMethod resolves how alerts reach Slack right now.
//
// An unreadable override row falls back to the YAML value, as the geo vendor does: that is the
// operator's own configured choice, not a guess. So does an override whose destination has since
// been removed from the YAML -- a webhook URL or channel address deleted after the owner chose it
// -- rather than queueing alerts that can never be delivered.
func (s *Server) effectiveSlackMethod() (config.SlackAlertMethod, alertMethodSource) {
	configured := s.configuredSlackMethod()
	stored, err := s.storedSlackMethod()
	if err != nil {
		slog.Warn(fmt.Sprintf("[Alerts] Could not read %s, using admin_alerts.slack.method from the config (%s): %v",
			slackAlertMethodSettingKey, configured, err))
		return configured, alertMethodSourceServerConfig
	}
	if stored == "" {
		return configured, alertMethodSourceServerConfig
	}
	if s.cfg == nil || !s.cfg.SlackMethodAvailable(stored) {
		slog.Warn(fmt.Sprintf("[Alerts] Slack method %q was chosen in System Settings but is no longer configured; "+
			"using admin_alerts.slack.method from the config (%s)", stored, configured))
		return configured, alertMethodSourceServerConfig
	}
	return stored, alertMethodSourcePortal
}

// alertEnabledForSlack applies the alert's Slack toggle (#2445). It is separate from the email
// toggle so the owner can take an alert in Slack and keep it out of the inbox, or the reverse.
//
// The admin's personal notification preference does not apply: that is a choice about one
// person's inbox, and a channel post is not an email to them. A read failure sends anyway --
// there is no inbox owner whose preference that could override, and an alert held back by a read
// error is the silent loss this route must not introduce.
func (s *Server) alertEnabledForSlack(settingKey string) bool {
	key := slackAlertKey(settingKey)
	if s.db == nil {
		return true
	}
	stored, err := s.db.GetAdminSetting(key)
	if err != nil {
		slog.Warn(fmt.Sprintf("[Alerts] Could not read %s; sending the Slack alert regardless: %v", key, err))
		return true
	}
	return alertSettingEnabled(key, stored)
}

// sendAdminAlertToSlack is the Slack half of sendAdminAlert: one route or none.
func (s *Server) sendAdminAlertToSlack(settingKey, subject, body string) {
	method, _ := s.effectiveSlackMethod()
	if method == config.SlackAlertMethodOff || !s.alertEnabledForSlack(settingKey) {
		return
	}
	switch method {
	case config.SlackAlertMethodWebhook:
		if s.webhooks != nil {
			s.webhooks.SendAlert(subject, htmlToSlack(body))
		}
	case config.SlackAlertMethodEmail:
		if s.notifications == nil {
			slog.Warn("[Alerts] Slack alerts go by channel email but no mail sender is configured; alert not sent to Slack")
			return
		}
		s.sendNotificationAsync(notifyAdminAlert, s.cfg.AdminAlerts.Slack.ChannelEmail, subject, body, "An alert has been triggered.")
	}
}

// slackMethodLabel describes a method for a human, e.g. the test alert's response.
func slackMethodLabel(m config.SlackAlertMethod) string {
	switch m {
	case config.SlackAlertMethodEmail:
		return "Slack channel email"
	case config.SlackAlertMethodWebhook:
		return "Slack/Teams webhook"
	}
	return "not sent to Slack"
}

var (
	htmlLinkRe      = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	htmlBoldRe      = regexp.MustCompile(`(?is)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	htmlListItemRe  = regexp.MustCompile(`(?i)<li[^>]*>`)
	htmlLineBreakRe = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</li>|</div>|</h[1-6]>|</tr>`)
	htmlTagRe       = regexp.MustCompile(`<[^>]+>`)
	blankLinesRe    = regexp.MustCompile(`\n{3,}`)
)

// htmlToSlack renders an alert body for Slack mrkdwn.
//
// The bodies are written for email, some as plain sentences and some as small HTML fragments.
// Converted rather than rewritten per alert so that the webhook carries exactly what the email
// does -- a second, hand-maintained copy of each message is how the webhook came to cover only
// half of them.
//
// Order matters. Links are lifted out first and restored last, because Slack writes a link as
// <url|text> and the tag-stripping pass would otherwise remove it. Text is HTML-unescaped and
// then escaped again for Slack, which reserves &, < and > -- an email address or error message
// containing them would otherwise be read as markup.
func htmlToSlack(body string) string {
	var links []string
	s := htmlLinkRe.ReplaceAllStringFunc(body, func(m string) string {
		parts := htmlLinkRe.FindStringSubmatch(m)
		text := slackEscape(html.UnescapeString(htmlTagRe.ReplaceAllString(parts[2], "")))
		links = append(links, "<"+slackEscape(html.UnescapeString(parts[1]))+"|"+text+">")
		return fmt.Sprintf("\x00%d\x00", len(links)-1)
	})
	s = htmlBoldRe.ReplaceAllString(s, "\x01$1\x01")
	s = htmlListItemRe.ReplaceAllString(s, "• ")
	s = htmlLineBreakRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = slackEscape(html.UnescapeString(s))
	s = strings.ReplaceAll(s, "\x01", "*")
	for i, l := range links {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), l, 1)
	}
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// slackEscape escapes the three characters Slack's mrkdwn reserves.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// alertAbuseReport raises the alert for a "this wasn't me" report on a magic-link or
// registration email. Escaped: the address and IP arrive from a request, and the body is HTML.
func (s *Server) alertAbuseReport(email, reason, reporterIP string) {
	body := fmt.Sprintf(
		"<p>%s was reported as not the user's own action.</p><ul><li>Email: %s</li><li>Reporter IP: %s</li></ul>"+
			"<p>The request was cancelled automatically. Review it in the Audit Log.</p>",
		html.EscapeString(reason), html.EscapeString(email), html.EscapeString(reporterIP),
	)
	s.sendAdminAlert("alert_notify_abuse_report", "LFR Tunnel Alert: Abuse Report", body)
}

// addAlertMethodSettings reports the Slack alert method to System Settings.
//
// Both the method in force and where it came from, like the geo vendor: a page that shows the
// owner's choice while the YAML's is the one applying -- or the reverse -- is a false statement
// about where alerts are going. The webhook URL is a secret and never leaves the server; the page
// is told only that one exists. The channel address is shown: it is not a credential, and an
// owner checking where alerts go needs to see it.
func (s *Server) addAlertMethodSettings(out map[string]interface{}, actor string) {
	method, source := s.effectiveSlackMethod()
	out["admin_alert_slack_method"] = string(method)
	out["admin_alert_slack_method_source"] = string(source)
	out["admin_alert_slack_methods"] = config.SlackAlertMethods
	out["admin_alert_slack_method_config"] = string(s.configuredSlackMethod())
	// The stored override as written, which can differ from the method in force when its
	// destination has since been removed from the YAML. The form shows the choice;
	// admin_alert_slack_method says what is actually happening.
	// An unreadable row reports no override; effectiveSlackMethod has already fallen back to the
	// YAML for the same reason, so the in-force line and the form agree.
	stored, err := s.storedSlackMethod()
	if err != nil {
		slog.Warn(fmt.Sprintf("[Alerts] Could not read %s for System Settings: %v", slackAlertMethodSettingKey, err))
	}
	out["admin_alert_slack_method_override"] = string(stored)

	channelEmail, webhook := "", false
	if s.cfg != nil {
		channelEmail = s.cfg.AdminAlerts.Slack.ChannelEmail
		webhook = s.cfg.Webhooks.HasURL()
	}
	out["slack_channel_email"] = channelEmail
	out["slack_webhook_configured"] = webhook
	// Owner-only, because it decides where every admin's alerts are posted. Admins see the
	// setting; they cannot change it.
	out["can_change_alert_method"] = s.isOwner(actor)
}

// validateAlertMethodChange returns a non-zero HTTP status and message when the change must be
// refused. An empty value clears the override, returning to the YAML.
func (s *Server) validateAlertMethodChange(actor, value string) (int, string) {
	if !s.isOwner(actor) {
		return http.StatusForbidden, "Only the owner can change how admin alerts reach Slack"
	}
	if value == "" {
		return 0, ""
	}
	m, ok := config.ParseSlackAlertMethod(value)
	if !ok || string(m) != value {
		return http.StatusBadRequest, fmt.Sprintf("Setting %q must be \"off\", \"email\" or \"webhook\"", slackAlertMethodSettingKey)
	}
	if s.cfg == nil || !s.cfg.SlackMethodAvailable(m) {
		if m == config.SlackAlertMethodEmail {
			return http.StatusBadRequest, "No Slack channel address is configured. Add admin_alerts.slack.channel_email to server-config.yaml first"
		}
		return http.StatusBadRequest, "No webhook URL is configured. Add webhooks.slack_url or webhooks.teams_url to server-config.yaml first"
	}
	return 0, ""
}
