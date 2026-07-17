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

func TestServer_MoreCoverage2(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleAdminBlacklist
	reqBody1 := map[string]interface{}{
		"ip":     "1.2.3.4",
		"reason": "Test",
	}
	bodyBytes1, _ := json.Marshal(reqBody1)
	req1, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/blacklist", bytes.NewBuffer(bodyBytes1))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleAdminBlacklist(w1, req1, admin.Email)

	// handleEdgeAuditLog
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/edge/audit?node_id=123", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleEdgeAuditLog(w2, req2)

	// handleEdgeKick
	reqBody3 := map[string]interface{}{
		"node_id":   "123",
		"subdomain": "test",
	}
	bodyBytes3, _ := json.Marshal(reqBody3)
	req3, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/edge/kick", bytes.NewBuffer(bodyBytes3))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleEdgeKick(w3, req3)

	// handleAdminExtendToken
	reqBody4 := map[string]interface{}{
		"days": 30,
	}
	bodyBytes4, _ := json.Marshal(reqBody4)
	req4, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/users/admin@example.com/tokens/1/extend", bytes.NewBuffer(bodyBytes4))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleAdminExtendToken(w4, req4, admin.Email)

	// handleAdminGetUser
	req5, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/users/admin@example.com", nil)
	req5.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w5 := httptest.NewRecorder()
	srv.handleAdminGetUser(w5, req5, admin.Email)
}
