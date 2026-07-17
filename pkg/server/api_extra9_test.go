package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"lfr-tunnel/pkg/db"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServer_SSO(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// handleSSOLogin
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/sso/login?provider=google", nil)
	w := httptest.NewRecorder()
	srv.handleSSOLogin(w, req)

	// handleSSOCallback
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/api/sso/callback", nil)
	w2 := httptest.NewRecorder()
	srv.handleSSOCallback(w2, req2)
}

func TestServer_EdgeAction(t *testing.T) {
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
		"node_id": "edge-1",
		"action":  "restart",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/edge/action", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleEdgeAction(w, req)
}

func TestServer_APIErrors(t *testing.T) {
	w := httptest.NewRecorder()
	respondWithError(w, errors.New("Test Error"))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}
