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

func TestServer_AdminGetHandlers(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

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
		{"handleAdminGetUser", http.MethodGet, "http://example.com/api/admin/users/admin@example.com"},
		{"handleAdminListTokens", http.MethodGet, "http://example.com/api/admin/users/admin@example.com/tokens"},
		{"handleAdminListBackups", http.MethodGet, "http://example.com/api/admin/backups"},
		{"handleAdminBlacklist", http.MethodGet, "http://example.com/api/admin/blacklist"},
		{"handleAdminSettings", http.MethodGet, "http://example.com/api/admin/settings"},
		{"handleAdminListMagicLinks", http.MethodGet, "http://example.com/api/admin/magic-links"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.url, nil)
			req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
			w := httptest.NewRecorder()

			switch tc.name {
			case "handleAdminGetUser":
				srv.handleAdminGetUser(w, req, admin.Email)
			case "handleAdminListTokens":
				srv.handleAdminListTokens(w, req, admin.Email)
			case "handleAdminListBackups":
				srv.handleAdminListBackups(w, req, admin.Email)
			case "handleAdminBlacklist":
				srv.handleAdminBlacklist(w, req, admin.Email)
			case "handleAdminSettings":
				srv.handleAdminSettings(w, req, admin.Email)
			case "handleAdminListMagicLinks":
				srv.handleAdminListMagicLinks(w, req)
			}

			if w.Code == http.StatusInternalServerError {
				t.Errorf("%s: expected not 500, got %d", tc.name, w.Code)
			}
		})
	}
}

func TestServer_FallbackPages(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	testCases := []struct {
		name   string
		method string
		url    string
	}{
		{"handleVisitorMaintenancePage", http.MethodGet, "http://example.com/maintenance"},
		{"handlePrivacyFallback", http.MethodGet, "http://example.com/privacy"},
		{"handleCookiesFallback", http.MethodGet, "http://example.com/cookies"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, tc.url, nil)
			w := httptest.NewRecorder()

			switch tc.name {
			case "handleVisitorMaintenancePage":
				srv.handleVisitorMaintenancePage(w, req)
			case "handlePrivacyFallback":
				srv.handlePrivacyFallback(w, req)
			case "handleCookiesFallback":
				srv.handleCookiesFallback(w, req)
			}

			if w.Code == http.StatusInternalServerError {
				t.Errorf("%s: expected not 500, got %d", tc.name, w.Code)
			}
		})
	}
}

func TestServer_AdminPostHandlers(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// handleAdminTargetedMessage
	reqBody := map[string]interface{}{
		"user_id": "dev@example.com",
		"message": "hello",
		"level":   "info",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/targeted-message", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w := httptest.NewRecorder()
	srv.handleAdminTargetedMessage(w, req, admin.Email)
	if w.Code == http.StatusInternalServerError {
		t.Errorf("handleAdminTargetedMessage expected not 500")
	}

	// handleDismissMessage
	reqBody2 := map[string]interface{}{
		"type": "broadcast",
	}
	bodyBytes2, _ := json.Marshal(reqBody2)
	req2, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/dismiss-message", bytes.NewBuffer(bodyBytes2))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleDismissMessage(w2, req2)
	if w2.Code == http.StatusInternalServerError {
		t.Errorf("handleDismissMessage expected not 500")
	}
}
