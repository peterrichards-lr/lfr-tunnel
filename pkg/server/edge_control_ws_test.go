package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lfr-tunnel/pkg/config"
)

func TestServer_EdgeControlWS_AuthenticationAndPubSub(t *testing.T) {
	// 1. Start Control Plane Server
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	// Configure authorized edge node
	edgeToken := "usedge-mysecrettokenvalue"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		controlSrv.Stop()
	}()

	// Start test HTTP server for control plane
	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	// 2. Start mock Edge Server
	cfgEdge := config.DefaultServerConfig()
	cfgEdge.DBPath = "" // Edge mode
	cfgEdge.Domains = []string{"usedge.example.se"}
	cfgEdge.ControlPlaneURL = ts.URL
	cfgEdge.EdgeToken = edgeToken
	cfgEdge.DisableBackupScheduler = true

	edgeSrv, err := NewServer(cfgEdge)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		edgeSrv.Stop()
	}()

	// Give a moment for the Edge node client loop to connect and authenticate
	time.Sleep(200 * time.Millisecond)

	// Check that Edge node is registered in Control Plane
	controlSrv.edgeClientsMu.RLock()
	clientConn, exists := controlSrv.edgeClients["usedge"]
	controlSrv.edgeClientsMu.RUnlock()

	if !exists || clientConn == nil {
		t.Error("expected edge client 'usedge' to be authenticated and registered on the control plane, but it wasn't")
	}

	// 3. Test IP Blacklist Broadcast propagation
	testIP := "198.51.100.42"
	controlSrv.BroadcastBlacklistUpdate("add", testIP)

	// Wait for WS propagation
	time.Sleep(100 * time.Millisecond)

	if _, blocked := edgeSrv.blacklist.Load(testIP); !blocked {
		t.Error("expected IP to be blacklisted on the Edge node via WS propagation")
	}

	controlSrv.BroadcastBlacklistUpdate("remove", testIP)
	time.Sleep(100 * time.Millisecond)

	if _, blocked := edgeSrv.blacklist.Load(testIP); blocked {
		t.Error("expected IP to be removed from the blacklist on the Edge node via WS propagation")
	}

	// 4. Test Lease Kick propagation
	// Create a dummy lease on the Edge Node registry
	sub := "my-ws-lease"
	session, remotes, err := edgeSrv.registry.Register("user-1", sub, []PortMapping{{LocalPort: 8080}}, []string{"usedge.example.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("failed to register lease on edge: %v", err)
	}
	t.Logf("Registered lease: %s, remotes: %v", session, remotes)

	// Verify lease exists on Edge node
	if len(edgeSrv.registry.ListLeases()) != 1 {
		t.Error("expected exactly 1 lease in the Edge node registry")
	}

	// Trigger kick from Control Plane
	controlSrv.sendEdgeWSKick("usedge", sub)
	time.Sleep(100 * time.Millisecond)

	// Verify lease was kicked on Edge node
	if len(edgeSrv.registry.ListLeases()) != 0 {
		t.Error("expected lease to be kicked on Edge node via WS propagation")
	}

	// 5. Test Maintenance Mode propagation
	// Create another lease on Edge
	_, _, _ = edgeSrv.registry.Register("user-1", "maint-lease", []PortMapping{{LocalPort: 8080}}, []string{"usedge.example.se"}, 100, "127.0.0.1", "", nil)

	controlSrv.BroadcastMaintenance("enable", 10, "Upgrading control plane")
	time.Sleep(100 * time.Millisecond)

	edgeSrv.maintMutex.RLock()
	edgeMaintActive := edgeSrv.maintenanceMode
	edgeSrv.maintMutex.RUnlock()

	if !edgeMaintActive {
		t.Error("expected Edge Node to enter maintenance mode via WS propagation")
	}

	if len(edgeSrv.registry.ListLeases()) != 0 {
		t.Error("expected Edge leases to be terminated upon entering maintenance mode")
	}
}

func TestServer_EdgeControlWS_HMACFail(t *testing.T) {
	// Start Control Plane Server
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "correct-token"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		controlSrv.Stop()
	}()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	// Dial WS manually with invalid HMAC to verify rejection
	u, _ := url.Parse(ts.URL)
	wsURL := fmt.Sprintf("ws://%s/api/internal/edge-control-ws?node_id=usedge", u.Host)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Read challenge
	var challengeMsg struct {
		Type  string `json:"type"`
		Nonce string `json:"nonce"`
	}
	if err := conn.ReadJSON(&challengeMsg); err != nil {
		t.Fatalf("failed to read challenge: %v", err)
	}

	// Compute invalid HMAC response (using wrong key)
	wrongKey := sha256.Sum256([]byte("wrong-token"))
	mac := hmac.New(sha256.New, wrongKey[:])
	mac.Write([]byte(challengeMsg.Nonce))
	respHex := hex.EncodeToString(mac.Sum(nil))

	authMsg := map[string]string{
		"type":     "auth",
		"response": respHex,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		t.Fatalf("failed to write auth: %v", err)
	}

	// Expect authentication failure
	var result struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	if err := conn.ReadJSON(&result); err != nil {
		t.Fatalf("failed to read result: %v", err)
	}

	if result.Type != "auth_failed" {
		t.Errorf("expected Type auth_failed, got %s", result.Type)
	}
}

func TestServer_EdgeActions(t *testing.T) {
	// Start Control Plane Server
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	// Configure authorized edge node
	edgeToken := "usedge-mysecrettokenvalue"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		controlSrv.Stop()
	}()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	// Start mock Edge Server
	cfgEdge := config.DefaultServerConfig()
	cfgEdge.DBPath = "" // Edge mode
	cfgEdge.Domains = []string{"usedge.example.se"}
	cfgEdge.ControlPlaneURL = ts.URL
	cfgEdge.EdgeToken = edgeToken
	cfgEdge.DisableBackupScheduler = true

	edgeSrv, err := NewServer(cfgEdge)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		edgeSrv.Stop()
	}()

	// Give a moment for connection
	time.Sleep(200 * time.Millisecond)

	// Verify version was registered
	controlSrv.edgeClientsMu.RLock()
	ver, ok := controlSrv.edgeVersions["usedge"]
	controlSrv.edgeClientsMu.RUnlock()
	if !ok || ver != config.Version {
		t.Errorf("expected version %s, got %s (ok=%v)", config.Version, ver, ok)
	}

	// Create a mock session to test SendEdgeKickAll
	_, _, err = edgeSrv.registry.Register("user-1", "test-kick-wildcard", []PortMapping{{LocalPort: 8080}}, []string{"usedge.example.se"}, 100, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("failed to register lease: %v", err)
	}

	// Trigger Kick All via helper
	err = controlSrv.SendEdgeKickAll("usedge")
	if err != nil {
		t.Fatalf("failed to send edge kick all: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Lease should be gone
	if len(edgeSrv.registry.ListLeases()) != 0 {
		t.Error("expected all leases to be kicked on edge node")
	}

	// Verify edge-health response includes version
	req := httptest.NewRequest("GET", "http://tunnel.example.se/api/portal/edge-health", nil)
	rec := httptest.NewRecorder()
	controlSrv.handleEdgeHealth(rec, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode edge-health response: %v", err)
	}
	node, exists := resp.Nodes["usedge"]
	if !exists {
		t.Fatal("expected 'usedge' node in health status")
	}
	if node.Version != config.Version {
		t.Errorf("expected node version %s, got %s", config.Version, node.Version)
	}
}
