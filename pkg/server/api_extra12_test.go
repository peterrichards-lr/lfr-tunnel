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

func TestServer_AdminGetHandlers2(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	testCases := []struct {
		name   string
		method string
		url    string
	}{
		{"handleAdminAuditLog", http.MethodGet, "http://example.com/api/admin/audit"},
		{"handleAdminGetUptimeHistory", http.MethodGet, "http://example.com/api/admin/uptime"},
		{"handleAdminEndpoints", http.MethodGet, "http://example.com/api/admin/endpoints"},
		{"handleAdminDeleteToken", http.MethodDelete, "http://example.com/api/admin/users/admin@example.com/tokens/1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.url, nil)
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
			w := httptest.NewRecorder()

			switch tc.name {
			case "handleAdminAuditLog":
				srv.handleAdminAuditLog(w, req, admin.Email)
			case "handleAdminGetUptimeHistory":
				srv.handleAdminGetUptimeHistory(w, req, admin.Email)
			case "handleAdminEndpoints":
				srv.handleAdminEndpoints(w, req)
			case "handleAdminDeleteToken":
				srv.handleAdminDeleteToken(w, req, admin.Email)
			}

			if w.Code == http.StatusInternalServerError {
				t.Errorf("%s: expected not 500, got %d", tc.name, w.Code)
			}
		})
	}
}

func TestServer_AdminOverrideRateLimit(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"subdomain": "test",
		"limit":     100,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/rate-limit", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleAdminOverrideRateLimit(w, req, admin.Email)
}
