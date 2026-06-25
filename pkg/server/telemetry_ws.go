package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"lfr-tunnel/pkg/db"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Secure by default, but allow all hosts for web portal dashboard connections
		return true
	},
}

type wsClient struct {
	server *Server
	conn   *websocket.Conn
	userID string
	role   string
	email  string
	send   chan []byte
}

func (c *wsClient) readPump() {
	defer func() {
		c.server.unregisterWSClient(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) registerWSClient(c *wsClient) {
	s.wsMutex.Lock()
	s.wsClients[c] = true
	s.wsMutex.Unlock()
}

func (s *Server) unregisterWSClient(c *wsClient) {
	s.wsMutex.Lock()
	if _, ok := s.wsClients[c]; ok {
		delete(s.wsClients, c)
		close(c.send)
	}
	s.wsMutex.Unlock()
}

// handleTelemetryWS upgrades connection and manages live telemetry streaming.
func (s *Server) handleTelemetryWS(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}

	client := &wsClient{
		server: s,
		conn:   conn,
		userID: user.ID,
		role:   user.Role,
		email:  user.Email,
		send:   make(chan []byte, 256),
	}

	s.registerWSClient(client)

	go client.writePump()
	go client.readPump()

	// Initial immediate push
	s.pushUserTelemetry(client)
}

func (s *Server) getUserTelemetryData(user *db.User, sessionToken string) map[string]interface{} {
	var activeLeases []map[string]interface{}
	if s.registry != nil {
		leases := s.registry.ListLeases()
		for _, l := range leases {
			if l.UserID == user.ID || user.Role == "admin" || user.Role == "owner" {
				var passcode, whitelistIPs, accessMode string
				if s.db != nil {
					parts := strings.SplitN(l.FullHost, ".", 2)
					if len(parts) == 2 {
						domain := parts[1]
						res, err := s.db.GetSubdomainReservationByName(l.SubdomainPrefix, domain)
						if err == nil && res != nil {
							passcode = res.Passcode
							whitelistIPs = res.WhitelistIPs
							accessMode = res.AccessMode
						}
					}
				}

				activeLeases = append(activeLeases, map[string]interface{}{
					"subdomain_prefix": l.SubdomainPrefix,
					"full_host":        l.FullHost,
					"status":           l.Status,
					"bytes_in":         atomic.LoadUint64(&l.BytesIn),
					"bytes_out":        atomic.LoadUint64(&l.BytesOut),
					"rate_limit":       l.RateLimit,
					"user_id":          l.UserID,
					"client_ip":        l.ClientIP,
					"created_at":       l.CreatedAt,
					"node_id":          l.NodeID,
					"visitor_ips":      l.GetActiveVisitorIPs(s.cfg.VisitorTimeout),
					"passcode":         passcode,
					"whitelist_ips":    whitelistIPs,
					"access_mode":      accessMode,
				})
			}
		}
	}

	s.edgeLeasesMu.Lock()
	for userID, userLeasesList := range s.edgeLeases {
		for _, el := range userLeasesList {
			if userID == user.ID || user.Role == "admin" || user.Role == "owner" {
				var passcode, whitelistIPs, accessMode string
				if s.db != nil {
					parts := strings.SplitN(el.FullHost, ".", 2)
					if len(parts) == 2 {
						domain := parts[1]
						res, err := s.db.GetSubdomainReservationByName(el.Subdomain, domain)
						if err == nil && res != nil {
							passcode = res.Passcode
							whitelistIPs = res.WhitelistIPs
							accessMode = res.AccessMode
						}
					}
				}

				activeLeases = append(activeLeases, map[string]interface{}{
					"subdomain_prefix": el.Subdomain,
					"full_host":        el.FullHost,
					"status":           "up",
					"bytes_in":         el.BytesIn,
					"bytes_out":        el.BytesOut,
					"rate_limit":       0,
					"user_id":          el.UserID,
					"client_ip":        el.ClientIP,
					"created_at":       el.CreatedAt,
					"node_id":          el.NodeID,
					"visitor_ips":      []string{},
					"passcode":         passcode,
					"whitelist_ips":    whitelistIPs,
					"access_mode":      accessMode,
				})
			}
		}
	}
	s.edgeLeasesMu.Unlock()

	resp := map[string]interface{}{
		"id":                  user.ID,
		"email":               user.Email,
		"first_name":          user.FirstName,
		"last_name":           user.LastName,
		"preferred_name":      user.PreferredName,
		"role":                user.Role,
		"status":              user.Status,
		"timezone":            user.Timezone,
		"auth_method":         user.AuthMethod,
		"theme_preference":    user.ThemePreference,
		"language_preference": user.LanguagePreference,
		"notification_prefs":  user.NotificationPrefs,
		"last_login_ip":       user.LastLoginIP,
		"tunnels":             activeLeases,
		"totp_enabled":        user.TOTPEnabled,
		"last_client_version": user.LastClientVersion,
		"last_client_os":      user.LastClientOS,
	}

	if sessionToken != "" {
		if sessionRaw, ok := s.portalMap.Load("admin_session_" + sessionToken); ok {
			if sessionData, ok := sessionRaw.(PortalSessionData); ok {
				if sessionData.PreviousLoginAt != nil {
					resp["last_login_at"] = *sessionData.PreviousLoginAt
				}
				if sessionData.KilledPreviousSession {
					resp["killed_previous_session"] = true
					// Unset it so the UI only alerts once
					sessionData.KilledPreviousSession = false
					s.portalMap.Store("admin_session_"+sessionToken, sessionData)
				}
			}
		}
	}

	s.broadcastMutex.RLock()
	resp["broadcast_message"] = s.broadcastMessage
	s.broadcastMutex.RUnlock()

	s.targetedMutex.RLock()
	if tm, ok := s.targetedMessages[user.ID]; ok && tm != "" {
		resp["targeted_message"] = tm
	}
	s.targetedMutex.RUnlock()

	s.maintMutex.RLock()
	isMaint := s.maintenanceMode
	maintScheduled := s.maintScheduledAt
	s.maintMutex.RUnlock()

	maintModeStr := "false"
	var secondsLeft int
	if isMaint {
		maintModeStr = "true"
	} else if !maintScheduled.IsZero() && time.Now().Before(maintScheduled) {
		maintModeStr = "pending"
		secondsLeft = int(time.Until(maintScheduled).Seconds())
	}
	resp["maintenance_mode"] = maintModeStr
	if maintModeStr == "pending" {
		resp["maintenance_seconds_left"] = secondsLeft
	}

	resp["iron_curtain"] = s.isNginxMaintenanceActive()

	return resp
}

func (s *Server) pushUserTelemetry(c *wsClient) {
	var u *db.User
	var err error
	if s.db != nil {
		u, err = s.db.GetUserByEmail(c.email)
	}
	if err != nil || u == nil {
		// Fallback for owner / artificial user
		if s.cfg.Owner.UserID != "" && strings.EqualFold(c.email, s.cfg.Owner.UserID) {
			u = &db.User{
				ID:        s.cfg.Owner.UserID,
				Email:     c.email,
				FirstName: s.cfg.Owner.Name,
				Role:      "owner",
			}
		} else {
			return
		}
	}

	data := s.getUserTelemetryData(u, "")
	payload := map[string]interface{}{
		"type": "telemetry",
		"data": data,
	}

	msg, err := json.Marshal(payload)
	if err != nil {
		return
	}

	select {
	case c.send <- msg:
	default:
		// Client is slow, drop connection
		_ = c.conn.Close()
	}
}

// BroadcastTelemetry triggers immediate push to all websockets.
func (s *Server) BroadcastTelemetry() {
	s.wsMutex.RLock()
	defer s.wsMutex.RUnlock()
	for client := range s.wsClients {
		go s.pushUserTelemetry(client)
	}
}

// PushUserTelemetryByID triggers telemetry push to a specific user.
func (s *Server) PushUserTelemetryByID(userID string) {
	s.wsMutex.RLock()
	defer s.wsMutex.RUnlock()
	for client := range s.wsClients {
		if client.userID == userID {
			go s.pushUserTelemetry(client)
		}
	}
}

// StartTelemetryTicker starts the periodic broadcast loop.
func (s *Server) StartTelemetryTicker() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.BroadcastTelemetry()
			}
		}
	}()
}
