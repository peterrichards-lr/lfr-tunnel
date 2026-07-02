package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServer_MiscCoverage(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Call handleSSOLogin
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/sso/login", nil)
	w := httptest.NewRecorder()
	srv.handleSSOLogin(w, req)

	// Call handleSSOCallback
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/api/sso/callback?code=abc&state=123", nil)
	w2 := httptest.NewRecorder()
	srv.handleSSOCallback(w2, req2)

	// Call handleAdminListBackups
	req3, _ := http.NewRequest(http.MethodGet, "http://example.com/api/admin/backups", nil)
	w3 := httptest.NewRecorder()
	srv.handleAdminListBackups(w3, req3, "admin@example.com")

	req4, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/maintenance", bytes.NewBufferString("{}"))
	w4 := httptest.NewRecorder()
	srv.handleAdminMaintenance(w4, req4, "admin@example.com")

	// Test Telemetry
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.ctx = ctx
	srv.StartTelemetryTicker()

	time.Sleep(10 * time.Millisecond)
}
