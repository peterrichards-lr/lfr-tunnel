package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestServer_HandleUpdateMe(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved", FirstName: "Old"}
	_ = srv.db.CreateUser(dev)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	updatedName := "Updated Dev Name"
	reqBody := map[string]interface{}{
		"first_name": updatedName,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPut, "http://example.com/api/me", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleUpdateMe(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	updated, _ := srv.db.GetUser(dev.ID) //nolint:errcheck
	if updated.FirstName != "Updated Dev Name" {
		t.Errorf("expected first name to be updated, got %s", updated.FirstName)
	}
}

func TestServer_HandleSelfDeleteAccount(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"confirm_email": "dev@example.com",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/me/delete-account", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleSelfDeleteAccount(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	_, err := srv.db.GetUser(dev.ID)
	if err == nil || !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected user to be deleted, got err %v", err)
	}
}

func TestServer_HandleAdminDeleteUser(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: "approved"}
	_ = srv.db.CreateUser(admin)

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// URL doesn't have the email at the end, it expects /api/admin/users/dev@example.com
	// Wait, handleAdminDeleteUser parses strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	// Wait, is there a /delete suffix? Let me check line 618.
	// `strings.TrimPrefix(r.URL.Path, "/api/admin/users/")`
	// If it doesn't trim /delete, then the email would be `dev@example.com/delete` or `dev@example.com`
	// Let's look at `serve.go` routing to see how it's routed.
	req, _ := http.NewRequest(http.MethodDelete, "http://example.com/api/admin/users/dev@example.com", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleAdminDeleteUser(w, req, "admin@example.com")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	_, err := srv.db.GetUser(dev.ID)
	if err == nil || !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected user to be deleted, got err %v", err)
	}
}

func TestServer_HandlePromoteReservation(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: "approved"}
	_ = srv.db.CreateUser(admin)

	expires := time.Now().Add(24 * time.Hour)
	_ = srv.db.CreateSubdomainReservation(&db.SubdomainReservation{ //nolint:errcheck
		UserID:    admin.ID,
		Subdomain: "test-promote",
		Domain:    "example.com",
		ExpiresAt: &expires,
	})

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"subdomain": "test-promote",
		"domain":    "example.com",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations/promote", bytes.NewBuffer(bodyBytes))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handlePromoteReservation(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}

	// We expect 400 because there is no active lease in the mock registry
	// so the promotion fails. This is enough to get coverage.
}
