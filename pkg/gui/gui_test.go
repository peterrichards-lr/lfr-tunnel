package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
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

// writeBrokenClientConfig points HOME at a fresh temp dir and drops an unparseable
// config.yaml at the path LoadClientConfig resolves by default. USERPROFILE is set too
// because that is what os.UserHomeDir reads on Windows, which the test matrix covers.
func writeBrokenClientConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	// An unterminated quoted scalar: the YAML scanner rejects this outright, which is
	// the shape of a real typo rather than a schema mismatch yaml would tolerate.
	broken := "server_url: \"https://example.com\nsubdomain: mine\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(broken), 0o600); err != nil {
		t.Fatalf("failed to write broken config: %v", err)
	}
	if _, err := config.LoadClientConfig(""); err == nil {
		t.Fatal("expected LoadClientConfig to reject the fixture; the test would prove nothing")
	}
}

// TestLoadUIConfigNeverReturnsNil — LoadClientConfig is nil-on-error for two of its three
// error paths, and #1758 made only the token_file branch return a usable config. #1771's
// fix is to normalise that in the one package that needs it rather than leave pkg/config
// carrying a caller's constraint on every future error it learns to return.
func TestLoadUIConfigNeverReturnsNil(t *testing.T) {
	writeBrokenClientConfig(t)

	cfg, err := loadUIConfig()
	if err == nil {
		t.Fatal("expected loadUIConfig to surface the parse error")
	}
	if cfg == nil {
		t.Fatal("expected loadUIConfig to fall back to a usable config, got nil")
	}
}

// TestSettingsHandlersReportAnUnparseableConfig — the two handlers that discarded
// LoadClientConfig's error reported a broken config as an absent one: /api/info showed
// "unknown" in every field and /api/logs 404'd with "Subdomain not configured", which the
// dashboard renders as "the client has not connected". Neither said the file failed to
// parse, so a typo looked like a first run (#1771).
func TestSettingsHandlersReportAnUnparseableConfig(t *testing.T) {
	writeBrokenClientConfig(t)
	srv := NewTempSettingsServer(0)

	t.Run("api/info carries the reason", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.handleInfo(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode /api/info response: %v", err)
		}
		msg, ok := resp["config_error"].(string)
		if !ok || msg == "" {
			t.Fatalf("expected /api/info to report why the config did not load, got %#v", resp)
		}
	})

	t.Run("api/config carries the reason", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.handleConfigGet(rec)

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode /api/config response: %v", err)
		}
		msg, ok := resp["config_error"].(string)
		if !ok || msg == "" {
			t.Fatalf("expected /api/config to report why the config did not load, got %#v", resp)
		}
	})

	t.Run("api/logs does not blame a missing subdomain", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.handleApiLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for a config that will not parse, got %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "Subdomain not configured") {
			t.Errorf("expected the parse failure to be named, got %q", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "config") {
			t.Errorf("expected the response to mention the config, got %q", rec.Body.String())
		}
	})
}

// TestApiLogsStillReportsAnUnsetSubdomain guards the other half: a config that loads fine
// but has no subdomain must keep its 404, because the dashboard's "the client has not
// connected" empty state is the correct message for that case.
func TestApiLogsStillReportsAnUnsetSubdomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LFT_CLIENT_SUBDOMAIN", "")
	t.Setenv("LFT_SUBDOMAIN", "")

	rec := httptest.NewRecorder()
	NewTempSettingsServer(0).handleApiLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when no subdomain is configured, got %d: %s", rec.Code, rec.Body.String())
	}
}
