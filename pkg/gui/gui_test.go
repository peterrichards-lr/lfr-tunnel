package gui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
)

func TestGetRunningState(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir) //nolint:errcheck
	}()

	oldHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tempDir) //nolint:errcheck
	defer func() {
		_ = os.Setenv("HOME", oldHome) //nolint:errcheck
	}()

	sub := "test-gui-sub"

	_, _, isRunning := getRunningState(sub)
	if isRunning {
		t.Errorf("expected running state to be false initially")
	}

	pidFile, err := getPIDFilePath(sub)
	if err != nil {
		t.Fatalf("failed to resolve pid file: %v", err)
	}

	myPID := os.Getpid()
	err = os.WriteFile(pidFile, []byte(strconv.Itoa(myPID)), 0600)
	if err != nil {
		t.Fatalf("failed to write pid: %v", err)
	}

	statePath, err := client.GetStateFilePath(sub)
	if err != nil {
		t.Fatalf("failed to resolve state path: %v", err)
	}

	err = os.MkdirAll(filepath.Dir(statePath), 0700)
	if err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	mockState := &client.ClientState{
		PID:           myPID,
		InspectorPort: 4040,
		InspectorURL:  "http://127.0.0.1:4040",
		Subdomain:     sub,
		PublicURLs:    []string{"https://test-gui-sub.lfr-demo.se"},
		StartTime:     time.Now().Format(time.RFC3339),
	}
	stateBytes, err := json.Marshal(mockState)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	err = os.WriteFile(statePath, stateBytes, 0600)
	if err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	resolvedState, resolvedSub, isRunning := getRunningState(sub)
	if !isRunning {
		t.Errorf("expected running state to be true")
	}
	if resolvedSub != sub {
		t.Errorf("expected subdomain prefix %q, got %q", sub, resolvedSub)
	}
	if resolvedState == nil || resolvedState.InspectorPort != 4040 {
		t.Errorf("expected inspector port to be 4040")
	}
}

// TestHandleConfigGetStillReportsPort8080WhenUnset — #1710 removed the []int{8080} seed
// from DefaultClientConfig so that the client can tell "unset" from "explicitly 8080" and
// reach port discovery. The settings UI must be unaffected: with no config file at all it
// still reports 8080, because it applies that fallback itself.
func TestHandleConfigGetStillReportsPort8080WhenUnset(t *testing.T) {
	// An empty HOME means LoadClientConfig finds no file and falls back to the defaults.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LFT_CLIENT_PORTS", "")

	rec := httptest.NewRecorder()
	NewTempSettingsServer(0).handleConfigGet(rec)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode settings response: %v", err)
	}
	destPort, ok := resp["dest_port"].(float64)
	if !ok {
		t.Fatalf("expected a numeric dest_port, got %#v", resp["dest_port"])
	}
	if int(destPort) != 8080 {
		t.Errorf("expected the settings UI to still default dest_port to 8080, got %d", int(destPort))
	}
}
