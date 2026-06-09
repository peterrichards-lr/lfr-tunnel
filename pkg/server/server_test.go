package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

func TestServer_Register(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1:   "example.com",
		AuthToken: "mysecret",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// 1. Unauthorized registration
	badPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "alpha",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "wrong-secret",
	})
	req := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(badPayload))
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Successful registration
	goodPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "alpha",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "mysecret",
	})
	req2 := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(goodPayload))
	req2.Host = "example.com"
	rec2 := httptest.NewRecorder()

	srv.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec2.Code)
	}

	var resp RegisterResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if resp.SessionToken == "" {
		t.Error("expected non-empty session token")
	}
	if len(resp.Remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(resp.Remotes))
	}
}

func TestServer_ControlWelcomePage(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1: "example.com",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Liferay Tunnel Gateway") {
		t.Error("expected welcome landing page content")
	}
}
