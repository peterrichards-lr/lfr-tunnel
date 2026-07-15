package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_HandleTunnelStatus(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/tunnel/status", bytes.NewBufferString("{}"))
	w := httptest.NewRecorder()

	srv.handleTunnelStatus(w, req)

	// Since we mock, it should just return 200 or 401
	if w.Code == http.StatusInternalServerError {
		t.Errorf("expected not 500")
	}
}

func TestServer_HandleSetupPage(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/setup", bytes.NewBufferString("{}"))
	w := httptest.NewRecorder()

	srv.handleSetupPage(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("expected not 500")
	}
}

func TestServer_HandleVerifyEmail(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/verify?token=123", bytes.NewBufferString("{}"))
	w := httptest.NewRecorder()

	srv.handleVerifyEmail(w, req)

	// Will likely be bad request or redirect, but we just want coverage
	if w.Code == http.StatusInternalServerError {
		t.Errorf("expected not 500")
	}
}

func TestServer_HandleAuthProviders(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/auth/providers", bytes.NewBufferString("{}"))
	w := httptest.NewRecorder()

	srv.handleAuthProviders(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("expected not 500")
	}
}

func TestServer_AdminTestWebhook(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// 1. First request should succeed (HTTP 200)
	req1, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/test-webhook", nil)
	w1 := httptest.NewRecorder()
	srv.handleAdminTestWebhook(w1, req1, "admin@example.com")
	if w1.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w1.Code)
	}

	// 2. Second immediate request from same admin should be rate-limited (HTTP 429)
	req2, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/test-webhook", nil)
	w2 := httptest.NewRecorder()
	srv.handleAdminTestWebhook(w2, req2, "admin@example.com")
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", w2.Code)
	}

	// 3. Request from a different admin should succeed (HTTP 200)
	req3, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/test-webhook", nil)
	w3 := httptest.NewRecorder()
	srv.handleAdminTestWebhook(w3, req3, "other-admin@example.com")
	if w3.Code != http.StatusOK {
		t.Errorf("expected status 200 for different admin, got %d", w3.Code)
	}
}
