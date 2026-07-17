package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestServer_MiscCoverage5(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleAdminOverrideTunnelsLimit
	reqBody := map[string]interface{}{
		"limit": 10,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/users/admin@example.com/tunnels-limit", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w := httptest.NewRecorder()
	srv.handleAdminOverrideTunnelsLimit(w, req, admin.Email)

	// handleEdgeAction
	reqBody2 := map[string]interface{}{
		"action":  "restart",
		"node_id": "test-edge-1",
	}
	bodyBytes2, _ := json.Marshal(reqBody2)
	req2, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/edge/action", bytes.NewBuffer(bodyBytes2))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleEdgeAction(w2, req2)

	// handleAdminDeleteUser
	req3, _ := http.NewRequest(http.MethodDelete, "http://example.com/api/admin/users/admin@example.com", nil)
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleAdminDeleteUser(w3, req3, admin.Email)

	// handleTunnelStatus
	req4, _ := http.NewRequest(http.MethodPost, "http://example.com/api/tunnels/status", bytes.NewBufferString("{}"))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleTunnelStatus(w4, req4)
}
