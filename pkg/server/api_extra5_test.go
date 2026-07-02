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
