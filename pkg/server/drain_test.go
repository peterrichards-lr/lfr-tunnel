package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

func newDrainTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	return srv
}

func postDrain(t *testing.T, srv *Server, body string) drainStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/local/drain", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got drainStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode drain status: %v", err)
	}
	return got
}

// Announcing a drain has to reach clients by the channel they already listen on -- the
// tunnel-status heartbeat -- or the announcement is invisible and the restart still drops
// them (#1238, #1303).
func TestDrain_AnnouncementReachesTheHeartbeat(t *testing.T) {
	srv := newDrainTestServer(t)

	if w := srv.pendingShutdownWarning(); w != nil {
		t.Fatalf("a fresh gateway should have nothing pending, got %#v", w)
	}

	got := postDrain(t, srv, `{"seconds": 45, "reason": "Deploying"}`)
	if !got.Draining {
		t.Fatal("expected the gateway to report itself draining")
	}
	if got.SecondsRemaining <= 0 || got.SecondsRemaining > 45 {
		t.Errorf("seconds_remaining = %d, want 0 < n <= 45", got.SecondsRemaining)
	}

	warning := srv.pendingShutdownWarning()
	if warning == nil {
		t.Fatal("the heartbeat carries nothing, so no client would ever learn of the restart")
	}
	if warning["type"] != "node_shutdown_warning" {
		t.Errorf("warning type = %v, want node_shutdown_warning", warning["type"])
	}
	if warning["reason"] != "Deploying" {
		t.Errorf("warning reason = %v, want the reason given to the drain", warning["reason"])
	}
}

// A deploy that fails partway through has to be able to put the gateway back, or clients keep
// migrating away from a node that is not going anywhere.
func TestDrain_CancellingClearsTheAnnouncement(t *testing.T) {
	srv := newDrainTestServer(t)

	postDrain(t, srv, `{"seconds": 45}`)
	got := postDrain(t, srv, `{"seconds": 0}`)

	if got.Draining {
		t.Error("expected the gateway to report itself no longer draining")
	}
	if w := srv.pendingShutdownWarning(); w != nil {
		t.Errorf("the heartbeat still carries a warning after cancelling: %#v", w)
	}
}

// The default reason is what an operator sees in the client's log during a deploy, so it has
// to say something, not be blank.
func TestDrain_SuppliesADefaultReason(t *testing.T) {
	srv := newDrainTestServer(t)

	got := postDrain(t, srv, `{"seconds": 30}`)
	if got.Reason == "" {
		t.Fatal("expected a default reason rather than an empty one")
	}
}

// GET is what the deploy polls while waiting, so it must report state without altering it.
func TestDrain_GetReportsWithoutChanging(t *testing.T) {
	srv := newDrainTestServer(t)
	postDrain(t, srv, `{"seconds": 45, "reason": "Deploying"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/local/drain", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var got drainStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if !got.Draining || got.Reason != "Deploying" {
		t.Errorf("GET changed or lost the announcement: %#v", got)
	}
	if got.LocalLeases != 0 {
		t.Errorf("local_leases = %d on a gateway serving nothing, want 0", got.LocalLeases)
	}
}

// nginx forwards to 127.0.0.1, so a request from the internet looks local by the time it
// arrives. The proxy headers are what distinguishes the two, and without that check anyone
// could announce a shutdown and push every client off a healthy gateway.
func TestDrain_RejectsForwardedAndRemoteRequests(t *testing.T) {
	srv := newDrainTestServer(t)

	cases := []struct {
		name       string
		remoteAddr string
		header     [2]string
	}{
		{"remote address", "203.0.113.9:40000", [2]string{"", ""}},
		{"forwarded-for", "127.0.0.1:54321", [2]string{"X-Forwarded-For", "203.0.113.9"}},
		{"forwarded-host", "127.0.0.1:54321", [2]string{"X-Forwarded-Host", "evil.example"}},
		{"real-ip", "127.0.0.1:54321", [2]string{"X-Real-IP", "203.0.113.9"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/local/drain", strings.NewReader(`{"seconds": 45}`))
			req.RemoteAddr = tc.remoteAddr
			if tc.header[0] != "" {
				req.Header.Set(tc.header[0], tc.header[1])
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("got %d, want 403", rec.Code)
			}
			if w := srv.pendingShutdownWarning(); w != nil {
				t.Errorf("a rejected request still announced a shutdown: %#v", w)
			}
		})
	}
}

// A gateway that announces a drain and then keeps accepting registrations moves the same
// client twice: it lands on a node seconds from restarting, and is moved again (#1238).
func TestDrain_HealthzAdvertisesDrainingDistinctly(t *testing.T) {
	srv := newDrainTestServer(t)

	if got := string(srv.healthzPayload()); got != `{"status":"healthy"}` {
		t.Fatalf("before draining, healthz = %s", got)
	}

	postDrain(t, srv, `{"seconds": 45}`)

	got := string(srv.healthzPayload())
	if !strings.Contains(got, `"status":"draining"`) {
		t.Errorf("healthz = %s, want a draining status", got)
	}
	// Deliberately NOT "degraded": an operator has to tell a deploy from a node that has lost
	// its control channel, and conflating those is where #1145 came from.
	if strings.Contains(got, "degraded") {
		t.Errorf("healthz = %s, want draining reported separately from degraded", got)
	}

	postDrain(t, srv, `{"seconds": 0}`)
	if got := string(srv.healthzPayload()); strings.Contains(got, "draining") {
		t.Errorf("healthz still reports draining after the drain was cancelled: %s", got)
	}
}

// The clients that matter here are the ones that do not probe: a -server pinned client, an
// older build, or a registration already in flight when the drain was announced.
func TestDrain_RegistrationIsRefusedWhileDraining(t *testing.T) {
	srv := newDrainTestServer(t)
	postDrain(t, srv, `{"seconds": 45}`)

	req := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"auth_token":"whatever","subdomain_prefix":"x","ports":[{"local_port":80}]}`))
	req.Host = "lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 while the gateway is draining", rec.Code)
	}
	// Temporary, not broken: the client should come back rather than write the gateway off.
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a refusal with no Retry-After reads as a permanent failure")
	}
}

// And once the drain is cancelled -- a deploy that failed partway, say -- the gateway has to
// accept work again, or a cancelled drain is worse than none.
func TestDrain_RegistrationResumesAfterCancel(t *testing.T) {
	srv := newDrainTestServer(t)
	postDrain(t, srv, `{"seconds": 45}`)
	postDrain(t, srv, `{"seconds": 0}`)

	req := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"auth_token":"whatever","subdomain_prefix":"x","ports":[{"local_port":80}]}`))
	req.Host = "lfr-demo.se"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusServiceUnavailable {
		t.Error("the gateway is still refusing registrations after the drain was cancelled")
	}
}
