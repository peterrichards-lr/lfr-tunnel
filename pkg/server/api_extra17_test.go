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

func TestServer_MiscCoverage4(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleVerifyEmail
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/auth/verify?token=123", nil)
	w := httptest.NewRecorder()
	srv.handleVerifyEmail(w, req)

	// handleDeleteInvitation
	req2, _ := http.NewRequest(http.MethodDelete, "http://example.com/api/portal/invitations/abc", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleDeleteInvitation(w2, req2)

	// handleUpdateReservationAccessControl
	reqBody3 := map[string]interface{}{
		"subdomain": "test",
		"access_control": map[string]interface{}{
			"enabled": true,
		},
	}
	bodyBytes3, _ := json.Marshal(reqBody3)
	req3, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations/test/access-control", bytes.NewBuffer(bodyBytes3))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w3, req3)

	// handleUpdateReservationHeaders
	reqBody4 := map[string]interface{}{
		"subdomain": "test",
		"headers":   []map[string]interface{}{},
	}
	bodyBytes4, _ := json.Marshal(reqBody4)
	req4, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations/test/headers", bytes.NewBuffer(bodyBytes4))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleUpdateReservationHeaders(w4, req4)

	// handleAdminGetUser -> pass user with magic links
	_ = srv.db.CreateMagicLink(admin.Email, "link-123", "127.0.0.1", time.Now().Add(time.Hour)) //nolint:errcheck
	req5, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/users/admin@example.com?magic_links=true", nil)
	req5.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w5 := httptest.NewRecorder()
	srv.handleAdminGetUser(w5, req5, admin.Email)
}
