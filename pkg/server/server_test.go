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

func TestServer_Domains(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1: "example.se",
		Domain2: "example.online",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	req := httptest.NewRequest("GET", "http://example.se/api/domains", nil)
	req.Host = "example.se"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	var domains []string
	if err := json.NewDecoder(rec.Body).Decode(&domains); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(domains) != 2 || domains[0] != "example.se" || domains[1] != "example.online" {
		t.Errorf("expected [example.se, example.online], got %v", domains)
	}
}

func TestServer_CheckSubdomain(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1:   "example.com",
		AuthToken: "mysecret",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// 1. Missing token (Unauthorized)
	req := httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=alpha", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Bad token (Unauthorized)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=alpha&token=badsecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 3. Good token, missing subdomain (Bad Request)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	// 4. Good token, available subdomain
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=beta-dev&token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	var checkResp CheckSubdomainResponse
	if err := json.NewDecoder(rec.Body).Decode(&checkResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !checkResp.Available {
		t.Errorf("expected beta-dev to be available")
	}
	if len(checkResp.Suggestions) != 0 {
		t.Errorf("expected no suggestions for available subdomain, got %v", checkResp.Suggestions)
	}

	// 5. Good token, reserved subdomain (Unavailable)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=admin&token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&checkResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if checkResp.Available {
		t.Errorf("expected admin to be unavailable")
	}
	if len(checkResp.Suggestions) != 3 {
		t.Errorf("expected 3 suggestions for unavailable subdomain, got %d", len(checkResp.Suggestions))
	}
}
