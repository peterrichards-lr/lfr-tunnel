package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// closeUpgraded closes an upgraded connection.
//
// Not `defer conn.Close()` with a //nolint, and not `_ = conn.Close()`: errcheck here runs with
// check-blank, so the blank assignment does not satisfy it, and the //nolint budget is AT its
// ceiling (make nolint-ratchet), so adding one turns a branch red on a check unrelated to its
// subject. Logged at debug instead -- the pattern the lint rules prescribe for a caller that
// genuinely cannot act on the error, which is the case here: by the time these run the echo has
// already been asserted, and a peer that closed first is the normal end of the session.
func closeUpgraded(c io.Closer) {
	if err := c.Close(); err != nil {
		slog.Debug(fmt.Sprintf("test: closing an upgraded connection: %v", err))
	}
}

// A WebSocket carried by the proxy, driven end to end.
//
// Built as a real ReverseProxy in front of a real upgrading backend, with a real client dialling
// through it, because an upgrade is the one shape a hand-built RoundTrip call cannot reproduce:
// ReverseProxy HIJACKS the connection on a 101 and copies raw bytes between the two sides, so
// nothing after the handshake goes through the transport at all (#2179).
func serveWebSocketThroughTrackingProxy(t *testing.T, payload string) (*TunnelLease, string, error) {
	t.Helper()
	return serveWebSocketThroughProxy(t, payload, true)
}

// serveWebSocketThroughProxy drives the same path with the tracking transport either installed or
// not, so the CONTROL below can establish that the fixture itself carries an upgrade.
func serveWebSocketThroughProxy(t *testing.T, payload string, tracking bool) (*TunnelLease, string, error) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer closeUpgraded(conn)
		for {
			kind, msg, rerr := conn.ReadMessage()
			if rerr != nil {
				return
			}
			if werr := conn.WriteMessage(kind, msg); werr != nil {
				return
			}
		}
	}))
	t.Cleanup(origin.Close)

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parsing the origin URL: %v", err)
	}

	lease := &TunnelLease{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if tracking {
		proxy.Transport = &trackingTransport{roundTripper: http.DefaultTransport, lease: lease}
	}

	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, res, derr := dialer.Dial("ws"+strings.TrimPrefix(front.URL, "http"), nil)
	if derr != nil {
		status := "no response"
		if res != nil {
			status = res.Status
		}
		return lease, status, derr
	}
	defer closeUpgraded(conn)

	if werr := conn.WriteMessage(websocket.TextMessage, []byte(payload)); werr != nil {
		t.Fatalf("writing through the upgraded connection: %v", werr)
	}
	if rerr := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); rerr != nil {
		t.Fatalf("setting a read deadline: %v", rerr)
	}
	_, echoed, rerr := conn.ReadMessage()
	if rerr != nil {
		t.Fatalf("reading the echo back: %v", rerr)
	}
	if string(echoed) != payload {
		t.Fatalf("the echo came back as %q, want %q", echoed, payload)
	}
	return lease, "101", nil
}

// CONTROL: the same fixture with the tracking transport left out.
//
// It has to pass both before and after the fix, and it is what makes the two tests below mean
// something. Without it "the handshake failed" is satisfied by a broken fixture -- a backend that
// does not upgrade, a dialler pointed at the wrong URL, an httptest server that closed -- which is
// exactly the §5c shape where a test reports on something nobody is watching.
func TestTheUpgradeFixtureItselfCarriesAWebSocket(t *testing.T) {
	if _, status, err := serveWebSocketThroughProxy(t, "hello", false); err != nil {
		t.Fatalf("the fixture cannot carry a WebSocket even with no tracking transport installed "+
			"(%s): %v -- the tests below would be measuring the fixture, not the transport", status, err)
	}
}

// Before anything about counting: the upgrade has to WORK.
//
// #2179 describes this as bytes going uncounted in both directions, and says that is symmetric
// and therefore skews nothing. It is worse than that: the upgrade does not happen at all.
// trackingTransport wraps res.Body in a read-only wrapper, and ReverseProxy requires a 101's body
// to be an io.ReadWriteCloser so it can hijack and copy raw bytes both ways. The type assertion
// fails, ReverseProxy calls its ErrorHandler, and the visitor gets a 502.
func TestAnUpgradeThroughTheTrackingProxySucceeds(t *testing.T) {
	_, status, err := serveWebSocketThroughTrackingProxy(t, "hello")
	if err != nil {
		t.Fatalf("the WebSocket handshake through the proxy failed (%s): %v", status, err)
	}
}

// The issue as filed: every byte after the handshake is invisible to both counters.
func TestUpgradedConnectionBytesAreCounted(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	lease, status, err := serveWebSocketThroughTrackingProxy(t, payload)
	if err != nil {
		t.Fatalf("the WebSocket handshake through the proxy failed (%s): %v", status, err)
	}

	in := atomic.LoadUint64(&lease.BytesIn)
	out := atomic.LoadUint64(&lease.BytesOut)
	if in < uint64(len(payload)) {
		t.Errorf("BytesIn is %d after sending %d bytes through an upgraded connection; the "+
			"handshake is counted but the session is not (#2179)", in, len(payload))
	}
	if out < uint64(len(payload)) {
		t.Errorf("BytesOut is %d after %d bytes were echoed back through an upgraded "+
			"connection (#2179)", out, len(payload))
	}
}
