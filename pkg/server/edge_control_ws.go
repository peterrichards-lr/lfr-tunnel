package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"lfr-tunnel/pkg/config"
)

// ControlMessage represents the JSON schema for websocket communication.
type ControlMessage struct {
	Type      string `json:"type"`
	Nonce     string `json:"nonce,omitempty"`
	Response  string `json:"response,omitempty"`
	IP        string `json:"ip,omitempty"`
	Action    string `json:"action,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	Subdomain string `json:"subdomain,omitempty"`
}

// handleEdgeControlWS handles control plane WebSocket connections from Edge nodes.
func (s *Server) handleEdgeControlWS(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		http.Error(w, "missing node_id", http.StatusBadRequest)
		return
	}
	version := r.URL.Query().Get("version")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Edge WS] Failed to upgrade WebSocket: %v", err)
		return
	}

	// 1. Generate challenge nonce
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	nonceStr := hex.EncodeToString(nonce)

	challenge := ControlMessage{
		Type:  "challenge",
		Nonce: nonceStr,
	}

	if err := conn.WriteJSON(challenge); err != nil {
		log.Printf("[Edge WS] Failed to send challenge to %s: %v", nodeID, err)
		_ = conn.Close()
		return
	}

	// 2. Wait for auth response
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var authMsg ControlMessage
	if err := conn.ReadJSON(&authMsg); err != nil {
		log.Printf("[Edge WS] Failed to read auth message from %s: %v", nodeID, err)
		_ = conn.Close()
		return
	}

	if authMsg.Type != "auth" {
		log.Printf("[Edge WS] Expected auth message from %s, got %s", nodeID, authMsg.Type)
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unexpected message type"})
		_ = conn.Close()
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
		log.Printf("[Edge WS] Unknown edge node ID: %s", nodeID)
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "unknown node_id"})
		_ = conn.Close()
		return
	}

	keyBytes, err := hex.DecodeString(nodeConfig.TokenHash)
	if err != nil {
		log.Printf("[Edge WS] Invalid token hash configured for %s", nodeID)
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "invalid token hash"})
		_ = conn.Close()
		return
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(nonceStr))
	expectedMAC := mac.Sum(nil)

	respMAC, err := hex.DecodeString(authMsg.Response)
	if err != nil || subtle.ConstantTimeCompare(respMAC, expectedMAC) != 1 {
		log.Printf("[Edge WS] HMAC verification failed for %s", nodeID)
		_ = conn.WriteJSON(ControlMessage{Type: "auth_failed", Reason: "invalid signature"})
		_ = conn.Close()
		return
	}

	// Reset read deadline
	_ = conn.SetReadDeadline(time.Time{})

	// Authenticated! Register edge client
	s.edgeClientsMu.Lock()
	if oldConn, exists := s.edgeClients[nodeID]; exists {
		_ = oldConn.WriteJSON(ControlMessage{Type: "replaced", Reason: "new connection established"})
		_ = oldConn.Close()
	}
	s.edgeClients[nodeID] = conn
	if version != "" {
		s.edgeVersions[nodeID] = version
	} else {
		s.edgeVersions[nodeID] = "Unknown"
	}
	s.edgeClientsMu.Unlock()

	log.Printf("[Edge WS] Edge node %s successfully authenticated.", nodeID)
	_ = conn.WriteJSON(ControlMessage{Type: "auth_success"})

	// Start read pump to keep alive and detect disconnects
	go func() {
		defer func() {
			s.edgeClientsMu.Lock()
			if activeConn, exists := s.edgeClients[nodeID]; exists && activeConn == conn {
				delete(s.edgeClients, nodeID)
				delete(s.edgeVersions, nodeID)
			}
			s.edgeClientsMu.Unlock()
			_ = conn.Close()
			log.Printf("[Edge WS] Edge node %s disconnected.", nodeID)
		}()

		// Set read limit and pong handler
		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
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
		_ = conn.WriteMessage(websocket.TextMessage, payload)
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
		_ = conn.WriteMessage(websocket.TextMessage, payload)
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
		log.Printf("[Edge Control] Terminating lease for %s", l.FullHost)
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
			log.Printf("[Edge Control] Invalid ControlPlaneURL: %v", err)
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

		log.Printf("[Edge Control] Connecting to Control Plane at %s...", wsURL)

		dialer := websocket.DefaultDialer
		dialer.HandshakeTimeout = 5 * time.Second

		if s.cfg.InsecureSkipVerify {
			dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}

		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[Edge Control] Connection failed: %v", err)
			if lostAt.IsZero() {
				lostAt = time.Now()
			} else if time.Since(lostAt) > 3*time.Minute {
				log.Printf("[Edge Control] Connection lost for >3 minutes. Terminating all active tunnels...")
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
			log.Printf("[Edge Control] Failed to read challenge: %v", err)
			_ = conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		if challengeMsg.Type != "challenge" {
			log.Printf("[Edge Control] Expected challenge message, got %s", challengeMsg.Type)
			_ = conn.Close()
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
			log.Printf("[Edge Control] Failed to send auth response: %v", err)
			_ = conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		// 3. Receive auth result
		var authResult ControlMessage
		if err := conn.ReadJSON(&authResult); err != nil {
			log.Printf("[Edge Control] Failed to read auth result: %v", err)
			_ = conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		if authResult.Type != "auth_success" {
			log.Printf("[Edge Control] Authentication failed: %s", authResult.Reason)
			_ = conn.Close()
			time.Sleep(10 * time.Second)
			continue
		}

		log.Printf("[Edge Control] Successfully connected and authenticated with Control Plane.")

		// Start ticker to send ping messages
		ticker := time.NewTicker(30 * time.Second)
		pingErrChan := make(chan error, 1)

		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
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
			_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))

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
				log.Printf("[Edge Control] Connection closed or read failed: %v", readErr)
				break
			}

			switch msg.Type {
			case "restart":
				log.Printf("[Edge Control] Restart request received from Control Plane. Exiting...")
				os.Exit(1)
			case "blacklist_update":
				switch msg.Action {
				case "add":
					log.Printf("[Edge Control] Blacklisting IP: %s", msg.IP)
					s.blacklist.Store(msg.IP, true)
				case "remove":
					log.Printf("[Edge Control] Unblacklisting IP: %s", msg.IP)
					s.blacklist.Delete(msg.IP)
				}
			case "maintenance_trigger":
				s.maintMutex.Lock()
				switch msg.Action {
				case "enable":
					log.Printf("[Edge Control] Maintenance enabled: %s (duration: %d mins)", msg.Reason, msg.Duration)
					s.maintenanceMode = true
					s.kickAllLocalLeases()
				case "disable":
					log.Printf("[Edge Control] Maintenance disabled.")
					s.maintenanceMode = false
				}
				s.maintMutex.Unlock()
			case "lease_kick":
				if msg.Subdomain == "*" || msg.Subdomain == "" {
					log.Printf("[Edge Control] Kicking ALL leases on this edge node")
					s.kickAllLocalLeases()
				} else {
					log.Printf("[Edge Control] Kicking lease for subdomain %s", msg.Subdomain)
					s.registry.KickLease(msg.Subdomain)
				}
			}
		}

		ticker.Stop()
		_ = conn.Close()
		lostAt = time.Now()
	}
}
