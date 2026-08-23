package client

import (
	"testing"
	"time"
)

// TestGatewayHasNoLease is the regression test for #1146. handleTunnelStatus answers 200
// whether it updated our lease or holds none at all, and the only difference is the body:
// the update path writes nothing, the no-lease path writes {"status":"ok"}. Getting this
// backwards either leaves a dead tunnel unrecovered or re-registers a healthy one.
func TestGatewayHasNoLease(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty body means our lease was updated", "", false},
		{"whitespace only is still an empty body", "  \n", false},
		{"status ok body means no lease is held", `{"status":"ok"}`, true},
		{"status ok with trailing newline", "{\"status\":\"ok\"}\n", true},
		// Doubt resolves towards "lease present": a false positive re-registers a
		// working tunnel, a false negative only costs one more tick.
		{"unparseable body is treated as lease present", "<html>502</html>", false},
		{"different status is not the no-lease branch", `{"status":"maintenance"}`, false},
		{"no body at all", "", false},
		// A shutdown warning rides the lease-held path, so it must never look like the
		// no-lease branch -- it deliberately carries no status field (#1238).
		{"a shutdown warning still means our lease is held",
			`{"type":"node_shutdown_warning","seconds_remaining":120,"shutdown_at":1,"reason":"deploy"}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayHasNoLease([]byte(tc.body)); got != tc.want {
				t.Errorf("gatewayHasNoLease() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConsumeLeaseLost checks the read-and-clear, which must fire exactly once: the
// health-check goroutine sets it while the main loop reads it.
func TestConsumeLeaseLost(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)

	if e.ConsumeLeaseLost() {
		t.Error("a fresh engine must not report a lost lease")
	}

	e.mu.Lock()
	e.LeaseLost = true
	e.mu.Unlock()

	if !e.ConsumeLeaseLost() {
		t.Error("expected the lost lease to be reported")
	}
	if e.ConsumeLeaseLost() {
		t.Error("expected the flag to be cleared, so recovery runs once rather than every loop")
	}
}

// TestSuppressFailback is the regression test for #1145. Cooling the region down is not
// enough on its own, because the prober targets the primary directly and never consults
// the cooldown set -- it needs its own hold-off or the client flaps.
func TestSuppressFailback(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)

	if e.failbackSuppressed() {
		t.Error("a fresh engine must allow failback")
	}

	e.SuppressFailback(time.Minute)
	if !e.failbackSuppressed() {
		t.Error("expected failback to be held off after suppression")
	}

	e.SuppressFailback(-time.Second)
	if e.failbackSuppressed() {
		t.Error("expected an elapsed suppression to allow failback again")
	}
}
