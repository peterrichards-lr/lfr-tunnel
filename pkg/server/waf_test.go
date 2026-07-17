package server

import (
	"bytes"
	"lfr-tunnel/pkg/config"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	chserver "github.com/jpillora/chisel/server"
)

func TestWAF_RuleScanning(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		urlPath     string
		queryParams string
		headers     map[string]string
		body        string
		isBlocked   bool
		category    string
	}{
		{
			name:      "Clean GET request",
			method:    "GET",
			urlPath:   "/web/guest/home",
			isBlocked: false,
		},
		{
			name:      "Path Traversal in URL Path",
			method:    "GET",
			urlPath:   "/something/../../etc/passwd",
			isBlocked: true,
			category:  "Path Traversal",
		},
		{
			name:      "Path Traversal with Backslashes",
			method:    "GET",
			urlPath:   `/windows\win.ini`,
			isBlocked: true,
			category:  "Path Traversal",
		},
		{
			name:        "SQL Injection in Query Param",
			method:      "GET",
			urlPath:     "/api/users",
			queryParams: "id=1' UNION SELECT username, password FROM users --",
			isBlocked:   true,
			category:    "SQL Injection",
		},
		{
			name:        "XSS in Query Param",
			method:      "GET",
			urlPath:     "/blog",
			queryParams: "search=<script>alert(1)</script>",
			isBlocked:   true,
			category:    "Cross-Site Scripting",
		},
		{
			name:        "Command Injection in Query Param",
			method:      "GET",
			urlPath:     "/run",
			queryParams: "cmd=;whoami",
			isBlocked:   true,
			category:    "Command Injection",
		},
		{
			name:      "Malicious User-Agent SQLi",
			method:    "GET",
			urlPath:   "/",
			headers:   map[string]string{"User-Agent": "UNION SELECT"},
			isBlocked: true,
			category:  "SQL Injection",
		},
		{
			name:      "Malicious User-Agent Cmd Injection",
			method:    "GET",
			urlPath:   "/",
			headers:   map[string]string{"User-Agent": "curl; whoami"},
			isBlocked: true,
			category:  "Command Injection",
		},
		{
			name:      "Malicious Cookie SQLi",
			method:    "GET",
			urlPath:   "/",
			headers:   map[string]string{"Cookie": "session=1' OR 1=1"},
			isBlocked: true,
			category:  "SQL Injection",
		},
		{
			name:      "Malicious JSON Body SQLi",
			method:    "POST",
			urlPath:   "/api/login",
			headers:   map[string]string{"Content-Type": "application/json"},
			body:      `{"username": "' OR 1=1 --", "password": "xyz"}`,
			isBlocked: true,
			category:  "SQL Injection",
		},
		{
			name:      "Malicious Form Body XSS",
			method:    "POST",
			urlPath:   "/submit",
			headers:   map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
			body:      `name=%3Cscript%3Ealert(1)%3C%2Fscript%3E`,
			isBlocked: true,
			category:  "Cross-Site Scripting",
		},
		{
			name:      "Clean JSON Body",
			method:    "POST",
			urlPath:   "/api/data",
			headers:   map[string]string{"Content-Type": "application/json"},
			body:      `{"title": "Standard title", "content": "Just a post description without code."}`,
			isBlocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetURL := tt.urlPath
			if tt.queryParams != "" {
				vals, err := url.ParseQuery(tt.queryParams)
				if err == nil {
					targetURL += "?" + vals.Encode()
				} else {
					targetURL += "?" + url.PathEscape(tt.queryParams)
				}
			}

			var bodyReader *strings.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			} else {
				bodyReader = strings.NewReader("")
			}

			req := httptest.NewRequest(tt.method, targetURL, bodyReader)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			blocked, category, _ := IsMaliciousRequest(req)
			if blocked != tt.isBlocked {
				t.Errorf("Expected blocked = %t, got %t", tt.isBlocked, blocked)
			}
			if blocked && category != tt.category {
				t.Errorf("Expected category = %s, got %s", tt.category, category)
			}

			// Verify that body can still be read afterwards (not drained)
			if tt.body != "" {
				buf := new(bytes.Buffer)
				_, _ = buf.ReadFrom(req.Body) //nolint:errcheck
				if buf.String() != tt.body {
					t.Errorf("Expected request body to be preserved: %q, got %q", tt.body, buf.String())
				}
			}
		})
	}
}

func TestWAF_IntegrationInProxyHandler(t *testing.T) {
	// 1. Create registry and a mock backend server
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)

	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Target Service")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL) //nolint:errcheck
	port := 80
	if pStr := u.Port(); pStr != "" {
		importPort := 0
		for _, char := range pStr {
			importPort = importPort*10 + int(char-'0')
		}
		port = importPort
	}

	reg.Lock()
	reg.leases["peter-dev.lfr-demo.se"] = &TunnelLease{
		SubdomainPrefix: "peter-dev",
		FullHost:        "peter-dev.lfr-demo.se",
		LocalPort:       port,
		CreatedAt:       time.Now(),
	}
	reg.Unlock()

	// Case A: WAF is enabled, request contains exploit -> Should block and return 403
	cfgEnabled := config.DefaultServerConfig()
	cfgEnabled.EnableWAF = true
	handlerEnabled := NewProxyHandler(reg, cfgEnabled)

	reqBlocked := httptest.NewRequest("GET", "http://peter-dev.lfr-demo.se/../../etc/passwd", nil)
	reqBlocked.Host = "peter-dev.lfr-demo.se"
	recBlocked := httptest.NewRecorder()

	handlerEnabled.ServeHTTP(recBlocked, reqBlocked)

	if recBlocked.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d", recBlocked.Code)
	}
	if backendCalled {
		t.Error("Expected backend NOT to be called when WAF blocks")
	}
	if !strings.Contains(recBlocked.Body.String(), "Request Blocked") {
		t.Error("Expected blocked error page to be served")
	}

	// Case B: WAF is disabled, request contains exploit -> Should bypass and call backend
	backendCalled = false
	cfgDisabled := config.DefaultServerConfig()
	cfgDisabled.EnableWAF = false
	handlerDisabled := NewProxyHandler(reg, cfgDisabled)

	reqBypass := httptest.NewRequest("GET", "http://peter-dev.lfr-demo.se/../../etc/passwd", nil)
	reqBypass.Host = "peter-dev.lfr-demo.se"
	recBypass := httptest.NewRecorder()

	handlerDisabled.ServeHTTP(recBypass, reqBypass)

	if recBypass.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", recBypass.Code)
	}
	if !backendCalled {
		t.Error("Expected backend to be called when WAF is disabled")
	}
}
