package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func probeServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestProbeGatewayRecordsWhatItSaw is the point of this change: a failback decision has to
// be explainable afterwards. A client failed back onto an edge that was mid-reboot and the
// logs carried nothing about what the prober had seen to justify it.
func TestProbeGatewayRecordsWhatItSaw(t *testing.T) {
	ctx := t.Context()

	t.Run("connected edge", func(t *testing.T) {
		p := probeGateway(ctx, probeServer(t, 200, `{"status":"healthy","control_plane":"connected"}`), time.Second)
		if !p.Usable {
			t.Error("expected usable")
		}
		if p.StatusCode != 200 || p.ControlPlane != "connected" {
			t.Errorf("expected the status and control_plane to be recorded, got %+v", p)
		}
		if !strings.Contains(p.Reason(), "connected") {
			t.Errorf("reason should name the control plane state, got %q", p.Reason())
		}
	})

	t.Run("disconnected edge", func(t *testing.T) {
		p := probeGateway(ctx, probeServer(t, 200, `{"status":"degraded","control_plane":"disconnected"}`), time.Second)
		if p.Usable {
			t.Error("a disconnected edge must not be usable")
		}
		if p.Reason() != "control plane disconnected" {
			t.Errorf("got %q", p.Reason())
		}
	})

	t.Run("gateway error keeps the status code", func(t *testing.T) {
		p := probeGateway(ctx, probeServer(t, 502, `<html>502</html>`), time.Second)
		if p.Usable {
			t.Error("a 502 must not be usable")
		}
		if p.StatusCode != 502 || p.Reason() != "http 502" {
			t.Errorf("expected the code recorded, got %+v reason=%q", p, p.Reason())
		}
	})

	// This is the fallback most likely to be wrong -- an unparseable 200 counts as
	// healthy -- so the body is kept precisely for this case.
	t.Run("unparseable 200 keeps the body for diagnosis", func(t *testing.T) {
		p := probeGateway(ctx, probeServer(t, 200, `<html>maintenance</html>`), time.Second)
		if !p.Usable {
			t.Error("an unparseable 200 is deliberately treated as healthy")
		}
		if !strings.Contains(p.Body, "maintenance") {
			t.Errorf("expected the body kept so this decision can be reviewed, got %q", p.Body)
		}
		if !strings.Contains(p.Reason(), "no control_plane reported") {
			t.Errorf("reason should say the field was absent, got %q", p.Reason())
		}
	})

	t.Run("a parsed response does not keep the body", func(t *testing.T) {
		p := probeGateway(ctx, probeServer(t, 200, `{"status":"healthy","control_plane":"connected"}`), time.Second)
		if p.Body != "" {
			t.Errorf("body is only worth keeping when it did not parse, got %q", p.Body)
		}
	})

	t.Run("unreachable gateway", func(t *testing.T) {
		p := probeGateway(ctx, "http://127.0.0.1:1", time.Second)
		if p.Usable {
			t.Error("unreachable must not be usable")
		}
		if p.Err == "" || !strings.HasPrefix(p.Reason(), "unreachable") {
			t.Errorf("expected the transport error recorded, got %+v", p)
		}
	})
}

// TestProbeGatewayHealthStillAgrees confirms the boolean wrapper did not drift from the
// detailed probe when it was split out.
func TestProbeGatewayHealthStillAgrees(t *testing.T) {
	ctx := t.Context()
	for _, body := range []string{
		`{"control_plane":"connected"}`,
		`{"control_plane":"disconnected"}`,
		`{"status":"healthy"}`,
	} {
		url := probeServer(t, 200, body)
		if probeGatewayHealth(ctx, url, time.Second) != probeGateway(ctx, url, time.Second).Usable {
			t.Errorf("wrapper disagrees with the probe for body %s", body)
		}
	}
}
