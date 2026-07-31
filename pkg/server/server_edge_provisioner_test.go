package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/provisioner"
)

func adminRequest(method, path string, body []byte, sessionToken string) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(string(body)))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	return req
}

func newAdminSession(t *testing.T, srv *Server, email string) string {
	t.Helper()
	token := "test-admin-session-" + email
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{Email: email, ExpiresAt: time.Now().Add(time.Hour)})
	return token
}

func TestAdminEdge_NotConfigured_Returns501(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	// provisionerClient left nil -- feature not configured.
	token := newAdminSession(t, srv, "admin@example.com")

	paths := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/admin/edge/edge-sa/start"},
		{http.MethodPost, "/api/admin/edge/edge-sa/stop"},
		{http.MethodPost, "/api/admin/edge/edge-sa/restart"},
		{http.MethodGet, "/api/admin/edge/edge-sa/schedule"},
		{http.MethodPut, "/api/admin/edge/edge-sa/schedule"},
		{http.MethodPost, "/api/admin/edge/bulk"},
	}
	for _, p := range paths {
		req := adminRequest(p.method, p.path, []byte(`{}`), token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: expected 501, got %d: %s", p.method, p.path, w.Code, w.Body.String())
		}
	}
}

// fakeSidecar spins up a real HTTP server implementing just enough of the
// #888 contract for these tests, so srv.provisionerClient is a genuine
// provisioner.Client talking over loopback HTTP -- not a hand-rolled mock of
// the client's internals.
func newFakeSidecar(t *testing.T) (*httptest.Server, *provisioner.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/nodes/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/nodes/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/nodes/{id}/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "ghost" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "node_not_found", "message": "unknown node: ghost"},
			})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /v1/nodes/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provisioner.Schedule{
			Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo",
		})
	})
	mux.HandleFunc("PUT /v1/nodes/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		var sched provisioner.Schedule
		_ = json.NewDecoder(r.Body).Decode(&sched)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sched)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, provisioner.NewClient(ts.URL, "test-token")
}

func TestAdminEdge_Start_ProxiesToSidecar(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	req := adminRequest(http.MethodPost, "/api/admin/edge/edge-sa/start", nil, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminEdge_Restart_UnknownNodePropagates404(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	req := adminRequest(http.MethodPost, "/api/admin/edge/ghost/restart", nil, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminEdge_GetSchedule_ReturnsSidecarResponse(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	req := adminRequest(http.MethodGet, "/api/admin/edge/edge-sa/schedule", nil, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var sched provisioner.Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &sched); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := provisioner.Schedule{Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo"}
	if sched != want {
		t.Fatalf("got %+v, want %+v", sched, want)
	}
}

func TestAdminEdge_SetSchedule_SendsBodyThrough(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	body, _ := json.Marshal(provisioner.Schedule{StopTime: "22:00", StartTime: "06:00", Timezone: "Asia/Kolkata"})
	req := adminRequest(http.MethodPut, "/api/admin/edge/edge-in/schedule", body, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminEdge_BulkAction_MixedResults(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	body, _ := json.Marshal(map[string]any{
		"node_ids": []string{"edge-sa", "ghost"},
		"action":   "restart",
	})
	req := adminRequest(http.MethodPost, "/api/admin/edge/bulk", body, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Results map[string]struct {
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !out.Results["edge-sa"].OK {
		t.Errorf("expected edge-sa to succeed, got %+v", out.Results["edge-sa"])
	}
	if out.Results["ghost"].OK {
		t.Errorf("expected ghost to fail, got %+v", out.Results["ghost"])
	}
}

func TestAdminEdge_BulkAction_RejectsInvalidAction(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	_, client := newFakeSidecar(t)
	srv.provisionerClient = client
	token := newAdminSession(t, srv, "admin@example.com")

	body, _ := json.Marshal(map[string]any{"node_ids": []string{"edge-sa"}, "action": "reboot-now-please"})
	req := adminRequest(http.MethodPost, "/api/admin/edge/bulk", body, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEdgeHealth_ExposesProvisionerEnabledFlag(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	token := newAdminSession(t, srv, "admin@example.com")

	req := adminRequest(http.MethodGet, "/api/portal/edge-health", nil, token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var out struct {
		EdgePowerActionsEnabled bool `json:"edge_power_actions_enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v (status %d, body %s)", err, w.Code, w.Body.String())
	}
	if out.EdgePowerActionsEnabled {
		t.Error("expected edge_power_actions_enabled=false when provisionerClient is nil")
	}

	_, client := newFakeSidecar(t)
	srv.provisionerClient = client

	req2 := adminRequest(http.MethodGet, "/api/portal/edge-health", nil, token)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if err := json.Unmarshal(w2.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !out.EdgePowerActionsEnabled {
		t.Error("expected edge_power_actions_enabled=true once provisionerClient is set")
	}
}
