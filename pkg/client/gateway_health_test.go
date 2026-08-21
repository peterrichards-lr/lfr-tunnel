package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestGatewayCanCarrySession is the regression test for #1165. An edge whose control
// channel to central is down answers /api/healthz perfectly well, so electing on the
// status code alone put clients onto a gateway that evicted them seconds later.
func TestGatewayCanCarrySession(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"connected edge is usable", 200, `{"status":"healthy","control_plane":"connected"}`, true},
		{"disconnected edge is not usable", 200, `{"status":"degraded","control_plane":"disconnected"}`, false},

		// Central has no upstream, and gateways from before the field existed send
		// nothing. Absence must read as healthy or an older fleet stops being elected.
		{"central omits the field", 200, `{"status":"healthy"}`, true},
		{"empty object", 200, `{}`, true},

		// Doubt resolves towards usable: refusing on a body we merely failed to parse
		// would strand a client with nowhere to go.
		{"unparseable body on a 200", 200, `<html>ok</html>`, true},
		{"empty body on a 200", 200, ``, true},

		{"non-200 is never usable", 503, `{"control_plane":"connected"}`, false},
		{"500 is not usable", 500, ``, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GatewayCanCarrySession(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("GatewayCanCarrySession(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestProbeGatewayHealthAgainstLiveServers exercises the fetch path end to end, since the
// bug was that nothing fetched the field at all.
func TestProbeGatewayHealthAgainstLiveServers(t *testing.T) {
	connected := healthzServer(t, 200, `{"status":"healthy","control_plane":"connected"}`)
	disconnected := healthzServer(t, 200, `{"status":"degraded","control_plane":"disconnected"}`)
	legacy := healthzServer(t, 200, `{"status":"healthy"}`)

	ctx := t.Context()

	if !probeGatewayHealth(ctx, connected, time.Second) {
		t.Error("a connected edge must be usable")
	}
	if probeGatewayHealth(ctx, disconnected, time.Second) {
		t.Error("an edge that cannot reach central must not be elected -- this is the whole bug")
	}
	if !probeGatewayHealth(ctx, legacy, time.Second) {
		t.Error("a gateway that does not report the field must keep working")
	}
	if probeGatewayHealth(ctx, "http://127.0.0.1:1", time.Second) {
		t.Error("an unreachable gateway must not be usable")
	}
}

func healthzServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
