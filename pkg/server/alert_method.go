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

// Which route admin alerts take: email or the Slack/Teams webhook, never both (#2435).
//
// admin_alerts.method in server-config.yaml is the default; an owner can override it from System
// Settings, and clearing that override returns to the YAML. This mirrors the geo-IP vendor
// (geo_provider_settings.go), the existing setting with the same two sources, so an operator who
// has learned one reads the other the same way.

// adminAlertMethodSettingKey is the admin_settings row holding the owner's override.
const adminAlertMethodSettingKey = "admin_alert_method"

// alertMethodSource names which configuration supplied the method in force.
type alertMethodSource string

const (
	alertMethodSourcePortal       alertMethodSource = "portal"
	alertMethodSourceServerConfig alertMethodSource = "server_config"
)

// effectiveAlertMethod resolves the method alerts are sent with right now.
//
// Never answers "nowhere". An unreadable override row falls back to the YAML value, as the geo
// vendor does: that is the operator's own configured choice, not a guess. A webhook selected in
// the portal when the YAML no longer carries a URL -- removed after the owner chose it -- falls
// back to email rather than queueing alerts that cannot be delivered, because a dropped alert is
// indistinguishable from nothing having happened.
func (s *Server) effectiveAlertMethod() (config.AdminAlertMethod, alertMethodSource) {
	configured := config.AdminAlertMethodEmail
	if s.cfg != nil && s.cfg.AdminAlerts.Method != "" {
		configured = s.cfg.AdminAlerts.Method
	}
	method, source := configured, alertMethodSourceServerConfig

	if s.db != nil {
		stored, err := s.db.GetAdminSetting(adminAlertMethodSettingKey)
		if err != nil {
			slog.Warn(fmt.Sprintf("[Alerts] Could not read %s, using admin_alerts.method from the config (%s): %v",
				adminAlertMethodSettingKey, configured, err))
		} else if m, ok := config.ParseAdminAlertMethod(stored); ok {
			method, source = m, alertMethodSourcePortal
		}
	}

	if method == config.AdminAlertMethodWebhook && (s.cfg == nil || !s.cfg.Webhooks.HasURL()) {
		slog.Warn("[Alerts] Alert method is webhook but no webhook URL is configured; sending by email instead")
		return config.AdminAlertMethodEmail, source
	}
	return method, source
}

// sendAdminAlertWebhook is the webhook half of sendAdminAlert.
//
// The per-alert toggles gate this route exactly as they gate email, so switching method never
// changes WHICH alerts arrive. The email recipient's personal notification preference does not:
// that is a choice about one person's inbox, and a channel post is not an email to them.
func (s *Server) sendAdminAlertWebhook(settingKey, subject, body string) {
	if s.webhooks == nil {
		return
	}
	if s.db != nil {
		stored, err := s.db.GetAdminSetting(settingKey)
		if err != nil {
			// Sent anyway. Unlike email, there is no inbox owner whose preference we might be
			// overriding, and an alert held back by a read failure is the silent loss this
			// switch must not introduce.
			slog.Warn(fmt.Sprintf("[Alerts] Could not read %s; sending the alert regardless: %v", settingKey, err))
		} else if !alertSettingEnabled(settingKey, stored) {
			return
		}
	}
	s.webhooks.SendAlert(subject, htmlToSlack(body))
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

// addAlertMethodSettings reports the alert method to System Settings.
//
// Both the method in force and where it came from, like the geo vendor: a page that shows the
// owner's choice while the YAML's is the one applying -- or the reverse -- is a false statement
// about where alerts are going. The webhook URLs themselves are secrets and never leave the
// server; the page is told only which destinations exist.
func (s *Server) addAlertMethodSettings(out map[string]interface{}, actor string) {
	method, source := s.effectiveAlertMethod()
	out["admin_alert_method"] = string(method)
	out["admin_alert_method_source"] = string(source)
	out["admin_alert_methods"] = config.AdminAlertMethods
	configured, slack, teams := config.AdminAlertMethodEmail, false, false
	if s.cfg != nil {
		if s.cfg.AdminAlerts.Method != "" {
			configured = s.cfg.AdminAlerts.Method
		}
		slack = strings.TrimSpace(s.cfg.Webhooks.SlackURL) != ""
		teams = strings.TrimSpace(s.cfg.Webhooks.TeamsURL) != ""
	}
	out["admin_alert_method_config"] = string(configured)
	// The stored override as written, which can differ from the method in force: a webhook
	// chosen here falls back to email when the YAML loses its URL. The form shows the choice;
	// admin_alert_method says what is actually happening.
	override := ""
	if s.db != nil {
		if stored, err := s.db.GetAdminSetting(adminAlertMethodSettingKey); err == nil {
			if m, ok := config.ParseAdminAlertMethod(stored); ok {
				override = string(m)
			}
		}
	}
	out["admin_alert_method_override"] = override
	out["webhook_slack_configured"] = slack
	out["webhook_teams_configured"] = teams
	// Owner-only, because it decides where every admin's alerts go -- including whether they
	// reach an inbox at all. Admins see the setting; they cannot change it.
	out["can_change_alert_method"] = s.isOwner(actor)
}

// validateAlertMethodChange returns a non-zero HTTP status and message when the change must be
// refused. An empty value clears the override, returning to the YAML.
func (s *Server) validateAlertMethodChange(actor, value string) (int, string) {
	if !s.isOwner(actor) {
		return http.StatusForbidden, "Only the owner can change how admin alerts are delivered"
	}
	if value == "" {
		return 0, ""
	}
	m, ok := config.ParseAdminAlertMethod(value)
	if !ok || string(m) != value {
		return http.StatusBadRequest, fmt.Sprintf("Setting %q must be \"email\" or \"webhook\"", adminAlertMethodSettingKey)
	}
	if m == config.AdminAlertMethodWebhook && (s.cfg == nil || !s.cfg.Webhooks.HasURL()) {
		return http.StatusBadRequest, "No webhook URL is configured. Add webhooks.slack_url or webhooks.teams_url to server-config.yaml first"
	}
	return 0, ""
}
