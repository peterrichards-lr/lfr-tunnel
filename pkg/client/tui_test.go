package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTUI_FormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1610612736, "1.5 GB"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d): expected %q, got %q", tt.bytes, tt.expected, result)
		}
	}
}

func TestTUI_FormatUptime(t *testing.T) {
	// Zero value
	if result := formatUptime(time.Time{}); result != "00:00" {
		t.Errorf("Expected uptime zero to be 00:00, got %s", result)
	}

	// 5 seconds ago
	fiveSecAgo := time.Now().Add(-5 * time.Second)
	if result := formatUptime(fiveSecAgo); result != "00:05" {
		t.Errorf("Expected uptime to be 00:05, got %s", result)
	}

	// 65 seconds ago (1:05)
	oneMinFive := time.Now().Add(-65 * time.Second)
	if result := formatUptime(oneMinFive); result != "01:05" {
		t.Errorf("Expected uptime to be 01:05, got %s", result)
	}

	// 2 hours, 3 minutes, 4 seconds ago (02:03:04)
	longTimeAgo := time.Now().Add(-2 * time.Hour).Add(-3 * time.Minute).Add(-4 * time.Second)
	if result := formatUptime(longTimeAgo); result != "02:03:04" {
		t.Errorf("Expected uptime to be 02:03:04, got %s", result)
	}
}

func TestTUI_ColorStatus(t *testing.T) {
	// Pending status 0
	if result := colorStatus(0); !strings.Contains(result, "In-Flight") {
		t.Errorf("Expected status 0 to be In-Flight, got %s", result)
	}

	// Green status 200
	if result := colorStatus(200); !strings.Contains(result, "200 OK") || !strings.Contains(result, "\033[32m") {
		t.Errorf("Expected 200 OK to be green, got %q", result)
	}

	// Yellow status 302
	if result := colorStatus(302); !strings.Contains(result, "302 Found") || !strings.Contains(result, "\033[33m") {
		t.Errorf("Expected 302 to be yellow, got %q", result)
	}

	// Red status 502
	if result := colorStatus(502); !strings.Contains(result, "502 Bad Gateway") || !strings.Contains(result, "\033[31m") {
		t.Errorf("Expected 502 to be red, got %q", result)
	}
}

func TestTUI_LogWriter(t *testing.T) {
	writer := &tuiLogWriter{}
	n, err := writer.Write([]byte("Info: Client connecting\nWarn: Retrying connection\n"))
	if err != nil {
		t.Fatalf("failed to write logs: %v", err)
	}
	if n != len("Info: Client connecting\nWarn: Retrying connection\n") {
		t.Errorf("expected to write %d bytes, wrote %d", len("Info: Client connecting\nWarn: Retrying connection\n"), n)
	}

	logs := writer.GetLogs()
	if len(logs) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(logs))
	}
	if logs[0] != "Info: Client connecting" {
		t.Errorf("expected first log to be 'Info: Client connecting', got %q", logs[0])
	}
	if logs[1] != "Warn: Retrying connection" {
		t.Errorf("expected second log to be 'Warn: Retrying connection', got %q", logs[1])
	}
}

func TestTUI_DashboardLifecycle(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.ConnState = "connected"
	engine.UptimeStart = time.Now()
	engine.RequestsTotal = 42
	engine.BytesIn = 1000
	engine.BytesOut = 2000
	engine.ActiveConnections = 2

	record := &RequestRecord{
		ID:         "test-id",
		Time:       time.Now(),
		Method:     "GET",
		Path:       "/api/users",
		Status:     http.StatusOK,
		DurationMs: 15,
	}
	engine.AddRecord(record)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the dashboard exits on the first check loop
	cancel()

	cleanup := StartTUIDashboard(ctx, engine, []string{"https://peter-dev.lfr-demo.se"})
	cleanup() // Exits screen and restores standard logger output
}
