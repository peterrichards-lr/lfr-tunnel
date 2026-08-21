package client

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func bodyResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestGatewayHasNoLease is the regression test for #1146. handleTunnelStatus answers 200
// whether it updated our lease or holds none at all, and the only difference is the body:
// the update path writes nothing, the no-lease path writes {"status":"ok"}. Getting this
// backwards either leaves a dead tunnel unrecovered or re-registers a healthy one.
func TestGatewayHasNoLease(t *testing.T) {
	cases := []struct {
		name string
		resp *http.Response
		want bool
	}{
		{"empty body means our lease was updated", bodyResponse(""), false},
		{"whitespace only is still an empty body", bodyResponse("  \n"), false},
		{"status ok body means no lease is held", bodyResponse(`{"status":"ok"}`), true},
		{"status ok with trailing newline", bodyResponse("{\"status\":\"ok\"}\n"), true},
		// Doubt resolves towards "lease present": a false positive re-registers a
		// working tunnel, a false negative only costs one more tick.
		{"unparseable body is treated as lease present", bodyResponse("<html>502</html>"), false},
		{"different status is not the no-lease branch", bodyResponse(`{"status":"maintenance"}`), false},
		{"nil response", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayHasNoLease(tc.resp); got != tc.want {
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
