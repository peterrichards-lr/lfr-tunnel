package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"lfr-tunnel/pkg/db"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) writeAudit(actorID, action, targetType, targetID string, details string, r *http.Request) {
	if s.db == nil {
		if s.cfg.ControlPlaneURL != "" {
			ip := ""
			if r != nil {
				ip = r.RemoteAddr
			}
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
		IPAddress:  r.RemoteAddr,
	}
	dbConn := s.db
	// Run in a goroutine so it doesn't block the HTTP response
	go func() {
		if err := dbConn.WriteAuditEntry(entry); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to write audit log: %v", err))
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
