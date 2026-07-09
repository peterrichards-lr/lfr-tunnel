package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"

	"github.com/gorilla/websocket"
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
	defer func() { _ = wsConn.Close() }()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected switching protocols (101), got %d", resp.StatusCode)
	}

	// 3. Receive initial telemetry push
	var msg struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}

	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
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

	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
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

	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
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

func TestServer_TelemetryWS_ChannelCloseRace(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "test_ws_race.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// Create a dummy client without actual connection to isolate the channel logic
	client := &wsClient{
		server: srv,
		userID: "test@example.com",
		email:  "test@example.com",
		send:   make(chan []byte, 2),
		done:   make(chan struct{}),
	}

	srv.registerWSClient(client)

	// Spin up multiple goroutines pushing telemetry and unregistering/closing the client
	// to see if we can trigger any write-on-closed-channel panic.
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			srv.pushUserTelemetry(client)
			time.Sleep(1 * time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			srv.BroadcastTelemetry()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		srv.unregisterWSClient(client)
		client.close()
	}()

	wg.Wait()
}

func TestServer_TelemetryCache(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "test_cache.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	email := "cachetest@example.com"
	user := &db.User{
		ID:        email,
		Email:     email,
		FirstName: "Initial",
		Role:      "user",
		Status:    "approved",
	}
	_ = srv.db.CreateUser(user)

	// 1. Initial lookup - should cache the user
	u1, err := srv.getCachedUser(email)
	if err != nil {
		t.Fatalf("failed to get cached user: %v", err)
	}
	if u1.FirstName != "Initial" {
		t.Errorf("expected first name 'Initial', got '%s'", u1.FirstName)
	}

	// 2. Modify DB directly (without updating cache)
	user.FirstName = "Modified"
	_ = srv.db.UpdateUser(user)

	// 3. Second lookup - should hit cache and still return 'Initial'
	u2, err := srv.getCachedUser(email)
	if err != nil {
		t.Fatalf("failed to get cached user: %v", err)
	}
	if u2.FirstName != "Initial" {
		t.Errorf("expected cached hit to return old name 'Initial', got '%s'", u2.FirstName)
	}

	// 4. Invalidate cache
	srv.invalidateUserCache(email)

	// 5. Third lookup - should query DB and return 'Modified'
	u3, err := srv.getCachedUser(email)
	if err != nil {
		t.Fatalf("failed to get cached user: %v", err)
	}
	if u3.FirstName != "Modified" {
		t.Errorf("expected cache miss to return updated name 'Modified', got '%s'", u3.FirstName)
	}
}
