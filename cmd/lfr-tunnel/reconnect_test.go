package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// shortenSameGatewayBackoff removes the real retry pauses, as shortenBackoff does for failover.
func shortenSameGatewayBackoff(t *testing.T) {
	t.Helper()
	orig := sameGatewayRetryBackoff
	sameGatewayRetryBackoff = time.Millisecond
	t.Cleanup(func() { sameGatewayRetryBackoff = orig })
}

// flakyRegistrationServer fails the first failures registrations and succeeds after that,
// which is the shape of a gateway being restarted: unreachable, then back.
func flakyRegistrationServer(t *testing.T, failures int32, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		n := atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if n <= failures {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"gateway restarting"}`)) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","session_token":"tok2","subdomain_prefix":"sub"}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A client with no failover path -- pinned with -server, or handed no region list -- used to
// have no way back at all after its gateway restarted: the session loop went round again with a
// session token the restarted gateway had forgotten, and nothing in that ~1s cycle lived long
// enough to re-register or even heartbeat (#1946). It must re-register with the gateway it
// already has, and keep trying across the restart.
func TestReregisterSameGatewayWaitsOutARestart(t *testing.T) {
	shortenSameGatewayBackoff(t)

	var hits int32
	srv := flakyRegistrationServer(t, 3, &hits)

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t", Region: "central"}
	resp, ok := reregisterSameGateway(context.Background(), cfg, nil, "sub", nil)
	if !ok {
		t.Fatalf("expected the client to keep trying until the gateway came back, after %d attempts", atomic.LoadInt32(&hits))
	}
	if resp.SessionToken != "tok2" {
		t.Errorf("expected the new session token, got %+v", resp)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Errorf("expected 3 failures then a success, got %d attempts", got)
	}

	// A reconnect, not a failover: this is the property that keeps PinnedRoutingNotice's
	// promise ("will not fail over") true for a pinned client.
	if cfg.ServerURL != srv.URL {
		t.Errorf("the gateway was changed: ServerURL = %q, want %q", cfg.ServerURL, srv.URL)
	}
	if cfg.Region != "central" {
		t.Errorf("the region was changed: Region = %q, want \"central\"", cfg.Region)
	}
}

// A failure no retry can fix must stop, rather than being re-sent every 30s forever. 403 is the
// reservation/quota/consent class, and this client has nowhere else to take it.
func TestReregisterSameGatewayStopsOnTerminalFailure(t *testing.T) {
	shortenSameGatewayBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusForbidden, `{"error":"subdomain reserved"}`, &hits)

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
	if _, ok := reregisterSameGateway(context.Background(), cfg, nil, "sub", nil); ok {
		t.Fatal("expected a terminal 403 to stop the reconnect")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected a terminal failure to stop after one attempt, got %d", got)
	}
}

// Retrying without a limit is only safe if stopping the client actually stops it.
func TestReregisterSameGatewayStopsWhenTheClientStops(t *testing.T) {
	shortenSameGatewayBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusBadGateway, `{}`, &hits)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := reregisterSameGateway(ctx, pinnedConfig(srv.URL), nil, "sub", nil)
		done <- ok
	}()

	// Let it establish that it really is retrying, then stop it.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&hits) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&hits); got < 3 {
		t.Fatalf("expected repeated attempts against the unreachable gateway, got %d", got)
	}
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Error("a cancelled reconnect must not report success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reregisterSameGateway ignored context cancellation")
	}
}

func pinnedConfig(serverURL string) *config.ClientConfig {
	return &config.ClientConfig{ServerURL: serverURL, AuthToken: "t"}
}
