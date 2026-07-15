package server

import (
	"bytes"
	"encoding/json"
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

func TestServer_AdminConfigView(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	srv.cfg.SMTPServer.Password = "supersecretpassword123"
	srv.cfg.Webhooks.SlackURL = "https://hooks.slack.com/services/T00/B00/X00"
	srv.cfg.EdgeToken = "token-hash-secret"

	// 1. Role: owner - should succeed (allowed role defaults to owner)
	req1, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/config-view", nil)
	w1 := httptest.NewRecorder()
	srv.handleAdminConfigView(w1, req1, "owner@example.com", "owner")
	if w1.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w1.Code)
	}

	var res1 map[string]interface{}
	if err := json.Unmarshal(w1.Body.Bytes(), &res1); err != nil {
		t.Fatalf("failed to unmarshal config-view response: %v", err)
	}

	// Verify masking
	smtp, ok := res1["SMTPServer"].(map[string]interface{})
	if !ok {
		t.Fatalf("SMTPServer field is missing or not a map")
	}
	if smtp["Password"] != "[MASKED]" {
		t.Errorf("expected SMTPServer.Password to be [MASKED], got %v", smtp["Password"])
	}

	webhooks, ok := res1["Webhooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("Webhooks field is missing or not a map")
	}
	if webhooks["SlackURL"] != "[MASKED]" {
		t.Errorf("expected Webhooks.SlackURL to be [MASKED], got %v", webhooks["SlackURL"])
	}

	if res1["EdgeToken"] != "[MASKED]" {
		t.Errorf("expected EdgeToken to be [MASKED], got %v", res1["EdgeToken"])
	}

	// 2. Role: admin - should fail by default (unauthorized role)
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/config-view", nil)
	w2 := httptest.NewRecorder()
	srv.handleAdminConfigView(w2, req2, "admin@example.com", "admin")
	if w2.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w2.Code)
	}

	// 3. Configure view_config_allowed_role to admin and verify admin now succeeds
	srv.cfg.ViewConfigAllowedRole = "admin"
	w3 := httptest.NewRecorder()
	srv.handleAdminConfigView(w3, req2, "admin@example.com", "admin")
	if w3.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w3.Code)
	}
}
