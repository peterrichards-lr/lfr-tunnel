package server

import (
	"encoding/json"
	"testing"
	"time"

	chserver "github.com/jpillora/chisel/server"
	"lfr-tunnel/pkg/config"
)

// TestHealthzReportsControlPlaneState is the regression test for #1145. An edge answering
// a bare "healthy" while its control channel was down is what let clients fail back onto
// a region that immediately evicted them.
func TestHealthzReportsControlPlaneState(t *testing.T) {
	parse := func(t *testing.T, raw []byte) map[string]string {
		t.Helper()
		var out map[string]string
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("healthz body is not valid JSON: %v (%s)", err, raw)
		}
		return out
	}

	t.Run("central reports no control_plane field", func(t *testing.T) {
		// Central has no upstream to be connected to. Older gateways also omit the
		// field, and a client treats its absence as healthy.
		s := &Server{cfg: &config.ServerConfig{}}
		body := parse(t, s.healthzPayload())
		if body["status"] != "healthy" {
			t.Errorf("expected healthy, got %q", body["status"])
		}
		if _, present := body["control_plane"]; present {
			t.Error("central must not claim a control-plane state it does not have")
		}
	})

	t.Run("connected edge is healthy", func(t *testing.T) {
		s := &Server{cfg: &config.ServerConfig{ControlPlaneURL: "https://tunnel.example"}}
		s.edgeControlConnected.Store(true)
		body := parse(t, s.healthzPayload())
		if body["status"] != "healthy" || body["control_plane"] != "connected" {
			t.Errorf("expected healthy/connected, got %v", body)
		}
	})

	t.Run("disconnected edge reports degraded", func(t *testing.T) {
		s := &Server{cfg: &config.ServerConfig{ControlPlaneURL: "https://tunnel.example"}}
		s.edgeControlConnected.Store(false)
		body := parse(t, s.healthzPayload())
		if body["control_plane"] != "disconnected" {
			t.Errorf("an edge that cannot reach central must say so, got %v", body)
		}
		if body["status"] == "healthy" {
			t.Error("an edge that cannot carry a session must not report itself healthy")
		}
	})
}

// TestSessionWasCleaned is the regression test for #1146: a gateway must be able to say
// "I destroyed that session" rather than leaving the client to infer it.
func TestSessionWasCleaned(t *testing.T) {
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}
	r := NewRegistry(chiselServer)

	if r.SessionWasCleaned("never-seen") {
		t.Error("a token this gateway never held is not evidence of anything -- central sees these constantly for edge-hosted sessions")
	}

	lease := &TunnelLease{FullHost: "x.example", SessionToken: "doomed", LocalPort: 41001, CreatedAt: time.Now()}
	r.sessionLeases["doomed"] = []*TunnelLease{lease}
	r.leases[lease.FullHost] = lease
	r.usedPorts[lease.LocalPort] = true

	r.CleanLease("doomed")

	if !r.SessionWasCleaned("doomed") {
		t.Error("expected a reaped session to be remembered so the client can be told to re-register")
	}

	// Expiry: the record only has to outlast the client's 5s heartbeat.
	r.Lock()
	r.cleanedSessions["doomed"] = time.Now().Add(-2 * cleanedSessionTTL)
	r.Unlock()
	if r.SessionWasCleaned("doomed") {
		t.Error("expected the record to expire rather than accumulate forever")
	}
}

// TestRememberCleanedSessionPrunes guards the bound on the map.
func TestRememberCleanedSessionPrunes(t *testing.T) {
	r := &Registry{cleanedSessions: make(map[string]time.Time)}

	r.Lock()
	r.cleanedSessions["stale"] = time.Now().Add(-2 * cleanedSessionTTL)
	r.rememberCleanedSession("fresh")
	r.Unlock()

	if _, ok := r.cleanedSessions["stale"]; ok {
		t.Error("expected expired entries to be pruned on insert, or the map grows without bound")
	}
	if _, ok := r.cleanedSessions["fresh"]; !ok {
		t.Error("expected the new entry to be recorded")
	}
}
