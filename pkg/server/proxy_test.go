package server

import (
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	chserver "github.com/jpillora/chisel/server"
)

func TestProxyHandler_Offline(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true}) //nolint:errcheck //nolint:errcheck
	reg := NewRegistry(chiselServer)
	handler := NewProxyHandler(reg, config.DefaultServerConfig())

	req := httptest.NewRequest("GET", "http://unknown-se.liferay.com/web/guest/home", nil)
	req.Host = "unknown-se.liferay.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Environment Offline") {
		t.Error("expected body to contain 'Environment Offline'")
	}
	if !strings.Contains(body, "unknown-se.liferay.com") {
		t.Error("expected body to contain the requested host 'unknown-se.liferay.com'")
	}
}

func TestProxyHandler_Online(t *testing.T) {
	// 1. Create a dummy backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Liferay headers are correctly injected
		if r.Header.Get("X-Forwarded-Host") != "online-se.liferay.com" {
			t.Errorf("X-Forwarded-Host mismatch: got %s", r.Header.Get("X-Forwarded-Host"))
		}
		if r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("X-Forwarded-Proto mismatch: got %s", r.Header.Get("X-Forwarded-Proto"))
		}
		if r.Header.Get("X-Real-IP") == "" {
			t.Error("X-Real-IP header was not set")
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Hello from Liferay Local!")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer backend.Close()

	// Parse backend port
	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}
	backendPort, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("failed to parse backend port: %v", err)
	}

	// 2. Setup registry with a manual lease pointing to backend port
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true}) //nolint:errcheck
	reg := NewRegistry(chiselServer)

	reg.Lock()
	reg.leases["online-se.liferay.com"] = &TunnelLease{
		SubdomainPrefix: "online-se",
		FullHost:        "online-se.liferay.com",
		SessionToken:    "test-token",
		LocalPort:       backendPort,
		TargetPort:      8080,
		CreatedAt:       time.Now(),
	}
	reg.Unlock()

	// 3. Serve proxy request
	handler := NewProxyHandler(reg, config.DefaultServerConfig())
	req := httptest.NewRequest("GET", "http://online-se.liferay.com/web/guest", nil)
	req.Host = "online-se.liferay.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Body.String() != "Hello from Liferay Local!" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestProxyHandler_AccessControls(t *testing.T) {
	// 1. Setup temp DB
	tmpFile, err := os.CreateTemp("", "proxy-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close() //nolint:errcheck

	// Seed database: User & Reservation
	userID := "dev-user-id"
	_ = database.CreateUser(&db.User{ //nolint:errcheck
		ID:        userID,
		Email:     "dev@liferay.com",
		Role:      "user",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	reservation := &db.SubdomainReservation{
		UserID:       userID,
		Subdomain:    "protected-se",
		Domain:       "liferay.com",
		Passcode:     "secretpass",
		WhitelistIPs: "192.168.1.1,10.0.0.0/24",
		AccessMode:   "or",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	_ = database.CreateSubdomainReservation(reservation) //nolint:errcheck

	freePort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	// 2. Setup registry with lease
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true}) //nolint:errcheck
	reg := NewRegistry(chiselServer)

	reg.Lock()
	reg.leases["protected-se.liferay.com"] = &TunnelLease{
		UserID:          userID,
		SubdomainPrefix: "protected-se",
		FullHost:        "protected-se.liferay.com",
		SessionToken:    "test-token",
		LocalPort:       freePort,
		TargetPort:      freePort,
		CreatedAt:       time.Now(),
	}
	reg.Unlock()

	handler := NewProxyHandler(reg, config.DefaultServerConfig())
	handler.db = database

	// A. Test IP whitelist match allows bypass (OR mode)
	t.Run("IPWhitelist_Success", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://protected-se.liferay.com/home", nil)
		req.Host = "protected-se.liferay.com"
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("expected 502 Bad Gateway (bypassed protection), got %d", rec.Code)
		}
	})

	// B. Test IP whitelist check fails -> prompts for passcode
	t.Run("IPWhitelist_Fail_PromptsPasscode", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://protected-se.liferay.com/home", nil)
		req.Host = "protected-se.liferay.com"
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized (passcode prompt), got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Secure Access") {
			t.Errorf("expected body to show passcode prompt, got %s", rec.Body.String())
		}
	})

	// C. Test passcode submission POST /lfr-tunnel-verify
	t.Run("Passcode_Verification_Success", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://protected-se.liferay.com/lfr-tunnel-verify", strings.NewReader("passcode=secretpass&redirect_uri=/target"))
		req.Host = "protected-se.liferay.com"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("expected 303 Redirect, got %d", rec.Code)
		}

		cookies := rec.Result().Cookies()
		foundCookie := false
		for _, c := range cookies {
			if c.Name == "lfr_tunnel_session" {
				foundCookie = true
				if c.Value == "" {
					t.Error("cookie value is empty")
				}
			}
		}
		if !foundCookie {
			t.Error("expected session cookie to be set")
		}
	})
}

func TestProxyHandler_CustomHeaders(t *testing.T) {
	// 1. Create a dummy backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom headers are correctly injected
		if r.Header.Get("X-Forwarded-Host") != "custom-se.liferay.com" {
			t.Errorf("X-Forwarded-Host mismatch: got %s", r.Header.Get("X-Forwarded-Host"))
		}
		if r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("X-Forwarded-Proto mismatch: got %s", r.Header.Get("X-Forwarded-Proto"))
		}
		if r.Header.Get("X-Liferay-Custom") != "my-custom-value" {
			t.Errorf("X-Liferay-Custom mismatch: got %s", r.Header.Get("X-Liferay-Custom"))
		}
		if r.Header.Get("X-Lease-Custom") != "lease-value" {
			t.Errorf("X-Lease-Custom mismatch: got %s", r.Header.Get("X-Lease-Custom"))
		}
		if r.Header.Get("X-Client-IP") == "" {
			t.Error("X-Client-IP header was not set")
		}
		// Standard default header shouldn't be present since it is overridden
		if r.Header.Get("X-Real-IP") != "" {
			t.Errorf("X-Real-IP should not be present since it was overridden, got %s", r.Header.Get("X-Real-IP"))
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Hello from Custom Proxy!")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer backend.Close()

	// Parse backend port
	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}
	backendPort, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("failed to parse backend port: %v", err)
	}

	// 2. Setup registry with a manual lease pointing to backend port
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true}) //nolint:errcheck
	reg := NewRegistry(chiselServer)

	reg.Lock()
	reg.leases["custom-se.liferay.com"] = &TunnelLease{
		SubdomainPrefix: "custom-se",
		FullHost:        "custom-se.liferay.com",
		SessionToken:    "test-token",
		LocalPort:       backendPort,
		TargetPort:      8080,
		CreatedAt:       time.Now(),
		AddedHeaders: map[string]string{
			"X-Lease-Custom": "lease-value",
		},
	}
	reg.Unlock()

	// 3. Serve proxy request with custom headers config
	cfg := config.DefaultServerConfig()
	cfg.ProxyHeaders = map[string]string{
		"X-Forwarded-Host":  "$host",
		"X-Forwarded-Proto": "$proto",
		"X-Liferay-Custom":  "my-custom-value",
		"X-Client-IP":       "$client_ip",
	}

	handler := NewProxyHandler(reg, cfg)
	req := httptest.NewRequest("GET", "http://custom-se.liferay.com/web/guest", nil)
	req.Host = "custom-se.liferay.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Body.String() != "Hello from Custom Proxy!" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestPasscodeHashingAndConstantTimeVerification(t *testing.T) {
	pass := "my-secure-passcode"
	hashed := HashPasscode(pass)
	if hashed == "" {
		t.Error("expected non-empty hashed passcode")
	}
	if hashed == pass {
		t.Error("expected hashed passcode to not equal raw passcode")
	}

	// 1. Success matching hashed
	if !VerifyPasscode(pass, hashed) {
		t.Error("expected raw passcode to verify successfully against hashed passcode")
	}

	// 2. Success matching legacy plaintext (fallback)
	if !VerifyPasscode(pass, pass) {
		t.Error("expected raw passcode to verify successfully against legacy plaintext passcode")
	}

	// 3. Fails wrong passcode
	if VerifyPasscode("wrong-passcode", hashed) {
		t.Error("expected validation to fail for incorrect passcode")
	}

	// 4. Fails empty raw or hashed
	if VerifyPasscode("", hashed) {
		t.Error("expected validation to fail for empty raw passcode")
	}
	if VerifyPasscode(pass, "") {
		t.Error("expected validation to fail for empty hashed passcode")
	}
}
