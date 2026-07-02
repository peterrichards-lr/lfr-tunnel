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

func TestServer_MoreCoverage3(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test GET with filters on List Tokens
	req1, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/users/admin@example.com/tokens?sort=created_at&order=desc", nil)
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleAdminListTokens(w1, req1, admin.Email)

	// handleAdminGetUser
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/users/admin@example.com?stats=true", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleAdminGetUser(w2, req2, admin.Email)

	// handleAdminPatchUser
	reqBody3 := map[string]interface{}{
		"status": "pending",
	}
	bodyBytes3, _ := json.Marshal(reqBody3)
	req3, _ := http.NewRequest(http.MethodPatch, "http://example.com/api/admin/users/admin@example.com", bytes.NewBuffer(bodyBytes3))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleAdminPatchUser(w3, req3, admin.Email, admin.Email)

	// ServeHTTP options
	req4, _ := http.NewRequest(http.MethodOptions, "http://example.com/api/test", nil)
	w4 := httptest.NewRecorder()
	srv.ServeHTTP(w4, req4)

	// PromoteReservation
	reqBody5 := map[string]interface{}{
		"subdomain": "test",
		"domain":    "example.com",
	}
	bodyBytes5, _ := json.Marshal(reqBody5)
	req5, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations/test/promote", bytes.NewBuffer(bodyBytes5))
	req5.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w5 := httptest.NewRecorder()
	srv.handlePromoteReservation(w5, req5)

	// MFA Verify
	reqBody6 := map[string]interface{}{
		"code": "123456",
	}
	bodyBytes6, _ := json.Marshal(reqBody6)
	req6, _ := http.NewRequest(http.MethodPost, "http://example.com/api/mfa/verify", bytes.NewBuffer(bodyBytes6))
	req6.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w6 := httptest.NewRecorder()
	srv.handleMFAVerify(w6, req6)
}
