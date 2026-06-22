package server

import (
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"net/url"
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
