package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRegisterTunnelTimesOut is the regression test for #1257. RegisterTunnel used bare
// http.Post, i.e. http.DefaultClient, which has no timeout. A gateway whose host is powered
// off drops packets rather than refusing the connection, so the call sat in the OS TCP
// retry cycle for over a minute -- and because the TUI only starts after registration
// succeeds, the client printed nothing at all in that window and looked hung.
//
// Edge nodes in this deployment power off nightly, so an unreachable gateway is routine.
func TestRegisterTunnelTimesOut(t *testing.T) {
	// A server that accepts the connection and then never answers, which is the shape of
	// the failure without needing an unroutable address (those behave differently across
	// CI platforms and can take the full OS timeout to fail).
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	// Defer order matters and is LIFO: Close() blocks until in-flight requests finish, so
	// the handler has to be released first or the test deadlocks on its own cleanup.
	defer server.Close()
	defer close(blocked)

	// Swap in a short timeout so the test does not pay registerTimeout to prove the point.
	original := registerClient
	registerClient = &http.Client{Timeout: 150 * time.Millisecond}
	defer func() { registerClient = original }()

	start := time.Now()
	_, err := RegisterTunnel(server.URL, "token", "sub", "", []PortMapping{{LocalPort: 8080}}, 0, "", nil, "linux", "", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from an unresponsive gateway")
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected the call to be bounded, took %v", elapsed)
	}

	// The prefix is load-bearing: attemptRegistration in cmd/lfr-tunnel matches on this
	// substring to decide the failure is worth retrying against a different region. Adding
	// detail to the message must not break that classification.
	if !strings.Contains(err.Error(), "registration request failed") {
		t.Errorf("error must retain the classification prefix, got: %v", err)
	}
}

// TestRegisterTunnelErrorNamesTheGateway checks that the failure says which host it could
// not reach. Reporting only "registration request failed" gives no way to tell a pinned
// gateway apart from an elected one when diagnosing.
func TestRegisterTunnelErrorNamesTheGateway(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	// Defer order matters and is LIFO: Close() blocks until in-flight requests finish, so
	// the handler has to be released first or the test deadlocks on its own cleanup.
	defer server.Close()
	defer close(blocked)

	original := registerClient
	registerClient = &http.Client{Timeout: 150 * time.Millisecond}
	defer func() { registerClient = original }()

	_, err := RegisterTunnel(server.URL, "token", "sub", "", []PortMapping{{LocalPort: 8080}}, 0, "", nil, "linux", "", "")
	if err == nil {
		t.Fatal("expected a timeout error")
	}

	host := strings.TrimPrefix(server.URL, "http://")
	if !strings.Contains(err.Error(), host) {
		t.Errorf("expected the error to name the gateway %q, got: %v", host, err)
	}
}
