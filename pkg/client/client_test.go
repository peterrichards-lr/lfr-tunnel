package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectWorkspacePorts(t *testing.T) {
	// 1. Create a mock workspace directory
	tmpDir, err := os.MkdirTemp("", "liferay-workspace-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a subdirectory for a client extension
	extDir := filepath.Join(tmpDir, "client-extensions", "my-custom-element")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("failed to create client extension dir: %v", err)
	}

	// Write a mock client-extension.yaml file
	yamlContent := []byte(`
my-custom-element:
  name: My Custom Element
  type: customElement
  port: 3001
`)
	yamlPath := filepath.Join(extDir, "client-extension.yaml")
	if err := os.WriteFile(yamlPath, yamlContent, 0644); err != nil {
		t.Fatalf("failed to write client-extension.yaml: %v", err)
	}

	// 2. Invoke detection
	mappings, err := DetectWorkspacePorts(tmpDir)
	if err != nil {
		t.Fatalf("DetectWorkspacePorts failed: %v", err)
	}

	// Expecting default 8080 plus detected 3001
	if len(mappings) != 2 {
		t.Errorf("expected 2 port mappings, got %d", len(mappings))
	}

	found8080 := false
	found3001 := false

	for _, m := range mappings {
		if m.LocalPort == 8080 {
			found8080 = true
		}
		if m.LocalPort == 3001 {
			found3001 = true
			if m.NameSuffix != "my-custom-element" {
				t.Errorf("expected suffix 'my-custom-element', got '%s'", m.NameSuffix)
			}
		}
	}

	if !found8080 {
		t.Error("expected default port 8080 to be included")
	}
	if !found3001 {
		t.Error("expected detected port 3001 to be included")
	}
}

func TestRegisterTunnel(t *testing.T) {
	// 1. Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/register" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}

		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode register request: %v", err)
		}

		if req.SubdomainPrefix != "test-sub" {
			t.Errorf("expected subdomain prefix 'test-sub', got '%s'", req.SubdomainPrefix)
		}
		if req.AuthToken != "mysecret" {
			t.Errorf("expected auth token 'mysecret', got '%s'", req.AuthToken)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(RegisterResponse{
			Status:       "success",
			SessionToken: "mock-session-token",
			Remotes:      []string{"R:10001:localhost:8080"},
		})
	}))
	defer server.Close()

	// 2. Call RegisterTunnel
	ports := []PortMapping{{LocalPort: 8080}}
	resp, err := RegisterTunnel(server.URL, "mysecret", "test-sub", ports, 0, "", nil)
	if err != nil {
		t.Fatalf("RegisterTunnel failed: %v", err)
	}

	if resp.Status != "success" {
		t.Errorf("expected status 'success', got '%s'", resp.Status)
	}
	if resp.SessionToken != "mock-session-token" {
		t.Errorf("expected session token 'mock-session-token', got '%s'", resp.SessionToken)
	}
	if len(resp.Remotes) != 1 || resp.Remotes[0] != "R:10001:localhost:8080" {
		t.Errorf("unexpected remotes: %v", resp.Remotes)
	}
}
