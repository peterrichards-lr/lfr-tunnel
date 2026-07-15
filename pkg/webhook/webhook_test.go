package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestWebhookAlerts(t *testing.T) {
	var mu sync.Mutex
	receivedSlack := make([]map[string]interface{}, 0)
	receivedTeams := make([]map[string]interface{}, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "slack") {
			receivedSlack = append(receivedSlack, data)
		} else if strings.Contains(r.URL.Path, "teams") {
			receivedTeams = append(receivedTeams, data)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookConfig{
		Enabled:  true,
		SlackURL: server.URL + "/slack",
		TeamsURL: server.URL + "/teams",
	}

	service := NewWebhookService(cfg, nil)

	// Send alerts
	service.SendRegistrationAlert("test@example.com", "my-subdomain")
	service.SendAbuseReportAlert("my-subdomain", "spamming", "127.0.0.1")
	service.SendRateLimitBanAlert("127.0.0.1", 10*time.Minute, "DDOS")
	service.SendIPBlacklistAlert("127.0.0.1", "spam")
	service.SendTestAlert("admin@example.com", "2026-07-15 16:30:00 UTC", "v1.34.3")

	// Wait for async goroutines to deliver payloads
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(receivedSlack) != 5 {
		t.Errorf("expected 5 Slack alerts, got %d", len(receivedSlack))
	}
	if len(receivedTeams) != 5 {
		t.Errorf("expected 5 Teams alerts, got %d", len(receivedTeams))
	}
}

type mockWebhookQueue struct {
	mu     sync.Mutex
	queued []*db.QueuedWebhookMessage
	nextID int64
}

func (m *mockWebhookQueue) EnqueueWebhookMessage(title, description, color, factsJSON string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	m.queued = append(m.queued, &db.QueuedWebhookMessage{
		ID:          m.nextID,
		Title:       title,
		Description: description,
		Color:       color,
		Facts:       factsJSON,
		CreatedAt:   time.Now(),
	})
	return nil
}

func (m *mockWebhookQueue) DequeueWebhookMessages(limit int) ([]*db.QueuedWebhookMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queued) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(m.queued) {
		n = len(m.queued)
	}
	res := make([]*db.QueuedWebhookMessage, n)
	copy(res, m.queued[:n])
	return res, nil
}

func (m *mockWebhookQueue) DeleteWebhookMessages(ids []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var remaining []*db.QueuedWebhookMessage
	for _, q := range m.queued {
		keep := true
		for _, id := range ids {
			if q.ID == id {
				keep = false
				break
			}
		}
		if keep {
			remaining = append(remaining, q)
		}
	}
	m.queued = remaining
	return nil
}

func TestWebhookBatchConsumer(t *testing.T) {
	var mu sync.Mutex
	receivedSlack := make([]map[string]interface{}, 0)
	receivedTeams := make([]map[string]interface{}, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "slack") {
			receivedSlack = append(receivedSlack, data)
		} else if strings.Contains(r.URL.Path, "teams") {
			receivedTeams = append(receivedTeams, data)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.WebhookConfig{
		Enabled:  true,
		SlackURL: server.URL + "/slack",
		TeamsURL: server.URL + "/teams",
	}

	mockDB := &mockWebhookQueue{
		queued: make([]*db.QueuedWebhookMessage, 0),
	}

	service := NewWebhookService(cfg, mockDB)

	// 1. Enqueue multiple alerts
	service.SendRegistrationAlert("test1@liferay.com", "sub1")
	service.SendRegistrationAlert("test2@liferay.com", "sub2")
	service.SendIPBlacklistAlert("127.0.0.1", "spam")

	// Verify no immediate server delivery happened
	mu.Lock()
	if len(receivedSlack) != 0 || len(receivedTeams) != 0 {
		t.Errorf("expected 0 immediate alert deliveries, got slack=%d, teams=%d", len(receivedSlack), len(receivedTeams))
	}
	mu.Unlock()

	// Verify database queue has 3 items
	mockDB.mu.Lock()
	if len(mockDB.queued) != 3 {
		t.Errorf("expected 3 enqueued messages, got %d", len(mockDB.queued))
	}
	mockDB.mu.Unlock()

	// 2. Trigger batch consumer coalescence execution
	service.processQueueBatch()

	// Verify database queue is now empty
	mockDB.mu.Lock()
	if len(mockDB.queued) != 0 {
		t.Errorf("expected queue to be empty after processing, got %d", len(mockDB.queued))
	}
	mockDB.mu.Unlock()

	// Verify single coalesced webhook request received
	mu.Lock()
	defer mu.Unlock()

	if len(receivedSlack) != 1 {
		t.Fatalf("expected 1 coalesced Slack digest payload, got %d", len(receivedSlack))
	}
	if len(receivedTeams) != 1 {
		t.Fatalf("expected 1 coalesced Teams digest payload, got %d", len(receivedTeams))
	}

	// Verify Slack payload block structure and content matching
	slackPayload := receivedSlack[0]
	blocks, ok := slackPayload["blocks"].([]interface{})
	if !ok || len(blocks) < 2 {
		t.Fatalf("invalid slack blocks format")
	}

	headerSection := blocks[0].(map[string]interface{})
	textObj := headerSection["text"].(map[string]interface{})
	if !strings.Contains(textObj["text"].(string), "Grouped Activity Digest") {
		t.Errorf("expected grouped digest title, got %q", textObj["text"].(string))
	}

	contentSection := blocks[1].(map[string]interface{})
	descObj := contentSection["text"].(map[string]interface{})
	descText := descObj["text"].(string)

	if !strings.Contains(descText, "test1@liferay.com") ||
		!strings.Contains(descText, "test2@liferay.com") ||
		!strings.Contains(descText, "127.0.0.1") {
		t.Errorf("digest payload does not contain coalesced details: %q", descText)
	}
}
