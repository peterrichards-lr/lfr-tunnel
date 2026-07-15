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

	service := NewWebhookService(cfg)

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
