package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
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
	db     db.WebhookQueueRepository
}

func NewWebhookService(cfg config.WebhookConfig, database db.WebhookQueueRepository) *WebhookService {
	return &WebhookService{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		db:     database,
	}
}

func (w *WebhookService) sendPayload(slackData map[string]interface{}, teamsData map[string]interface{}) {
	if !w.cfg.Enabled {
		return
	}

	go func() {
		if w.cfg.SlackURL != "" && slackData != nil {
			_ = w.postToURL(w.cfg.SlackURL, slackData, "Slack") //nolint:errcheck
		}
		if w.cfg.TeamsURL != "" && teamsData != nil {
			_ = w.postToURL(w.cfg.TeamsURL, teamsData, "Microsoft Teams") //nolint:errcheck
		}
	}()
}

func (w *WebhookService) sendPayloadSync(slackData map[string]interface{}, teamsData map[string]interface{}) error {
	if !w.cfg.Enabled {
		return nil
	}

	var firstErr error
	if w.cfg.SlackURL != "" && slackData != nil {
		if err := w.postToURL(w.cfg.SlackURL, slackData, "Slack"); err != nil {
			firstErr = err
		}
	}
	if w.cfg.TeamsURL != "" && teamsData != nil {
		if err := w.postToURL(w.cfg.TeamsURL, teamsData, "Microsoft Teams"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *WebhookService) postToURL(urlStr string, data interface{}, platform string) error {
	bodyBytes, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal webhook payload", "platform", platform, "error", err)
		return err
	}

	req, err := http.NewRequest("POST", urlStr, bytes.NewBuffer(bodyBytes))
	if err != nil {
		slog.Error("failed to create webhook request", "platform", platform, "error", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		slog.Error("failed to send webhook request", "platform", platform, "error", err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("webhook endpoint returned failure status", "platform", platform, "status", resp.Status)
		return fmt.Errorf("webhook endpoint %s returned status %s", platform, resp.Status)
	}
	return nil
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

func (w *WebhookService) enqueueOrSend(title, desc, color string, facts []map[string]string) {
	if w.db != nil {
		factsJSON, _ := json.Marshal(facts)
		if err := w.db.EnqueueWebhookMessage(title, desc, color, string(factsJSON)); err != nil {
			slog.Error(fmt.Sprintf("[Webhook] Failed to enqueue alert %q: %v", title, err))
		}
		return
	}
	// Fallback to immediate send if DB is not configured (e.g. unit tests or stateless edge nodes)
	slack, teams := w.buildPayloads(title, desc, color, facts)
	w.sendPayload(slack, teams)
}

func (w *WebhookService) SendRegistrationAlert(email, subdomain string) {
	title := "📬 New User Registration Request"
	desc := fmt.Sprintf("*Email:* `%s`\n*Requested Subdomain:* `%s`\n\n_Please review and approve or reject this request inside the admin dashboard._", email, subdomain)
	facts := []map[string]string{
		{keyName: "Email", keyValue: email},
		{keyName: "Requested Subdomain", keyValue: subdomain},
	}
	w.enqueueOrSend(title, desc, colorDefault, facts)
}

func (w *WebhookService) SendAbuseReportAlert(subdomain, reason, reporterIP string) {
	title := "🚨 Tunnel Abuse Report Submitted"
	desc := fmt.Sprintf("*Subdomain:* `%s`\n*Reason:* %s\n*Reporter IP:* `%s`\n\n_Immediate admin review recommended._", subdomain, reason, reporterIP)
	facts := []map[string]string{
		{keyName: "Subdomain", keyValue: subdomain},
		{keyName: keyReason, keyValue: reason},
		{keyName: "Reporter IP", keyValue: reporterIP},
	}
	w.enqueueOrSend(title, desc, colorDanger, facts)
}

func (w *WebhookService) SendRateLimitBanAlert(ip string, duration time.Duration, reason string) {
	title := "🛡️ EDR: IP Automatically Rate Limit Banned"
	desc := fmt.Sprintf("*IP Address:* `%s`\n*Duration:* `%v`\n*Reason:* %s", ip, duration, reason)
	facts := []map[string]string{
		{keyName: "IP Address", keyValue: ip},
		{keyName: "Duration", keyValue: duration.String()},
		{keyName: keyReason, keyValue: reason},
	}
	w.enqueueOrSend(title, desc, colorWarning, facts)
}

func (w *WebhookService) SendIPBlacklistAlert(ip string, reason string) {
	title := "🚫 Admin Action: IP Blacklisted"
	desc := fmt.Sprintf("*IP Address:* `%s`\n*Reason:* %s", ip, reason)
	facts := []map[string]string{
		{keyName: "IP Address", keyValue: ip},
		{keyName: keyReason, keyValue: reason},
	}
	w.enqueueOrSend(title, desc, colorDanger, facts)
}

func (w *WebhookService) SendTestAlert(actor, timestamp, version string) {
	title := "🧪 Liferay Tunnel Integration Test"
	desc := fmt.Sprintf("This is a test notification dispatched from the Liferay Tunnel gateway.\n\n*Triggered By:* `%s`\n*Timestamp:* `%s`\n*Server Version:* `%s` ", actor, timestamp, version)
	facts := []map[string]string{
		{keyName: "Triggered By", keyValue: actor},
		{keyName: "Timestamp", keyValue: timestamp},
		{keyName: "Server Version", keyValue: version},
	}
	w.enqueueOrSend(title, desc, colorDefault, facts)
}

func (w *WebhookService) StartQueueConsumer(ctx context.Context, interval time.Duration) {
	if w.db == nil {
		slog.Warn("[Webhook] No database configured for persistent webhook queue. Consumer will not start.")
		return
	}
	slog.Info(fmt.Sprintf("[Webhook] Starting webhook queue consumer (polling interval: %v)", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("[Webhook] Stopping queue consumer worker.")
			return
		case <-ticker.C:
			w.processQueueBatch()
		}
	}
}

func (w *WebhookService) processQueueBatch() {
	if !w.cfg.Enabled {
		return
	}

	msgs, err := w.db.DequeueWebhookMessages(50)
	if err != nil {
		slog.Error(fmt.Sprintf("[Webhook] Failed to dequeue messages: %v", err))
		return
	}
	if len(msgs) == 0 {
		return
	}

	slog.Info(fmt.Sprintf("[Webhook] Processing batch of %d queued messages", len(msgs)))

	var slack, teams map[string]interface{}
	if len(msgs) == 1 {
		// Single message: send using original properties
		var facts []map[string]string
		if err := json.Unmarshal([]byte(msgs[0].Facts), &facts); err != nil {
			facts = []map[string]string{}
		}
		slack, teams = w.buildPayloads(msgs[0].Title, msgs[0].Description, msgs[0].Color, facts)
	} else {
		// Coalesce / group multiple events into a single Digest post
		title := "🔔 Liferay Tunnel: Grouped Activity Digest"
		color := colorDefault
		for _, msg := range msgs {
			if msg.Color == colorDanger {
				color = colorDanger
			} else if msg.Color == colorWarning && color != colorDanger {
				color = colorWarning
			}
		}

		var sb strings.Builder
		_, _ = fmt.Fprintf(&sb, "This is an aggregated activity digest containing %d events:\n\n", len(msgs))
		for i, msg := range msgs {
			_, _ = fmt.Fprintf(&sb, "#### %d. %s\n%s\n\n", i+1, msg.Title, msg.Description)
		}

		slack, teams = w.buildPayloads(title, sb.String(), color, nil)
	}

	// Post the payload synchronously (retrying on tick if it fails)
	if err := w.sendPayloadSync(slack, teams); err != nil {
		slog.Error(fmt.Sprintf("[Webhook] Failed to deliver coalesced webhook payload: %v. Message batch will be retried in the next tick.", err))
		return
	}

	// Delete from database only after successful delivery
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	if err := w.db.DeleteWebhookMessages(ids); err != nil {
		slog.Error(fmt.Sprintf("[Webhook] Failed to delete successfully processed messages from queue: %v", err))
	}
}
