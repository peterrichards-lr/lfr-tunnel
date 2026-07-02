package server

import (
	"bytes"
	"encoding/json"
	"lfr-tunnel/pkg/db"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServer_MoreCoverage(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleCreateToken
	reqBody1 := map[string]interface{}{
		"name":       "MyToken",
		"expires_at": "2030-01-01",
	}
	bodyBytes1, _ := json.Marshal(reqBody1)
	req1, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/tokens", bytes.NewBuffer(bodyBytes1))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleCreateToken(w1, req1)

	// handleAdminPatchUser
	reqBody2 := map[string]interface{}{
		"role":   "admin",
		"status": "approved",
	}
	bodyBytes2, _ := json.Marshal(reqBody2)
	req2, _ := http.NewRequest(http.MethodPatch, "http://example.com/api/admin/users/admin@example.com", bytes.NewBuffer(bodyBytes2))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleAdminPatchUser(w2, req2, admin.Email, admin.Email)

	// handleRegisterRequest
	reqBody3 := map[string]interface{}{
		"email":   "newuser@example.com",
		"company": "Liferay",
		"reason":  "Test",
	}
	bodyBytes3, _ := json.Marshal(reqBody3)
	req3, _ := http.NewRequest(http.MethodPost, "http://example.com/api/auth/register", bytes.NewBuffer(bodyBytes3))
	w3 := httptest.NewRecorder()
	srv.handleRegisterRequest(w3, req3)

	// handleCSRSignInvitation
	reqBody4 := map[string]interface{}{
		"csr_pem": "test",
	}
	bodyBytes4, _ := json.Marshal(reqBody4)
	req4, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/invitations/1/sign", bytes.NewBuffer(bodyBytes4))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleCSRSignInvitation(w4, req4)
}
