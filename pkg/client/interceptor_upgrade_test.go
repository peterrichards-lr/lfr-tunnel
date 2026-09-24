package client

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A WebSocket carried by the client's own interceptor, driven end to end (#2213).
//
// This drives PRODUCTION's assembly -- NewInterceptorEngine and InterceptPort -- rather than a
// hand-built ReverseProxy, because the defect lives in how those two are wired together and a
// copy would only prove the copy works (§5c rule 4). An upgrade is also the one shape a direct
// RoundTrip call cannot reproduce: ReverseProxy HIJACKS the connection on a 101 and copies raw
// bytes, so nothing after the handshake goes through the transport at all.
func serveWebSocketThroughInterceptor(t *testing.T, intercept bool, payload string) (string, error) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer closeUpgradedConn(conn)
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

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parsing the origin URL: %v", err)
	}
	_, portStr, err := net.SplitHostPort(originURL.Host)
	if err != nil {
		t.Fatalf("splitting the origin host %q: %v", originURL.Host, err)
	}
	originPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing the origin port %q: %v", portStr, err)
	}

	// CONTROL: dial the backend directly, with no interceptor in the path at all.
	dialPort := originPort
	if intercept {
		engine := NewInterceptorEngine("127.0.0.1", nil)
		dialPort, err = engine.InterceptPort(originPort)
		if err != nil {
			t.Fatalf("starting the interceptor: %v", err)
		}
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, res, derr := dialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", dialPort), nil)
	if derr != nil {
		status := "no response"
		if res != nil {
			status = res.Status
		}
		return status, derr
	}
	defer closeUpgradedConn(conn)

	if werr := conn.WriteMessage(websocket.TextMessage, []byte(payload)); werr != nil {
		t.Fatalf("writing through the upgraded connection: %v", werr)
	}
	if rerr := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); rerr != nil {
		t.Fatalf("setting a read deadline: %v", rerr)
	}
	_, echoed, rerr := conn.ReadMessage()
	if rerr != nil {
		return "101", fmt.Errorf("reading the echo back: %w", rerr)
	}
	if string(echoed) != payload {
		t.Fatalf("the echo came back as %q, want %q", echoed, payload)
	}
	return "101", nil
}

// closeUpgradedConn closes a connection at the end of a test.
//
// Not `defer c.Close()` with a //nolint: errcheck here runs with check-blank (so `_ = c.Close()`
// does not satisfy it either) and the //nolint budget is AT its ceiling, so adding one turns a
// branch red on a check unrelated to its subject. Logged at debug, the pattern the lint rules
// prescribe where the caller genuinely cannot act -- by the time these run the echo has already
// been asserted, and a peer that closed first is the normal end of the session.
func closeUpgradedConn(c io.Closer) {
	if err := c.Close(); err != nil {
		slog.Debug(fmt.Sprintf("test: closing an upgraded connection: %v", err))
	}
}

// CONTROL: the backend upgrades perfectly well with no interceptor in the path.
//
// It must pass before and after the fix, and it is what makes the test below mean anything:
// without it, "the handshake failed" is equally satisfied by a backend that does not upgrade, a
// dialler pointed at the wrong port, or an httptest server that already closed.
func TestTheInterceptorUpgradeFixtureItselfCarriesAWebSocket(t *testing.T) {
	if status, err := serveWebSocketThroughInterceptor(t, false, "hello"); err != nil {
		t.Fatalf("the fixture cannot carry a WebSocket with NO interceptor in the path (%s): %v "+
			"-- the test below would be measuring the fixture, not the interceptor", status, err)
	}
}

// The defect: interceptorTransport.RoundTrip replaces res.Body with a struct{io.Reader; io.Closer}
// to capture a preview for the Inspector. On a 101 that body is also the backend CONNECTION, and
// ReverseProxy type-asserts it to io.ReadWriteCloser before hijacking -- so the assertion fails
// and the visitor gets a 502. It also io.ReadAll's up to 10KB off the live connection first.
func TestAWebSocketSurvivesTheClientInterceptor(t *testing.T) {
	status, err := serveWebSocketThroughInterceptor(t, true, "hello")
	if err != nil {
		t.Fatalf("a WebSocket through the client's interceptor failed (%s): %v\n\n"+
			"An upgraded connection is not a body. Capturing a preview of it, or wrapping it in "+
			"anything that is not an io.ReadWriteCloser, stops ReverseProxy hijacking it and the "+
			"developer's WebSocket app is unreachable through the tunnel (#2213).", status, err)
	}
}
