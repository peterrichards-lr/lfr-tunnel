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

func TestServer_CoveragePathValues(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleAdminDeleteToken
	req1, _ := http.NewRequest(http.MethodDelete, "http://example.com/api/admin/users/admin@example.com/tokens/1", nil)
	req1.SetPathValue("email", "admin@example.com")
	req1.SetPathValue("id", "1")
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleAdminDeleteToken(w1, req1, admin.Email)

	// handleAdminExtendToken
	reqBody2 := map[string]interface{}{
		"days": 30,
	}
	bodyBytes2, _ := json.Marshal(reqBody2)
	req2, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/users/admin@example.com/tokens/1/extend", bytes.NewBuffer(bodyBytes2))
	req2.SetPathValue("email", "admin@example.com")
	req2.SetPathValue("id", "1")
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleAdminExtendToken(w2, req2, admin.Email)

	// handleAdminPatchUser
	reqBody3 := map[string]interface{}{
		"status": "approved",
		"role":   "developer",
	}
	bodyBytes3, _ := json.Marshal(reqBody3)
	req3, _ := http.NewRequest(http.MethodPatch, "http://example.com/api/admin/users/admin@example.com", bytes.NewBuffer(bodyBytes3))
	req3.SetPathValue("email", "admin@example.com")
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleAdminPatchUser(w3, req3, admin.Email, admin.Email)

	// handleAdminDeleteUser
	req4, _ := http.NewRequest(http.MethodDelete, "http://example.com/api/admin/users/admin@example.com", nil)
	req4.SetPathValue("email", "admin@example.com")
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleAdminDeleteUser(w4, req4, admin.Email)
}
