package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"lfr-tunnel/pkg/config"
)

const (
	slackTypeHeader    = "header"
	slackTypeSection   = "section"
	slackTypeMrkdwn    = "mrkdwn"
	slackTypePlainText = "plain_text"

	keyType   = "type"
	keyText   = "text"
	keyName   = "name"
	keyReason = "Reason"
	keyValue  = "value"

	teamsTypeMessageCard = "MessageCard"
	teamsContext         = "http://schema.org/extensions"

	colorDefault = "0076D7"
	colorWarning = "F7630C"
	colorDanger  = "E81123"
)

type WebhookService struct {
	cfg    config.WebhookConfig
	client *http.Client
}

func NewWebhookService(cfg config.WebhookConfig) *WebhookService {
	return &WebhookService{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *WebhookService) sendPayload(slackData map[string]interface{}, teamsData map[string]interface{}) {
	if !w.cfg.Enabled {
		return
	}

	go func() {
		if w.cfg.SlackURL != "" && slackData != nil {
			w.postToURL(w.cfg.SlackURL, slackData, "Slack")
		}
		if w.cfg.TeamsURL != "" && teamsData != nil {
			w.postToURL(w.cfg.TeamsURL, teamsData, "Microsoft Teams")
		}
	}()
}

func (w *WebhookService) postToURL(urlStr string, data interface{}, platform string) {
	bodyBytes, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal webhook payload", "platform", platform, "error", err)
		return
	}

	req, err := http.NewRequest("POST", urlStr, bytes.NewBuffer(bodyBytes))
	if err != nil {
		slog.Error("failed to create webhook request", "platform", platform, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		slog.Error("failed to send webhook request", "platform", platform, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("webhook endpoint returned failure status", "platform", platform, "status", resp.Status)
	}
}

func (w *WebhookService) buildPayloads(title, description, themeColor string, facts []map[string]string) (map[string]interface{}, map[string]interface{}) {
	slackData := map[string]interface{}{
		"blocks": []interface{}{
			map[string]interface{}{
				keyType: slackTypeHeader,
				keyText: map[string]string{
					keyType: slackTypePlainText,
					keyText: title,
				},
			},
			map[string]interface{}{
				keyType: slackTypeSection,
				keyText: map[string]string{
					keyType: slackTypeMrkdwn,
					keyText: description,
				},
			},
		},
	}

	teamsSection := map[string]interface{}{
		"activityTitle": title,
		"markdown":      true,
	}
	if len(facts) > 0 {
		teamsSection["facts"] = facts
	}

	teamsData := map[string]interface{}{
		"@type":      teamsTypeMessageCard,
		"@context":   teamsContext,
		"themeColor": themeColor,
		"summary":    title,
		"sections": []interface{}{
			teamsSection,
		},
	}

	return slackData, teamsData
}

func (w *WebhookService) SendRegistrationAlert(email, subdomain string) {
	title := "📬 New User Registration Request"
	desc := fmt.Sprintf("*Email:* `%s`\n*Requested Subdomain:* `%s`\n\n_Please review and approve or reject this request inside the admin dashboard._", email, subdomain)
	facts := []map[string]string{
		{keyName: "Email", keyValue: email},
		{keyName: "Requested Subdomain", keyValue: subdomain},
	}
	slack, teams := w.buildPayloads(title, desc, colorDefault, facts)
	w.sendPayload(slack, teams)
}

func (w *WebhookService) SendAbuseReportAlert(subdomain, reason, reporterIP string) {
	title := "🚨 Tunnel Abuse Report Submitted"
	desc := fmt.Sprintf("*Subdomain:* `%s`\n*Reason:* %s\n*Reporter IP:* `%s`\n\n_Immediate admin review recommended._", subdomain, reason, reporterIP)
	facts := []map[string]string{
		{keyName: "Subdomain", keyValue: subdomain},
		{keyName: keyReason, keyValue: reason},
		{keyName: "Reporter IP", keyValue: reporterIP},
	}
	slack, teams := w.buildPayloads(title, desc, colorDanger, facts)
	w.sendPayload(slack, teams)
}

func (w *WebhookService) SendRateLimitBanAlert(ip string, duration time.Duration, reason string) {
	title := "🛡️ EDR: IP Automatically Rate Limit Banned"
	desc := fmt.Sprintf("*IP Address:* `%s`\n*Duration:* `%v`\n*Reason:* %s", ip, duration, reason)
	facts := []map[string]string{
		{keyName: "IP Address", keyValue: ip},
		{keyName: "Duration", keyValue: duration.String()},
		{keyName: keyReason, keyValue: reason},
	}
	slack, teams := w.buildPayloads(title, desc, colorWarning, facts)
	w.sendPayload(slack, teams)
}

func (w *WebhookService) SendIPBlacklistAlert(ip string, reason string) {
	title := "🚫 Admin Action: IP Blacklisted"
	desc := fmt.Sprintf("*IP Address:* `%s`\n*Reason:* %s", ip, reason)
	facts := []map[string]string{
		{keyName: "IP Address", keyValue: ip},
		{keyName: keyReason, keyValue: reason},
	}
	slack, teams := w.buildPayloads(title, desc, colorDanger, facts)
	w.sendPayload(slack, teams)
}

func (w *WebhookService) SendTestAlert(actor, timestamp, version string) {
	title := "🧪 Liferay Tunnel Integration Test"
	desc := fmt.Sprintf("This is a test notification dispatched from the Liferay Tunnel gateway.\n\n*Triggered By:* `%s`\n*Timestamp:* `%s`\n*Server Version:* `%s` ", actor, timestamp, version)
	facts := []map[string]string{
		{keyName: "Triggered By", keyValue: actor},
		{keyName: "Timestamp", keyValue: timestamp},
		{keyName: "Server Version", keyValue: version},
	}
	slack, teams := w.buildPayloads(title, desc, colorDefault, facts)
	w.sendPayload(slack, teams)
}
