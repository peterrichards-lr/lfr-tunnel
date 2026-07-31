package provisioner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeBackend is a Backend test double -- each field defaults to a
// reasonable success behavior, override individual funcs per test.
type fakeBackend struct {
	capabilities func() Capabilities
	start        func(ctx context.Context, nodeID string) error
	stop         func(ctx context.Context, nodeID string) error
	restart      func(ctx context.Context, nodeID string) error
	getSchedule  func(ctx context.Context, nodeID string) (Schedule, error)
	setSchedule  func(ctx context.Context, nodeID string, s Schedule) error
}

func (f *fakeBackend) Capabilities() Capabilities { return f.capabilities() }
func (f *fakeBackend) Start(ctx context.Context, nodeID string) error {
	return f.start(ctx, nodeID)
}
func (f *fakeBackend) Stop(ctx context.Context, nodeID string) error {
	return f.stop(ctx, nodeID)
}
func (f *fakeBackend) Restart(ctx context.Context, nodeID string) error {
	return f.restart(ctx, nodeID)
}
func (f *fakeBackend) GetSchedule(ctx context.Context, nodeID string) (Schedule, error) {
	return f.getSchedule(ctx, nodeID)
}
func (f *fakeBackend) SetSchedule(ctx context.Context, nodeID string, s Schedule) error {
	return f.setSchedule(ctx, nodeID, s)
}

func newTestFakeBackend() *fakeBackend {
	return &fakeBackend{
		capabilities: func() Capabilities { return Capabilities{StartStop: true, Restart: true, Scheduling: true} },
		start:        func(context.Context, string) error { return nil },
		stop:         func(context.Context, string) error { return nil },
		restart:      func(context.Context, string) error { return nil },
		getSchedule:  func(context.Context, string) (Schedule, error) { return Schedule{Enabled: true}, nil },
		setSchedule:  func(context.Context, string, Schedule) error { return nil },
	}
}

func TestServer_Versions_NoAuthRequired(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodGet, "/versions", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body struct {
		Supported []string `json:"supported"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(body.Supported) != 1 || body.Supported[0] != "v1" {
		t.Errorf("expected [\"v1\"], got %v", body.Supported)
	}
}

func TestServer_AuthenticatedEndpoints_RejectMissingToken(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestServer_AuthenticatedEndpoints_RejectWrongToken(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestServer_AuthenticatedEndpoints_AcceptCorrectToken(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_Start_Returns202(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/edge-sa/start", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}

func TestServer_Start_UnknownNodeReturns404(t *testing.T) {
	backend := newTestFakeBackend()
	backend.start = func(context.Context, string) error { return &ErrNodeNotFound{NodeID: "ghost"} }
	srv := NewServer(backend, "the-token")

	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/ghost/start", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestServer_Restart_DoesNotBlockOnSlowBackend is the key async-contract
// test: the handler must return 202 immediately even if the underlying
// Restart call would take a long time (see issue #888's async design).
func TestServer_Restart_DoesNotBlockOnSlowBackend(t *testing.T) {
	started := make(chan struct{})
	backend := newTestFakeBackend()
	backend.restart = func(ctx context.Context, nodeID string) error {
		close(started)
		<-ctx.Done() // would block "forever" if the handler waited on us
		return nil
	}
	srv := NewServer(backend, "the-token")

	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/edge-sa/restart", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.mux.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return promptly -- restart appears to be blocking the HTTP response")
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background restart never actually started")
	}
}

func TestServer_GetSchedule_ReturnsBackendResult(t *testing.T) {
	backend := newTestFakeBackend()
	backend.getSchedule = func(context.Context, string) (Schedule, error) {
		return Schedule{Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo"}, nil
	}
	srv := NewServer(backend, "the-token")

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes/edge-sa/schedule", nil)
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var sched Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &sched); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := Schedule{Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo"}
	if sched != want {
		t.Fatalf("got %+v, want %+v", sched, want)
	}
}

func TestServer_SetSchedule_PassesDecodedBody(t *testing.T) {
	var got Schedule
	backend := newTestFakeBackend()
	backend.setSchedule = func(_ context.Context, nodeID string, s Schedule) error {
		got = s
		return nil
	}
	srv := NewServer(backend, "the-token")

	body, _ := json.Marshal(Schedule{StopTime: "22:00", StartTime: "06:00", Timezone: "Asia/Kolkata"})
	req := httptest.NewRequest(http.MethodPut, "/v1/nodes/edge-in/schedule", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got.StopTime != "22:00" || got.StartTime != "06:00" || got.Timezone != "Asia/Kolkata" {
		t.Fatalf("backend did not receive the decoded body correctly: %+v", got)
	}
}

func TestServer_SetSchedule_RejectsInvalidJSON(t *testing.T) {
	srv := NewServer(newTestFakeBackend(), "the-token")
	req := httptest.NewRequest(http.MethodPut, "/v1/nodes/edge-in/schedule", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer the-token")
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
