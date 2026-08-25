package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"lfr-tunnel/pkg/db"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (s *Server) writeAudit(actorID, action, targetType, targetID string, details string, r *http.Request) {
	// r.RemoteAddr is the wrong answer here and always has been (#1357). The gateway binds
	// loopback and nginx proxies to it, so RemoteAddr is 127.0.0.1 with an ephemeral port on
	// every single request -- which is exactly what the production audit log was full of: 4,896
	// of 4,951 rows. clientIP resolves the address the visitor actually came from, honouring the
	// forwarding headers only when the hop that set them is trusted.
	ip := ""
	if r != nil {
		ip = s.clientIP(r)
	}

	if s.db == nil {
		if s.cfg.ControlPlaneURL != "" {
			go s.forwardAuditToControlPlane(actorID, action, targetType, targetID, details, ip)
		}
		return
	}
	entry := &db.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    details,
		IPAddress:  ip,
	}
	dbConn := s.db
	// Run in a goroutine so it doesn't block the HTTP response
	go func() {
		if err := dbConn.WriteAuditEntry(entry); err != nil {
			if !strings.Contains(err.Error(), "database is closed") {
				slog.Info(fmt.Sprintf("[Server] Failed to write audit log: %v", err))
			}
		}
	}()
}

func (s *Server) forwardAuditToControlPlane(actorID, action, targetType, targetID, details, ip string) {
	client := &http.Client{Timeout: 5 * time.Second}
	payloadBytes, err := json.Marshal(map[string]string{
		"actor_id":    actorID,
		"action":      action,
		"target_type": targetType,
		"target_id":   targetID,
		"details":     details,
		"ip_address":  ip,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", s.cfg.ControlPlaneURL+"/api/internal/edge-audit-log", bytes.NewReader(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server Edge] Failed to forward audit log to control plane: %v", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck
}
