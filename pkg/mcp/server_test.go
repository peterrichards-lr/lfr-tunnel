package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPServer_Initialize(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"initialize","id":1}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	RunMCPLoop(r, &w)

	var resp Response
	if err := json.Unmarshal(w.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v, body: %s", err, w.String())
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc '2.0', got %q", resp.JSONRPC)
	}
	if fmt.Sprintf("%v", resp.ID) != "1" {
		t.Errorf("expected ID '1', got %v", resp.ID)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", resp.Result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion '2024-11-05', got %v", result["protocolVersion"])
	}
}

func TestMCPServer_ToolsList(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"tools/list","id":"my-req-id"}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	RunMCPLoop(r, &w)

	var resp Response
	if err := json.Unmarshal(w.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v, body: %s", err, w.String())
	}

	if resp.ID != "my-req-id" {
		t.Errorf("expected ID 'my-req-id', got %v", resp.ID)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", resp.Result)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}

	if len(tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(tools))
	}
}

func TestMCPServer_GetTunnelStatus_Offline(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	input := `{"jsonrpc":"2.0","method":"tools/call","id":3,"params":{"name":"get_tunnel_status"}}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	RunMCPLoop(r, &w)

	var resp Response
	if err := json.Unmarshal(w.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v, body: %s", err, w.String())
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", resp.Result)
	}
	content, ok := result["content"].([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", result["content"])
	}
	if len(content) == 0 {
		t.Fatalf("expected non-empty content list")
	}

	item := content[0].(map[string]interface{})
	text := item["text"].(string)

	var status struct {
		ActiveTunnels []interface{} `json:"active_tunnels"`
	}
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to decode tool text content: %v", err)
	}

	if len(status.ActiveTunnels) != 0 {
		t.Errorf("expected 0 active tunnels, got %d", len(status.ActiveTunnels))
	}
}

func TestMCPServer_GetTunnelStatus_Online(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Spin up a mock HTTP server to act as the inspector API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"status":"healthy","connection":{"state":"connected"}}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Write a mock active state file with our mock server's URL and the current process PID (so it reports as running)
	dir := filepath.Join(tmpDir, ".lfr-tunnel")
	_ = os.MkdirAll(dir, 0700) //nolint:errcheck
	state := ClientState{
		PID:           os.Getpid(),
		InspectorPort: 4040,
		InspectorURL:  server.URL,
		Subdomain:     "mcp-test-subdomain",
		PublicURLs:    []string{"https://mcp-test-subdomain.lfr-demo.se"},
		Ports:         []int{8080},
		StartTime:     "2026-06-24T10:00:00Z",
	}

	b, _ := json.Marshal(state)
	_ = os.WriteFile(filepath.Join(dir, "lfr-tunnel-mcp-test-subdomain.state"), b, 0600) //nolint:errcheck

	input := `{"jsonrpc":"2.0","method":"tools/call","id":4,"params":{"name":"get_tunnel_status"}}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	RunMCPLoop(r, &w)

	var resp Response
	_ = json.Unmarshal(w.Bytes(), &resp) //nolint:errcheck
	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	item := content[0].(map[string]interface{})
	text := item["text"].(string)

	var status struct {
		ActiveTunnels []map[string]interface{} `json:"active_tunnels"`
	}
	_ = json.Unmarshal([]byte(text), &status) //nolint:errcheck

	if len(status.ActiveTunnels) != 1 {
		t.Errorf("expected 1 active tunnel, got %d", len(status.ActiveTunnels))
	} else {
		tName := status.ActiveTunnels[0]["subdomain"].(string)
		if tName != "mcp-test-subdomain" {
			t.Errorf("expected subdomain 'mcp-test-subdomain', got %q", tName)
		}
		liveStatus := status.ActiveTunnels[0]["live_status"].(map[string]interface{})
		if liveStatus["status"] != "healthy" {
			t.Errorf("expected live_status 'healthy', got %v", liveStatus["status"])
		}
	}
}

func TestMCPServer_ListRequests(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Spin up a mock HTTP server for inspector history
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"history":[{"id":"req_1","method":"GET","path":"/test"}]}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Write mock state file pointing to our mock inspector
	dir := filepath.Join(tmpDir, ".lfr-tunnel")
	_ = os.MkdirAll(dir, 0700) //nolint:errcheck
	state := ClientState{
		PID:           os.Getpid(),
		InspectorPort: 4040,
		InspectorURL:  server.URL,
		Subdomain:     "mcp-test",
		PublicURLs:    []string{"https://mcp-test.lfr-demo.se"},
		Ports:         []int{8080},
		StartTime:     "2026-06-24T10:00:00Z",
	}
	b, _ := json.Marshal(state)
	_ = os.WriteFile(filepath.Join(dir, "lfr-tunnel-mcp-test.state"), b, 0600) //nolint:errcheck

	input := `{"jsonrpc":"2.0","method":"tools/call","id":5,"params":{"name":"list_requests","arguments":{"limit":5}}}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	RunMCPLoop(r, &w)

	var resp Response
	_ = json.Unmarshal(w.Bytes(), &resp) //nolint:errcheck
	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	item := content[0].(map[string]interface{})
	text := item["text"].(string)

	var list struct {
		Requests []map[string]interface{} `json:"requests"`
	}
	_ = json.Unmarshal([]byte(text), &list) //nolint:errcheck

	if len(list.Requests) != 1 {
		t.Errorf("expected 1 request, got %d", len(list.Requests))
	} else {
		path := list.Requests[0]["path"].(string)
		if path != "/test" {
			t.Errorf("expected path '/test', got %q", path)
		}
	}
}
