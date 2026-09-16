package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpillora/backoff"
)

// TestRunClientRidesOutAGatewayRestart is the control for #1946.
//
// It fails on the behaviour this repo shipped: `MaxRetryInterval: 3s, MaxRetryCount: 3` is four
// connection attempts and 700ms of backoff -- not three seconds times three -- because
// MaxRetryInterval only caps chisel's backoff, which starts at the backoff library's 100ms
// default. Against a gateway that answers immediately (nginx in a maintenance window, say)
// RunClient returned in well under a second and the session was over. Measured against the old
// constants this test reports "gave up after 0.7s having made 4 attempts".
//
// The assertion is deliberately two-part. "Still running at 4s" alone would also be satisfied by
// a connection that hung, which is a different bug wearing the same result; the attempt count
// says it is genuinely still retrying, and that it has already exceeded the entire old budget.
func TestRunClientRidesOutAGatewayRestart(t *testing.T) {
	// How long a client must keep trying before this test is satisfied. Far short of the real
	// 60s window -- the point is to be past the old 0.7s budget by a margin no scheduling
	// hiccup can explain, not to sit through a production restart.
	const restartFloor = 4 * time.Second

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// What nginx returns for /tunnel while lfr-tunneld is being restarted.
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := NewInterceptorEngine("127.0.0.1", nil)
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, srv.URL, "dummy-token", []string{"R:127.0.0.1:60000:localhost:8080"}, nil, engine)
	}()

	start := time.Now()
	select {
	case <-done:
		t.Fatalf("RunClient gave up after %s having made %d connection attempts; a gateway restart takes longer than that, and giving up here is what left a real client offline for 2h12m (#1946)",
			time.Since(start).Round(time.Millisecond), atomic.LoadInt32(&attempts))
	case <-time.After(restartFloor):
	}

	// The old budget was four attempts in total. Anything past that proves the retry loop is
	// still alive rather than blocked on a single dial.
	if got := atomic.LoadInt32(&attempts); got <= 4 {
		t.Errorf("expected more than the old 4-attempt budget within %s, got %d -- is it retrying, or stuck on one connection?", restartFloor, got)
	}

	// The other half of the trade: a bounded budget is only safe if a client that has somewhere
	// better to go can leave immediately. Every signalled failover path (lease eviction, drain
	// warning, failback) cancels the session context, so that cancellation must not wait out the
	// reconnect window.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the session context did not return promptly; region failover would be starved for the whole reconnect window")
	}
}

// TestChiselClientConfigCarriesReconnectPolicy pins the values RunClient actually builds,
// rather than the constants they are made of.
func TestChiselClientConfigCarriesReconnectPolicy(t *testing.T) {
	cfg := newChiselClientConfig("https://tunnel.example", "tok", []string{"R:1:2"}, 0)

	if cfg.KeepAlive != defaultChiselKeepAlive {
		t.Errorf("KeepAlive = %s, want %s -- unset means chisel never pings and a dead control channel is never noticed (#1946)", cfg.KeepAlive, defaultChiselKeepAlive)
	}
	if cfg.MaxRetryInterval != chiselMaxRetryInterval {
		t.Errorf("MaxRetryInterval = %s, want %s", cfg.MaxRetryInterval, chiselMaxRetryInterval)
	}
	if want := retryCountForWindow(defaultReconnectWindow); cfg.MaxRetryCount != want {
		t.Errorf("MaxRetryCount = %d, want %d (the default %s window)", cfg.MaxRetryCount, want, defaultReconnectWindow)
	}
	if cfg.Server != "https://tunnel.example/tunnel" {
		t.Errorf("Server = %q, want the /tunnel endpoint", cfg.Server)
	}
}

// TestChiselClientConfigHonoursAndClampsTheAdvertisedWindow covers the server-side tuning lever:
// the gateway advertises client_reconnect_seconds on /api/version, and the client honours it
// within bounds it will not be talked out of.
func TestChiselClientConfigHonoursAndClampsTheAdvertisedWindow(t *testing.T) {
	cases := []struct {
		name       string
		advertised time.Duration
		want       time.Duration
	}{
		{"nothing advertised falls back to the default", 0, defaultReconnectWindow},
		{"a negative value is not trusted", -5 * time.Second, defaultReconnectWindow},
		{"an honoured value is used as given", 120 * time.Second, 120 * time.Second},
		{"too short would undo the fix", 3 * time.Second, minReconnectWindow},
		{"too long would starve region failover", time.Hour, maxReconnectWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampReconnectWindow(tc.advertised); got != tc.want {
				t.Errorf("clampReconnectWindow(%s) = %s, want %s", tc.advertised, got, tc.want)
			}
			cfg := newChiselClientConfig("https://x", "t", nil, tc.advertised)
			if want := retryCountForWindow(tc.want); cfg.MaxRetryCount != want {
				t.Errorf("MaxRetryCount = %d, want %d", cfg.MaxRetryCount, want)
			}
		})
	}
}

// TestRetryCountForWindowMatchesChiselBackoff checks the derivation against the real backoff
// library chisel uses, not against a restatement of it.
//
// retryCountForWindow has to reproduce chisel's schedule -- `&backoff.Backoff{Max:
// MaxRetryInterval}` with Min and Factor left at the library defaults
// (chisel/client/client_connect.go:22) -- and the whole defect in #1946 was believing a
// schedule without checking it. So the expected total is summed from the library itself: a
// dependency bump that changes those defaults turns this red instead of silently shortening
// every client's reconnect window.
func TestRetryCountForWindowMatchesChiselBackoff(t *testing.T) {
	for _, window := range []time.Duration{minReconnectWindow, defaultReconnectWindow, maxReconnectWindow} {
		count := retryCountForWindow(window)

		b := &backoff.Backoff{Max: chiselMaxRetryInterval}
		var total time.Duration
		for i := 0; i < count; i++ {
			total += b.Duration()
		}
		if total < window {
			t.Errorf("%d attempts cover only %s of chisel's real backoff, short of the %s window", count, total, window)
		}

		b2 := &backoff.Backoff{Max: chiselMaxRetryInterval}
		var oneLess time.Duration
		for i := 0; i < count-1; i++ {
			oneLess += b2.Duration()
		}
		if oneLess >= window {
			t.Errorf("%d attempts already cover %s; %d is more retrying than the %s window asks for", count-1, oneLess, count, window)
		}
	}
}

// TestEngineReconnectWindowRoundTrips covers the carrier: /api/version -> engine -> the config
// RunClient builds.
func TestEngineReconnectWindowRoundTrips(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	if got := engine.ReconnectWindow(); got != 0 {
		t.Errorf("a fresh engine should carry no advertised window, got %s", got)
	}

	engine.SetReconnectWindow(90 * time.Second)
	if got := engine.ReconnectWindow(); got != 90*time.Second {
		t.Errorf("ReconnectWindow() = %s, want 90s", got)
	}

	cfg := newChiselClientConfig("https://x", "t", nil, engine.ReconnectWindow())
	if want := retryCountForWindow(90 * time.Second); cfg.MaxRetryCount != want {
		t.Errorf("the advertised window did not reach the chisel config: MaxRetryCount = %d, want %d", cfg.MaxRetryCount, want)
	}
}
