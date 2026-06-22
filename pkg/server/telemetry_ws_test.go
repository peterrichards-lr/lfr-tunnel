package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestServer_TelemetryWS(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "test_ws.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	}()

	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})

	// Setup a mock test server using HTTP Server
	testServer := httptest.NewServer(srv)
	defer testServer.Close()

	// 1. Rejection on unauthorized connection (no cookie)
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/api/portal/telemetry/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected dial to fail without auth cookie")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", resp.StatusCode)
	}

	// Create session for authorization
	sessionToken := "test-session-123"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     "test@example.com",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// 2. Successful connection (with cookie)
	header := http.Header{}
	header.Set("Cookie", "lfr_session="+sessionToken)
	wsConn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to establish websocket connection: %v", err)
	}
	defer wsConn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected switching protocols (101), got %d", resp.StatusCode)
	}

	// 3. Receive initial telemetry push
	var msg struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read initial telemetry message: %v", err)
	}

	if err := json.Unmarshal(p, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "telemetry" {
		t.Errorf("expected message type 'telemetry', got '%s'", msg.Type)
	}
	if email, ok := msg.Data["email"].(string); !ok || email != "test@example.com" {
		t.Errorf("expected data email to be test@example.com, got %v", msg.Data["email"])
	}

	// 4. Test targeted message broadcast
	srv.targetedMutex.Lock()
	srv.targetedMessages["test@example.com"] = "Hello Peter"
	srv.targetedMutex.Unlock()

	srv.PushUserTelemetryByID("test@example.com")

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err = wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read targeted message update: %v", err)
	}

	var msg2 struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(p, &msg2); err != nil {
		t.Fatalf("failed to parse targeted message JSON: %v", err)
	}
	if tm, ok := msg2.Data["targeted_message"].(string); !ok || tm != "Hello Peter" {
		t.Errorf("expected targeted message 'Hello Peter', got '%v'", msg2.Data["targeted_message"])
	}

	// 5. Test global broadcast trigger
	srv.broadcastMutex.Lock()
	srv.broadcastMessage = "Alert System Update"
	srv.broadcastMutex.Unlock()

	srv.BroadcastTelemetry()

	wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err = wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read global broadcast message update: %v", err)
	}

	var msg3 struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(p, &msg3); err != nil {
		t.Fatalf("failed to parse global broadcast JSON: %v", err)
	}
	if bm, ok := msg3.Data["broadcast_message"].(string); !ok || bm != "Alert System Update" {
		t.Errorf("expected broadcast message 'Alert System Update', got '%v'", msg3.Data["broadcast_message"])
	}
}
