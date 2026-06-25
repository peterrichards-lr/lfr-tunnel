package server

import (
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jpillora/chisel/server"
)

func TestProxyHandler_Offline(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
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
		_, _ = w.Write([]byte("Hello from Liferay Local!"))
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
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
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
	_ = database.CreateUser(&db.User{
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
	_ = database.CreateSubdomainReservation(reservation)

	// 2. Setup registry with lease
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)

	reg.Lock()
	reg.leases["protected-se.liferay.com"] = &TunnelLease{
		UserID:          userID,
		SubdomainPrefix: "protected-se",
		FullHost:        "protected-se.liferay.com",
		SessionToken:    "test-token",
		LocalPort:       8080,
		TargetPort:      8080,
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
