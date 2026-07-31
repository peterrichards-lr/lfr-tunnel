package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Start_SendsBearerToken(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "the-token")
	if err := c.Start(context.Background(), "edge-sa"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer the-token" {
		t.Errorf("expected Bearer the-token, got %q", gotAuth)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/nodes/edge-sa/start" {
		t.Errorf("expected /v1/nodes/edge-sa/start, got %s", gotPath)
	}
}

func TestClient_GetSchedule_DecodesResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Schedule{Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "the-token")
	sched, err := c.GetSchedule(context.Background(), "edge-sa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Schedule{Enabled: true, StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo"}
	if sched != want {
		t.Fatalf("got %+v, want %+v", sched, want)
	}
}

func TestClient_SetSchedule_SendsBodyAsJSON(t *testing.T) {
	var gotBody Schedule
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "the-token")
	send := Schedule{StopTime: "22:00", StartTime: "06:00", Timezone: "Asia/Kolkata"}
	if err := c.SetSchedule(context.Background(), "edge-in", send); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody != send {
		t.Fatalf("got %+v, want %+v", gotBody, send)
	}
}

func TestClient_NonOKResponse_ReturnsErrRemote(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "node_not_found", "message": "unknown node: ghost"},
		})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "the-token")
	err := c.Start(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	remoteErr, ok := err.(*ErrRemote)
	if !ok {
		t.Fatalf("expected *ErrRemote, got %T: %v", err, err)
	}
	if remoteErr.StatusCode != http.StatusNotFound || remoteErr.Code != "node_not_found" {
		t.Errorf("got %+v", remoteErr)
	}
}

func TestClient_Unreachable_ReturnsErrBackendUnavailable(t *testing.T) {
	// A closed server guarantees a connection failure without relying on any
	// particular unused port being free.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close()

	c := NewClient(ts.URL, "the-token")
	err := c.Start(context.Background(), "edge-sa")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if _, ok := err.(*ErrBackendUnavailable); !ok {
		t.Fatalf("expected *ErrBackendUnavailable, got %T: %v", err, err)
	}
}

func TestClient_Versions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"supported": []string{"v1"}})
	}))
	defer ts.Close()

	// The client always sends its token, even to /versions -- the server side
	// (server.go) is what makes that endpoint not *require* one; a harmless
	// extra header on a request the server ignores it on isn't worth a
	// client-side special case.
	c := NewClient(ts.URL, "the-token")
	versions, err := c.Versions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 || versions[0] != "v1" {
		t.Fatalf("got %v", versions)
	}
}
