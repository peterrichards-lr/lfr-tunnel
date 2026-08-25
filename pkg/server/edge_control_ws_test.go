package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"

	"github.com/gorilla/websocket"
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

	// 3. Test IP Blacklist Broadcast propagation. nil is a ban with no expiry, which is what a
	// manual admin ban is.
	testIP := "198.51.100.42"
	controlSrv.BroadcastBlacklistUpdate("add", testIP, nil)

	// Wait for WS propagation
	time.Sleep(100 * time.Millisecond)

	if !edgeSrv.isBlacklisted(testIP) {
		t.Error("expected IP to be blacklisted on the Edge node via WS propagation")
	}

	controlSrv.BroadcastBlacklistUpdate("remove", testIP, nil)
	time.Sleep(100 * time.Millisecond)

	if edgeSrv.isBlacklisted(testIP) {
		t.Error("expected IP to be removed from the blacklist on the Edge node via WS propagation")
	}

	// 3b. An automatic ban carries its expiry across the control channel, so the edge lifts it
	// at the same moment the control plane does rather than holding it forever (#1353).
	expiringIP := "198.51.100.43"
	expiry := time.Now().Add(80 * time.Millisecond)
	controlSrv.BroadcastBlacklistUpdate("add", expiringIP, &expiry)
	time.Sleep(100 * time.Millisecond)

	if edgeSrv.isBlacklisted(expiringIP) {
		t.Error("an expiring ban was still in force on the Edge node after its expiry -- the edge would outlive the control plane's ban")
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
	_, _, _ = edgeSrv.registry.Register("user-1", "maint-lease", []PortMapping{{LocalPort: 8080}}, []string{"usedge.example.se"}, 100, "127.0.0.1", "", nil) //nolint:errcheck

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
	u, _err := url.Parse(ts.URL)
	_ = _err //nolint:errcheck
	wsURL := fmt.Sprintf("ws://%s/api/internal/edge-control-ws?node_id=usedge", u.Host)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

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

// TestServer_EdgeControlWS_SurvivesBeyondOldOneShotDeadline verifies the fix for the
// bug where the control plane's read deadline was set once and never refreshed by
// incoming Pings, forcing a disconnect/reconnect every ~60s regardless of how alive
// the connection actually was (Closes #848). edgeControlReadDeadline is overridden to
// a short duration so this can be verified without a real 60s wait: the test keeps
// the connection alive purely via Ping frames for longer than that shortened deadline
// would have survived under the old (pre-fix) behavior.
func TestServer_EdgeControlWS_SurvivesBeyondOldOneShotDeadline(t *testing.T) {
	original := edgeControlReadDeadline
	edgeControlReadDeadline = 150 * time.Millisecond
	defer func() { edgeControlReadDeadline = original }()

	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "usedge-keepalive-token"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer controlSrv.Stop()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	u, _err := url.Parse(ts.URL)
	_ = _err //nolint:errcheck
	wsURL := fmt.Sprintf("ws://%s/api/internal/edge-control-ws?node_id=usedge", u.Host)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	var challengeMsg struct {
		Type  string `json:"type"`
		Nonce string `json:"nonce"`
	}
	if err := conn.ReadJSON(&challengeMsg); err != nil {
		t.Fatalf("failed to read challenge: %v", err)
	}

	mac := hmac.New(sha256.New, tokenHashBytes[:])
	mac.Write([]byte(challengeMsg.Nonce))
	respHex := hex.EncodeToString(mac.Sum(nil))
	if err := conn.WriteJSON(map[string]string{"type": "auth", "response": respHex}); err != nil {
		t.Fatalf("failed to write auth: %v", err)
	}

	var result struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&result); err != nil {
		t.Fatalf("failed to read auth result: %v", err)
	}
	if result.Type != "auth_success" {
		t.Fatalf("expected auth_success, got %s", result.Type)
	}

	// Mimic the edge's real keepalive: send nothing but raw Ping frames, at an
	// interval comfortably shorter than the (shortened) deadline, for well longer
	// than that deadline's total duration. Under the pre-fix behavior this alone
	// would never prevent the one-shot deadline from expiring.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) //nolint:errcheck
			case <-stop:
				return
			}
		}
	}()
	defer close(stop)

	time.Sleep(500 * time.Millisecond) // ~3.3x the shortened deadline

	controlSrv.edgeClientsMu.RLock()
	_, stillConnected := controlSrv.edgeClients["usedge"]
	controlSrv.edgeClientsMu.RUnlock()

	if !stillConnected {
		t.Error("expected edge connection to survive well past the read deadline via Ping keepalive alone, but it was dropped — the reconnect-loop bug (#848) has regressed")
	}
}

// TestServer_EdgeControlWS_LatencyMeasuredViaPing verifies the fix for #976: edges are
// configured with no `url` in the current architecture (see docs/server/edge_setup_guide.md's
// example config), so they never got an entry via the HTTP-polling checkEdgeNodeHealth path
// at all -- EdgeHealthStatus.LatencyMs stayed permanently unset, rendered as "--" on the
// portal's Network Health screen regardless of how healthy the connection actually was. The
// control plane now sends its own periodic Ping over the WS control channel and times the
// Pong (edgeHealthPingInterval, overridden here to a short duration); the real edge client
// (runEdgeControlChannel) never overrides gorilla/websocket's default PingHandler, so it
// answers automatically without any edge-side change.
func TestServer_EdgeControlWS_LatencyMeasuredViaPing(t *testing.T) {
	original := edgeHealthPingInterval
	edgeHealthPingInterval = 50 * time.Millisecond
	defer func() { edgeHealthPingInterval = original }()

	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "usedge-latencytoken"
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

	// Give the edge time to connect, authenticate, and answer at least one RTT ping.
	time.Sleep(300 * time.Millisecond)

	controlSrv.edgeHealthMu.RLock()
	status, exists := controlSrv.edgeHealth["usedge"]
	controlSrv.edgeHealthMu.RUnlock()

	if !exists {
		t.Fatal("expected an EdgeHealthStatus entry for 'usedge' after at least one RTT ping cycle, but there was none")
	}
	// Not asserting LatencyMs > 0: a real loopback round-trip can legitimately measure
	// under a millisecond and truncate to 0, which is a correct measurement, not a
	// missing one -- LastCheckAt is the reliable signal that updateEdgeLatencyFromPing
	// actually ran (it's set unconditionally on every RTT measurement, success or not).
	if status.LatencyMs < 0 {
		t.Errorf("expected a non-negative LatencyMs, got %d", status.LatencyMs)
	}
	if status.LastCheckAt == 0 {
		t.Error("expected LastCheckAt to be set after the control plane's own Ping/Pong RTT cycle, but it was never recorded -- the ping mechanism never fired")
	}
}

// TestServer_EdgeControlChannel_SurvivesIdlePeriodViaPongHandler verifies the fix
// for #911: the edge's own outbound connection reset its read deadline right before
// each blocking read, but nothing ever refreshed that deadline during an idle period
// with no real ControlMessage traffic -- gorilla/websocket handles incoming Pong
// frames (replies to the edge's own Pings) internally and never surfaces them to the
// caller unless a PongHandler is registered, which the edge side never did (mirror-
// image of the already-fixed #848 bug on the server side). This forced a disconnect/
// reconnect every ~75s regardless of how alive the connection actually was --
// reported as "edge-apac cycles every ~75s over IPv6" but actually reproducible on
// any edge given a long enough idle gap, independent of network path or IP version.
//
// edgeClientReadDeadline/edgeClientPingInterval are overridden to short durations so
// this can be verified without a real 75s wait. No BroadcastBlacklistUpdate/
// BroadcastMaintenance/etc is ever sent, so the only thing keeping the connection
// alive is the edge's own Ping/Pong keepalive -- exactly the idle scenario that
// exposed the bug.
func TestServer_EdgeControlChannel_SurvivesIdlePeriodViaPongHandler(t *testing.T) {
	originalDeadline := edgeClientReadDeadline
	originalPingInterval := edgeClientPingInterval
	edgeClientReadDeadline = 300 * time.Millisecond
	edgeClientPingInterval = 50 * time.Millisecond
	defer func() {
		edgeClientReadDeadline = originalDeadline
		edgeClientPingInterval = originalPingInterval
	}()

	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "apacedge-mysecrettokenvalue"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "apacedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer controlSrv.Stop()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	cfgEdge := config.DefaultServerConfig()
	cfgEdge.DBPath = "" // Edge mode
	cfgEdge.Domains = []string{"apacedge.example.se"}
	cfgEdge.ControlPlaneURL = ts.URL
	cfgEdge.EdgeToken = edgeToken
	cfgEdge.DisableBackupScheduler = true

	edgeSrv, err := NewServer(cfgEdge)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}
	defer edgeSrv.Stop()

	time.Sleep(150 * time.Millisecond) // let the initial connection establish

	controlSrv.edgeClientsMu.RLock()
	initialConn, exists := controlSrv.edgeClients["apacedge"]
	controlSrv.edgeClientsMu.RUnlock()
	if !exists || initialConn == nil {
		t.Fatal("expected edge client 'apacedge' to be authenticated and registered on the control plane")
	}

	// Idle well past several read-deadline cycles, sending no real ControlMessage at
	// all -- the only thing that can keep this alive is the edge's own Ping/Pong.
	time.Sleep(1500 * time.Millisecond) // 5x the shortened deadline

	controlSrv.edgeClientsMu.RLock()
	finalConn, stillExists := controlSrv.edgeClients["apacedge"]
	controlSrv.edgeClientsMu.RUnlock()

	if !stillExists {
		t.Fatal("expected edge to still be connected after idling past the read deadline via Ping/Pong keepalive alone, but it disconnected and never reconnected")
	}
	if finalConn != initialConn {
		t.Error("expected the edge's connection to survive the entire idle period without reconnecting, but the registered connection object changed -- " +
			"the edge disconnected and reconnected at least once, meaning the #911 fix has regressed")
	}
}

func TestServer_EdgeControlWS_ProxyIP(t *testing.T) {
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "usedge-token"
	tokenHashBytes := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHashBytes[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer controlSrv.Stop()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	u, _err := url.Parse(ts.URL)
	_ = _err //nolint:errcheck
	wsURL := fmt.Sprintf("ws://%s/api/internal/edge-control-ws?node_id=usedge&version=v1.23.2", u.Host)

	header := make(http.Header)
	header.Set("X-Real-IP", "203.0.113.195")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	var challengeMsg struct {
		Type  string `json:"type"`
		Nonce string `json:"nonce"`
	}
	if err := conn.ReadJSON(&challengeMsg); err != nil {
		t.Fatalf("failed to read challenge: %v", err)
	}

	keyBytes := tokenHashBytes[:]
	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(challengeMsg.Nonce))
	respHex := hex.EncodeToString(mac.Sum(nil))

	authMsg := map[string]string{
		"type":     "auth",
		"response": respHex,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		t.Fatalf("failed to write auth: %v", err)
	}

	var result struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&result); err != nil {
		t.Fatalf("failed to read result: %v", err)
	}
	if result.Type != "auth_success" {
		t.Fatalf("expected auth_success, got %s", result.Type)
	}

	controlSrv.edgeClientsMu.RLock()
	trackedIP := controlSrv.edgeIPs["usedge"]
	controlSrv.edgeClientsMu.RUnlock()

	if trackedIP != "203.0.113.195" {
		t.Errorf("expected tracked IP 203.0.113.195, got %s", trackedIP)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://tunnel.example.se/api/portal/edge-health", nil)
	controlSrv.handleEdgeHealth(rec, req)

	var resp struct {
		Nodes map[string]EdgeHealthStatus `json:"nodes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	node, exists := resp.Nodes["usedge"]
	if !exists {
		t.Fatal("expected usedge node in health status")
	}
	if node.ResolvedIP != "203.0.113.195" {
		t.Errorf("expected ResolvedIP 203.0.113.195, got %s", node.ResolvedIP)
	}
}
