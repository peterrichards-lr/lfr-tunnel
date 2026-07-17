package client

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStateFileWriteAndDelete(t *testing.T) {
	sub := "test-sub-state"
	state := &ClientState{
		PID:           12345,
		InspectorPort: 4040,
		InspectorURL:  "http://127.0.0.1:4040",
		Subdomain:     "test-sub",
		PublicURLs:    []string{"https://test-sub.lfr-demo.se"},
		Ports:         []int{8080},
		StartTime:     "2026-06-22T08:00:00Z",
	}

	err := WriteState(sub, state)
	if err != nil {
		t.Fatalf("failed to write state: %v", err)
	}

	path, err := GetStateFilePath(sub)
	if err != nil {
		t.Fatalf("failed to get state path: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("state file was not created")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var readState ClientState
	if err := json.Unmarshal(data, &readState); err != nil {
		t.Fatalf("failed to unmarshal state: %v", err)
	}

	if readState.PID != state.PID || readState.Subdomain != state.Subdomain {
		t.Errorf("state mismatch: %+v vs %+v", readState, state)
	}

	DeleteState(sub)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("state file was not deleted")
	}
}

func TestQueryStatusJSON(t *testing.T) {
	// Start mock inspector HTTP server
	mockMux := http.NewServeMux()
	mockMux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{
			"status": "healthy",
			"connection": {
				"state": "connected"
			},
			"traffic": {
				"bytes_in": 123,
				"bytes_out": 456,
				"requests_total": 789
			}
		}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	})
	server := httptest.NewServer(mockMux)
	defer server.Close()

	tempDir, err := os.MkdirTemp("", "tunnel-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir) //nolint:errcheck

	statePath := filepath.Join(tempDir, "lfr-tunnel-test.state")
	state := &ClientState{
		PID:           99999,
		InspectorPort: 4040,
		InspectorURL:  server.URL,
		Subdomain:     "test",
		PublicURLs:    []string{"https://test.lfr-demo.se"},
		Ports:         []int{8080},
		StartTime:     "2026-06-22T08:00:00Z",
	}

	data, _ := json.Marshal(state)
	_ = os.WriteFile(statePath, data, 0600)

	// Scenario 1: PID is running, and inspector is up
	isRunningTrue := func(pid int) bool { return true }
	res, err := QueryStatusJSON(statePath, isRunningTrue)
	if err != nil {
		t.Fatalf("QueryStatusJSON failed: %v", err)
	}

	var output StatusOutput
	if err := json.Unmarshal(res, &output); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if !output.Running {
		t.Errorf("expected running to be true")
	}
	if output.Status != "healthy" || output.ConnectionState != "connected" {
		t.Errorf("expected status 'healthy' and state 'connected', got status %s, state %s", output.Status, output.ConnectionState)
	}
	if output.BytesIn != 123 || output.BytesOut != 456 || output.RequestsTotal != 789 {
		t.Errorf("unexpected telemetry: %+v", output)
	}

	// Scenario 2: PID is NOT running (file should be deleted and return running: false)
	isRunningFalse := func(pid int) bool { return false }
	res2, err := QueryStatusJSON(statePath, isRunningFalse)
	if err != nil {
		t.Fatalf("QueryStatusJSON failed: %v", err)
	}

	var output2 StatusOutput
	_ = json.Unmarshal(res2, &output2)
	if output2.Running {
		t.Errorf("expected running to be false for non-running process")
	}

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("expected state file to be deleted for non-running process")
	}
}
