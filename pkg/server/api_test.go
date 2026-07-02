package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func setupTestServerForAPI(t *testing.T) *Server {
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.com"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
	}

	cfg.DBPath = filepath.Join(t.TempDir(), "api_test.db")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return srv
}

func TestServer_HandleMFAVerify(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Add a user with TOTP enabled
	user := &db.User{
		ID:          "mfa.user@example.com",
		Email:       "mfa.user@example.com",
		Role:        "developer",
		Status:      "approved",
		TOTPEnabled: true,
		TOTPSecret:  "JBSWY3DPEHPK3PXP", // A valid base32 secret
	}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// We can test a failure case to ensure it's parsing and enforcing correctly.
	reqBody := map[string]string{
		"temp_token": "some-invalid-token",
		"code":       "123456", // Deliberately wrong
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/mfa/verify", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.handleMFAVerify(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for bad TOTP code, got %d", w.Code)
	}
}

func TestServer_HandleAdminOverrideLimit(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Admin
	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin", Status: "approved"}
	_ = srv.db.CreateUser(admin)

	// Developer
	devLimit := 1
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved", MaxTunnels: &devLimit}
	_ = srv.db.CreateUser(dev)

	adminToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+adminToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"max_reservations": 5,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/admin/users/dev@example.com/limit", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	// Fake the session cookie
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: adminToken})

	w := httptest.NewRecorder()

	// Because of middleware (requireAdmin), we need to route it through the router
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	// Verify limit updated
	updatedDev, _ := srv.db.GetUser("dev@example.com")
	if updatedDev.MaxReservations == nil || *updatedDev.MaxReservations != 5 {
		val := "nil"
		if updatedDev.MaxReservations != nil {
			val = fmt.Sprintf("%d", *updatedDev.MaxReservations)
		}
		t.Errorf("expected MaxReservations to be 5, got %s", val)
	}
}

func TestServer_HandleUpdateReservationAccessControl(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Create user
	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev)

	// Create a reservation for the user
	_ = srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    dev.ID,
		Subdomain: "test-subdomain",
		Domain:    "example.com",
	})

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     dev.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	reqBody := map[string]interface{}{
		"subdomain":     "test-subdomain",
		"domain":        "example.com",
		"passcode":      "secret123",
		"whitelist_ips": "192.168.1.1,10.0.0.1",
		"access_mode":   "and",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/portal/reservations/access-control", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})

	w := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Fetch Reservation and verify
	res, err := srv.db.GetSubdomainReservationByName("test-subdomain", "example.com")
	if err != nil || res == nil {
		t.Fatalf("expected reservation, got error: %v", err)
	}

	if res.Passcode != "secret123" {
		t.Errorf("expected passcode 'secret123', got '%s'", res.Passcode)
	}
	if res.WhitelistIPs != "192.168.1.1,10.0.0.1" {
		t.Errorf("expected whitelist '192.168.1.1,10.0.0.1', got '%s'", res.WhitelistIPs)
	}
}
