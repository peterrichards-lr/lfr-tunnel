package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fetchAPIVersion(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/version = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /api/version: %v", err)
	}
	return body
}

// The block has to actually reach the wire, on the endpoint the client already polls.
func TestAPIVersionCarriesTheClientSettingsBlock(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.ClientReconnectWindow = 90 * time.Second
	srv.cfg.ClientHeartbeatInterval = 15 * time.Second

	body := fetchAPIVersion(t, srv)
	settings, ok := body["client_settings"].(map[string]any)
	if !ok {
		t.Fatalf("client_settings missing from /api/version: %s", mustJSON(t, body))
	}
	if got := settings["reconnect_seconds"]; got != float64(90) {
		t.Errorf("reconnect_seconds = %v, want 90", got)
	}
	if got := settings["heartbeat_seconds"]; got != float64(15) {
		t.Errorf("heartbeat_seconds = %v, want 15", got)
	}

	// The legacy top-level field stays, or every client shipped before the block silently
	// reverts to its compiled-in default.
	if got := body["client_reconnect_seconds"]; got != float64(90) {
		t.Errorf("client_reconnect_seconds = %v, want 90 -- an older client reads only this", got)
	}
}

// "No opinion" has to stay expressible, or a gateway that configures nothing would push zeroes
// at the fleet and every client would have to special-case them.
func TestUnconfiguredSettingsAreOmittedRatherThanSentAsZero(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.ClientReconnectWindow = 0
	srv.cfg.ClientHeartbeatInterval = 0

	body := fetchAPIVersion(t, srv)
	settings, ok := body["client_settings"].(map[string]any)
	if !ok {
		t.Fatalf("client_settings missing entirely: %s", mustJSON(t, body))
	}
	if _, present := settings["reconnect_seconds"]; present {
		t.Errorf("an unconfigured reconnect window was advertised as a value: %v", settings)
	}
	if _, present := settings["heartbeat_seconds"]; present {
		t.Errorf("an unconfigured heartbeat was advertised as a value: %v", settings)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(b)
}
