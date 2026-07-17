package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestServer_HandleDeleteToken(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Developer
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev) //nolint:errcheck

	// Create token
	expires := time.Now().Add(24 * time.Hour)
	pat := &db.PersonalAccessToken{
		UserID:      dev.ID,
		Name:        "Test Token",
		TokenHash:   "dummyhash",
		TokenPrefix: "lfr_pat_dummy",
		ExpiresAt:   &expires,
	}
	_ = srv.db.CreatePAT(pat) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://example.com/api/tokens/%d", pat.ID), nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleDeleteToken(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Verify deleted
	tokens, _ := srv.db.ListPATs(dev.ID) //nolint:errcheck
	for _, tk := range tokens {
		if tk.ID == pat.ID && tk.RevokedAt == nil {
			t.Errorf("token should have been deleted (revoked), but it is not")
		}
	}
}

func TestServer_HandleGenerateSubdomain(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Developer
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev) //nolint:errcheck

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/portal/generate-subdomain", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleGenerateSubdomain(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	if resp["subdomain"] == "" {
		t.Errorf("expected generated subdomain, got empty")
	}
}

func TestServer_HandleListTokens(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Developer
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev) //nolint:errcheck

	expires := time.Now().Add(24 * time.Hour)
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{ //nolint:errcheck
		UserID:      dev.ID,
		Name:        "Token 1",
		TokenHash:   "hash1",
		TokenPrefix: "lfr_pat_hash1",
		ExpiresAt:   &expires,
	})

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/tokens", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleListTokens(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	var resp []db.PersonalAccessToken
	_ = json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	if len(resp) == 0 {
		t.Errorf("expected tokens, got 0")
	}
}

func TestServer_HandleDeleteReservation(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev) //nolint:errcheck

	_ = srv.db.CreateSubdomainReservation(&db.SubdomainReservation{ //nolint:errcheck
		UserID:    dev.ID,
		Subdomain: "test-delete",
		Domain:    "example.com",
	})

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"domain": "example.com",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	createdRes, _ := srv.db.GetSubdomainReservationByName("test-delete", "example.com") //nolint:errcheck
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://example.com/api/portal/reservations/%d", createdRes.ID), bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleDeleteReservation(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Verify
	res, _ := srv.db.GetSubdomainReservationByName("test-delete", "example.com") //nolint:errcheck
	if res != nil {
		t.Errorf("reservation should be deleted")
	}
}
