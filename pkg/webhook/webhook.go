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

	keyType = "type"
	keyText = "text"

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

// sendPayload and sendPayloadSync post to whichever destinations are configured. Whether alerts
// go to the webhook at all is the server's decision (admin_alerts.method, #2435), made before
// anything is enqueued; this service no longer second-guesses it with webhooks.enabled.
func (w *WebhookService) sendPayload(slackData map[string]interface{}, teamsData map[string]interface{}) {
	go func() {
		_ = w.sendPayloadSync(slackData, teamsData) //nolint:errcheck
	}()
}

func (w *WebhookService) sendPayloadSync(slackData map[string]interface{}, teamsData map[string]interface{}) error {
	if !w.cfg.HasURL() {
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
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("webhook endpoint returned failure status", "platform", platform, "status", resp.Status)
		return fmt.Errorf("webhook endpoint %s returned status %s", platform, resp.Status)
	}
	return nil
}

// Slack's Block Kit limits: a header's plain_text and a section's text. A payload over either is
// rejected with a 400, and because the queue only deletes what was delivered, one oversized
// digest would be retried -- and refused -- on every tick, holding up every alert behind it.
const (
	slackHeaderMax  = 150
	slackSectionMax = 3000
)

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	const marker = "\n…(truncated -- see the admin portal for the rest)"
	keep := max - len([]rune(marker))
	if keep < 0 {
		keep = 0
	}
	return string(r[:keep]) + marker
}

func (w *WebhookService) buildPayloads(title, description, themeColor string, facts []map[string]string) (map[string]interface{}, map[string]interface{}) {
	slackData := map[string]interface{}{
		// Top-level text is what a notification preview and a screen reader show; without it
		// Slack shows "This content can't be displayed".
		"text": title,
		"blocks": []interface{}{
			map[string]interface{}{
				keyType: slackTypeHeader,
				keyText: map[string]string{
					keyType: slackTypePlainText,
					keyText: truncateRunes(title, slackHeaderMax),
				},
			},
			map[string]interface{}{
				keyType: slackTypeSection,
				keyText: map[string]string{
					keyType: slackTypeMrkdwn,
					keyText: truncateRunes(description, slackSectionMax),
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

// SendAlert queues one admin alert. title is the alert's subject; text is Slack mrkdwn.
//
// One entry point for every alert kind, replacing six hand-built ones (#2435). Those covered five
// of the ten admin alerts, so a webhook could never be a full alternative to email: extension
// requests, quota events, watchdog restarts and stalled edge metrics reached email only. The
// server now hands every alert to this, so coverage follows from the dispatch rather than from
// someone remembering to write a seventh.
func (w *WebhookService) SendAlert(title, text string) {
	w.enqueueOrSend(title, text, colorDefault, nil)
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
	if !w.cfg.HasURL() {
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
			// Bold, not a "####" heading: Slack mrkdwn has no headings and printed the hashes.
			_, _ = fmt.Fprintf(&sb, "*%d. %s*\n%s\n\n", i+1, msg.Title, msg.Description)
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
