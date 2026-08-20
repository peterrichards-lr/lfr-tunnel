package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"

	"github.com/gorilla/websocket"
)

// edgeControlReadDeadline is how long the control plane waits for any frame
// (data or Ping) from an edge before considering the connection dead. Overridable
// in tests so the reconnect-loop fix can be verified without a real 60s wait.
var edgeControlReadDeadline = 60 * time.Second

// edgeClientReadDeadline is the mirror-image of edgeControlReadDeadline for the
// edge's own outbound connection: how long an edge waits for any frame (data or
// Pong) from the control plane before considering the connection dead. Overridable
// in tests for the same reason. See #911.
var edgeClientReadDeadline = 75 * time.Second

// edgeClientPingInterval is how often an edge sends a keepalive Ping to the control
// plane on its outbound connection. Overridable in tests so edgeClientReadDeadline's
// fix can be verified without a real 30s+ wait.
var edgeClientPingInterval = 30 * time.Second

// edgeHealthPingInterval is how often the control plane sends its OWN Ping to each
// connected edge, purely to time the Pong for RTT (see handleEdgeControlWS's PongHandler
// and #976) -- independent of edgeClientPingInterval above, which is the edge's existing
// keepalive in the other direction and was never used for timing. Overridable in tests.
var edgeHealthPingInterval = 20 * time.Second

// ControlMessage represents the JSON schema for websocket communication.
type ControlMessage struct {
	Type             string            `json:"type"`
	Nonce            string            `json:"nonce,omitempty"`
	Response         string            `json:"response,omitempty"`
	IP               string            `json:"ip,omitempty"`
	NodeID           string            `json:"node_id,omitempty"`
	Action           string            `json:"action,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	Duration         int               `json:"duration,omitempty"`
	UserID           string            `json:"user_id,omitempty"`
	Subdomain        string            `json:"subdomain,omitempty"`
	SecondsRemaining int               `json:"seconds_remaining,omitempty"`
	ShutdownAt       int64             `json:"shutdown_at,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
}

// handleEdgeControlWS handles control plane WebSocket connections from Edge nodes.
func (s *Server) handleEdgeControlWS(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		http.Error(w, "missing node_id", http.StatusBadRequest)
		return
	}
	version := r.URL.Query().Get("version")

	var clientIP string
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		clientIP = xrip
	} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		clientIP = strings.TrimSpace(parts[0])
	} else {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		clientIP = host
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to upgrade WebSocket: %v", err))
		return
	}

	// 1. Generate challenge nonce
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce) //nolint:errcheck
	nonceStr := hex.EncodeToString(nonce)

	challenge := ControlMessage{
		Type:  "challenge",
		Nonce: nonceStr,
	}

	if err := conn.WriteJSON(challenge); err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to send challenge to %s: %v", nodeID, err))
		_ = conn.Close() //nolint:errcheck
		return
	}

	// 2. Wait for auth response
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	var authMsg ControlMessage
	if err := conn.ReadJSON(&authMsg); err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Failed to read auth message from %s: %v", nodeID, err))
		_ = conn.Close() //nolint:errcheck
		return
	}

	if authMsg.Type != "auth" {
		slog.Info(fmt.Sprintf("[Edge WS] Expected auth message from %s, got %s", nodeID, authMsg.Type))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unexpected message type"}) //nolint:errcheck
		_ = conn.Close()                                                                           //nolint:errcheck
		return
	}

	// 3. Verify HMAC response
	var nodeConfig *config.EdgeNodeConfig
	for _, node := range s.cfg.EdgeNodes {
		if node.ID == nodeID {
			nodeConfig = &node
			break
		}
	}

	if nodeConfig == nil {
		slog.Info(fmt.Sprintf("[Edge WS] Unknown edge node ID: %s", nodeID))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unknown node_id"}) //nolint:errcheck
		_ = conn.Close()                                                                   //nolint:errcheck
		return
	}

	keyBytes, err := hex.DecodeString(nodeConfig.TokenHash)
	if err != nil {
		slog.Info(fmt.Sprintf("[Edge WS] Invalid token hash configured for %s", nodeID))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "invalid token hash"}) //nolint:errcheck
		_ = conn.Close()                                                                      //nolint:errcheck
		return
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(nonceStr))
	expectedMAC := mac.Sum(nil)

	respMAC, err := hex.DecodeString(authMsg.Response)
	if err != nil || subtle.ConstantTimeCompare(respMAC, expectedMAC) != 1 {
		slog.Info(fmt.Sprintf("[Edge WS] HMAC verification failed for %s", nodeID))
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "invalid signature"}) //nolint:errcheck
		_ = conn.Close()                                                                     //nolint:errcheck
		return
	}

	// Reset read deadline
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	// Authenticated! Register edge client
	s.edgeClientsMu.Lock()
	if oldConn, exists := s.edgeClients[nodeID]; exists {
		_ = oldConn.WriteJSON(ControlMessage{Type: "replaced", Reason: "new connection established"}) //nolint:errcheck
		_ = oldConn.Close()                                                                           //nolint:errcheck
	}
	s.edgeClients[nodeID] = &safeConn{conn: conn}
	if version != "" {
		s.edgeVersions[nodeID] = version
	} else {
		s.edgeVersions[nodeID] = "Unknown"
	}
	s.edgeIPs[nodeID] = clientIP
	s.edgeClientsMu.Unlock()

	slog.Info(fmt.Sprintf("[Edge WS] Edge node %s successfully authenticated.", nodeID))
	_ = conn.WriteJSON(ControlMessage{Type: "auth_success"}) //nolint:errcheck

	// pingStop signals the RTT-ping goroutine below to exit once the read pump's defer
	// runs -- it has no other way to notice the connection is gone, since it only ever
	// writes and never reads.
	pingStop := make(chan struct{})

	// Start read pump to keep alive and detect disconnects
	go func() {
		defer func() {
			close(pingStop)
			s.edgeClientsMu.Lock()
			if activeConn, exists := s.edgeClients[nodeID]; exists && activeConn.conn == conn {
				delete(s.edgeClients, nodeID)
				delete(s.edgeVersions, nodeID)
				delete(s.edgeIPs, nodeID)
			}
			s.edgeClientsMu.Unlock()
			s.edgePingMu.Lock()
			delete(s.edgePingSentAt, nodeID)
			s.edgePingMu.Unlock()
			_ = conn.Close() //nolint:errcheck
			slog.Info(fmt.Sprintf("[Edge WS] Edge node %s disconnected.", nodeID))
		}()

		// Set read limit and pong handler
		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(edgeControlReadDeadline)) //nolint:errcheck
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(edgeControlReadDeadline)) //nolint:errcheck
			// Answers our own RTT-ping below, not the edge's keepalive Ping (that one
			// gets a Pong reply from the PingHandler further down, never from us
			// sending a Ping ourselves) -- see #976. A miss (no recorded send time,
			// e.g. a stray Pong right after reconnect) just leaves latency unchanged
			// rather than recording a bogus value.
			s.edgePingMu.Lock()
			sentAt, ok := s.edgePingSentAt[nodeID]
			s.edgePingMu.Unlock()
			if ok {
				s.updateEdgeLatencyFromPing(nodeID, time.Since(sentAt).Milliseconds())
			}
			return nil
		})

		// The edge sends its own Ping every 30s (runEdgeControlChannel), not a Pong,
		// so the PongHandler above never actually fires — nothing ever prompts the
		// edge to send a Pong back to us, and this server never sends a Ping of its
		// own either. Left with only the PongHandler as a reset mechanism, the
		// initial deadline set above is a one-shot timer that always expires,
		// forcing a disconnect/reconnect every ~60s regardless of how alive the
		// connection actually is. gorilla/websocket handles incoming Ping/Pong
		// frames internally inside ReadMessage/NextReader and never surfaces them
		// to the caller, so resetting the deadline only *after* ReadMessage returns
		// would not fire on those pings either — a custom PingHandler is what
		// actually observes them. Replicate the default handler's pong reply here
		// too, since registering a custom handler replaces it entirely.
		conn.SetPingHandler(func(appData string) error {
			_ = conn.SetReadDeadline(time.Now().Add(edgeControlReadDeadline)) //nolint:errcheck
			err := conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
			if err == websocket.ErrCloseSent {
				return nil
			} else if e, ok := err.(net.Error); ok && e.Temporary() { //nolint:staticcheck
				return nil
			}
			return err
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
			_ = conn.SetReadDeadline(time.Now().Add(edgeControlReadDeadline)) //nolint:errcheck
		}
	}()

	// Send our own periodic Ping so the PongHandler above has something to time (#976) --
	// the edge's existing keepalive Ping (runEdgeControlChannel, other direction) was never
	// usable for this since we only reply to it, we don't time it. WriteControl is safe to
	// call concurrently with the read pump's WriteControl (PongHandler, above) and with any
	// WriteJSON/WriteMessage via safeConn elsewhere -- gorilla/websocket exempts
	// WriteControl from its single-writer restriction.
	go func() {
		ticker := time.NewTicker(edgeHealthPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingStop:
				return
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.edgePingMu.Lock()
				s.edgePingSentAt[nodeID] = time.Now()
				s.edgePingMu.Unlock()
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()
}

// BroadcastBlacklistUpdate pushes an IP blacklist update to all connected Edge nodes.
func (s *Server) BroadcastBlacklistUpdate(action, ip string) {
	msg := ControlMessage{
		Type:   "blacklist_update",
		Action: action, // "add" or "remove"
		IP:     ip,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	for _, conn := range s.edgeClients {
		_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
	}
}

// BroadcastMaintenance pushes a maintenance mode event to all connected Edge nodes.
func (s *Server) BroadcastMaintenance(action string, duration int, reason string) {
	msg := ControlMessage{
		Type:     "maintenance_trigger",
		Action:   action, // "enable" or "disable"
		Duration: duration,
		Reason:   reason,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	for _, conn := range s.edgeClients {
		_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
	}
}

// BroadcastNodeShutdownWarning sends a shutdown warning notification to a specific edge node or all edge nodes.
func (s *Server) BroadcastNodeShutdownWarning(nodeID string, secondsRemaining int, reason string) {
	shutdownAt := time.Now().Unix() + int64(secondsRemaining)
	msg := ControlMessage{
		Type:             "node_shutdown_warning",
		NodeID:           nodeID,
		Action:           "shutdown_warning",
		SecondsRemaining: secondsRemaining,
		ShutdownAt:       shutdownAt,
		Reason:           reason,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.edgeClientsMu.RLock()
	defer s.edgeClientsMu.RUnlock()

	if nodeID != "" {
		if conn, exists := s.edgeClients[nodeID]; exists {
			_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
		}
	} else {
		for _, conn := range s.edgeClients {
			_ = conn.WriteMessage(websocket.TextMessage, payload) //nolint:errcheck
		}
	}
}

// sendEdgeWSKick sends a lease kick message to a specific Edge node via WebSocket.
// Returns true if the message was sent successfully.
func (s *Server) sendEdgeWSKick(nodeID, subdomain string) bool {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists {
		return false
	}

	msg := ControlMessage{
		Type:      "lease_kick",
		Subdomain: subdomain,
	}

	err := conn.WriteJSON(msg)
	return err == nil
}

// sendEdgeWSHeaders sends a lease headers update to a specific Edge node via WebSocket.
// Returns true if the message was sent successfully.
func (s *Server) sendEdgeWSHeaders(nodeID, fullHost string, headers map[string]string) bool {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists {
		return false
	}

	msg := ControlMessage{
		Type:      "lease_headers",
		Subdomain: fullHost,
		Headers:   headers,
	}

	err := conn.WriteJSON(msg)
	return err == nil
}

// SendEdgeRestart sends a restart command to a specific edge node.
func (s *Server) SendEdgeRestart(nodeID string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type: "restart",
	}
	return conn.WriteJSON(msg)
}

// SendEdgeMaintenance sends a maintenance mode trigger to a specific edge node.
func (s *Server) SendEdgeMaintenance(nodeID string, action string, duration int, reason string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type:     "maintenance_trigger",
		Action:   action,
		Duration: duration,
		Reason:   reason,
	}
	return conn.WriteJSON(msg)
}

// SendEdgeKickAll kicks all active leases/tunnels on a specific edge node.
func (s *Server) SendEdgeKickAll(nodeID string) error {
	s.edgeClientsMu.RLock()
	conn, exists := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()

	if !exists || conn == nil {
		return fmt.Errorf("edge node %s is offline or not connected", nodeID)
	}

	msg := ControlMessage{
		Type:      "lease_kick",
		Subdomain: "*",
	}
	return conn.WriteJSON(msg)
}

// kickAllLocalLeases terminates all tunnels hosted locally on this server instance.
func (s *Server) kickAllLocalLeases() {
	if s.registry == nil {
		return
	}
	leases := s.registry.ListLeases()
	for _, l := range leases {
		slog.Info(fmt.Sprintf("[Edge Control] Terminating lease for %s", l.FullHost))
		s.registry.KickLease(l.SubdomainPrefix)
	}
}

// runEdgeControlChannel manages the Edge Node's client control WebSocket connection.
func (s *Server) runEdgeControlChannel() {
	lostAt := time.Time{}

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		u, err := url.Parse(s.cfg.ControlPlaneURL)
		if err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Invalid ControlPlaneURL: %v", err))
			time.Sleep(10 * time.Second)
			continue
		}

		nodeID := ""
		parts := strings.Split(s.cfg.EdgeToken, "-")
		if len(parts) > 1 {
			nodeID = strings.Join(parts[:len(parts)-1], "-")
		} else if len(parts) == 1 {
			nodeID = parts[0]
		}
		if nodeID == "" {
			nodeID = "edge"
		}

		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		wsURL := fmt.Sprintf("%s://%s/api/internal/edge-control-ws?node_id=%s&version=%s", scheme, u.Host, nodeID, url.QueryEscape(config.Version))

		slog.Info(fmt.Sprintf("[Edge Control] Connecting to Control Plane at %s...", wsURL))

		dialer := websocket.DefaultDialer
		dialer.HandshakeTimeout = 5 * time.Second
		// Force IPv4 for this outbound connection (see #911): on dual-stack edges, the
		// default dialer prefers IPv6 when both are available, but at least one edge
		// region's IPv6 path to the control plane has exhibited a ~75s idle-connection
		// timeout at an intermediate network hop, causing needless reconnect churn. Every
		// edge always has a guaranteed IPv4 Elastic IP (IPv6 is opt-in), so this is safe
		// across all regions, not just the affected one.
		dialer.NetDialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		}

		if s.cfg.InsecureSkipVerify {
			dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}

		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Connection failed: %v", err))
			if lostAt.IsZero() {
				lostAt = time.Now()
			} else if time.Since(lostAt) > 3*time.Minute {
				slog.Info("[Edge Control] Connection lost for >3 minutes. Terminating all active tunnels...")
				s.kickAllLocalLeases()
			}
			time.Sleep(10 * time.Second)
			continue
		}

		// Connected! Reset lostAt timer
		lostAt = time.Time{}

		// 1. Receive challenge
		var challengeMsg ControlMessage
		if err := conn.ReadJSON(&challengeMsg); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to read challenge: %v", err))
			_ = conn.Close() //nolint:errcheck
			time.Sleep(5 * time.Second)
			continue
		}

		if challengeMsg.Type != "challenge" {
			slog.Info(fmt.Sprintf("[Edge Control] Expected challenge message, got %s", challengeMsg.Type))
			_ = conn.Close() //nolint:errcheck
			time.Sleep(5 * time.Second)
			continue
		}

		// 2. Calculate HMAC response using sha256(EdgeToken)
		key := sha256.Sum256([]byte(s.cfg.EdgeToken))
		mac := hmac.New(sha256.New, key[:])
		mac.Write([]byte(challengeMsg.Nonce))
		respHex := hex.EncodeToString(mac.Sum(nil))

		authMsg := ControlMessage{
			Type:     "auth",
			Response: respHex,
		}
		if err := conn.WriteJSON(authMsg); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to send auth response: %v", err))
			_ = conn.Close() //nolint:errcheck
			time.Sleep(5 * time.Second)
			continue
		}

		// 3. Receive auth result
		var authResult ControlMessage
		if err := conn.ReadJSON(&authResult); err != nil {
			slog.Info(fmt.Sprintf("[Edge Control] Failed to read auth result: %v", err))
			_ = conn.Close() //nolint:errcheck
			time.Sleep(5 * time.Second)
			continue
		}

		if authResult.Type != "auth_success" {
			slog.Info(fmt.Sprintf("[Edge Control] Authentication failed: %s", authResult.Reason))
			_ = conn.Close() //nolint:errcheck
			time.Sleep(10 * time.Second)
			continue
		}

		slog.Info("[Edge Control] Successfully connected and authenticated with Control Plane.")

		// The read loop below resets its 75s read deadline before each blocking read,
		// but that only actually gets hit once a real ControlMessage arrives -- and the
		// control plane only sends one on real events (blacklist updates, maintenance
		// triggers, lease kicks, etc.), not on a fixed schedule. During an idle period
		// with no such events, nothing refreshes the deadline except this edge's own
		// outgoing Ping (sent every 30s below) getting a Pong back -- but gorilla/
		// websocket handles incoming Pong frames internally and never surfaces them to
		// the caller unless a PongHandler is registered (see #911; same class of bug the
		// server side already had to work around for the equivalent Ping case). Without
		// this, any edge idle for >75s hits the deadline and reconnects regardless of
		// network conditions -- confirmed as the actual root cause of edge-apac's ~75s
		// reconnect cycling, not a network-path timeout as originally suspected. Other
		// edges apparently receive enough incidental real ControlMessage traffic
		// (broadcasts) to keep resetting the deadline before it fires; edge-apac's idle
		// periods are long enough to expose the missing handler.
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(edgeClientReadDeadline)) //nolint:errcheck
			return nil
		})

		// Start ticker to send ping messages
		ticker := time.NewTicker(edgeClientPingInterval)
		pingErrChan := make(chan error, 1)

		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						pingErrChan <- err
						return
					}
				case <-s.ctx.Done():
					return
				}
			}
		}()

		// Read loop
		for {
			var msg ControlMessage
			// Reset read deadline on receiving messages
			_ = conn.SetReadDeadline(time.Now().Add(edgeClientReadDeadline)) //nolint:errcheck

			readErrChan := make(chan error, 1)
			go func() {
				err := conn.ReadJSON(&msg)
				readErrChan <- err
			}()

			var readErr error
			select {
			case readErr = <-readErrChan:
			case pingErr := <-pingErrChan:
				readErr = pingErr
			}

			if readErr != nil {
				slog.Info(fmt.Sprintf("[Edge Control] Connection closed or read failed: %v", readErr))
				break
			}

			switch msg.Type {
			case "restart":
				slog.Info("[Edge Control] Restart request received from Control Plane. Exiting...")
				os.Exit(1)
			case "blacklist_update":
				switch msg.Action {
				case "add":
					slog.Info(fmt.Sprintf("[Edge Control] Blacklisting IP: %s", msg.IP))
					s.blacklist.Store(msg.IP, true)
				case "remove":
					slog.Info(fmt.Sprintf("[Edge Control] Unblacklisting IP: %s", msg.IP))
					s.blacklist.Delete(msg.IP)
				}
			case "maintenance_trigger":
				s.maintMutex.Lock()
				switch msg.Action {
				case "enable":
					slog.Info(fmt.Sprintf("[Edge Control] Maintenance enabled: %s (duration: %d mins)", msg.Reason, msg.Duration))
					s.maintenanceMode = true
					s.kickAllLocalLeases()
				case "disable":
					slog.Info("[Edge Control] Maintenance disabled.")
					s.maintenanceMode = false
				}
				s.maintMutex.Unlock()
			case "lease_kick":
				if msg.Subdomain == "*" || msg.Subdomain == "" {
					slog.Info("[Edge Control] Kicking ALL leases on this edge node")
					s.kickAllLocalLeases()
				} else {
					slog.Info(fmt.Sprintf("[Edge Control] Kicking lease for subdomain %s", msg.Subdomain))
					s.registry.KickLease(msg.Subdomain)
				}
			case "lease_headers":
				slog.Info(fmt.Sprintf("[Edge Control] Updating custom headers for lease %s", msg.Subdomain))
				if err := s.registry.UpdateLeaseHeaders(msg.Subdomain, msg.Headers); err != nil {
					slog.Error(fmt.Sprintf("[Edge Control] Failed to update lease headers for %s: %v", msg.Subdomain, err))
				}
			}
		}

		ticker.Stop()
		_ = conn.Close() //nolint:errcheck
		lostAt = time.Now()
	}
}
