package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// liferayStub answers like Liferay's MainServlet: the Liferay-Portal header on every response,
// and -- like ValidHostNameFilter -- a header-less 500 for a Host it has not accepted, so a probe
// that sent the public Host would get the wrong answer.
func liferayStub(t *testing.T, status int) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := net.SplitHostPort(r.Host); host != "127.0.0.1" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Liferay-Portal", "Liferay Digital Experience Platform")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, portOf(t, srv)
}

func portOf(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func plainStub(t *testing.T, status int) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return portOf(t, srv)
}

func TestProbeLiferay_TheHeaderIdentifiesLiferay(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound} { // measured: / is 200, /c a 302
		_, port := liferayStub(t, status)
		verdict, header := ProbeLiferay(context.Background(), nil, "127.0.0.1", port)
		if verdict != LiferayIdentified || header != "Liferay Digital Experience Platform" {
			t.Errorf("status %d: verdict %v header %q, want identified with the product name", status, verdict, header)
		}
	}
}

// A server that is up and answers without the header is not Liferay -- the case that fixes a
// Gradle project's 8080 being taken for one.
func TestProbeLiferay_AnUpServerWithoutTheHeaderIsNotLiferay(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusUnauthorized} {
		port := plainStub(t, status)
		if verdict, _ := ProbeLiferay(context.Background(), nil, "127.0.0.1", port); verdict != LiferayNotIdentified {
			t.Errorf("status %d without the header: verdict %v, want not identified", status, verdict)
		}
	}
}

// 404 and 5xx can be Tomcat answering before the portal is deployed, and no answer at all is
// Liferay not started yet (measured: DXP refuses connections until it is ready). Neither is a
// verdict.
func TestProbeLiferay_NoAnswerOrStartingIsUnknown(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		port := plainStub(t, status)
		if verdict, _ := ProbeLiferay(context.Background(), nil, "127.0.0.1", port); verdict != LiferayUnknown {
			t.Errorf("status %d without the header: verdict %v, want unknown", status, verdict)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if verdict, _ := ProbeLiferay(context.Background(), nil, "127.0.0.1", closed); verdict != LiferayUnknown {
		t.Errorf("closed port: verdict %v, want unknown", verdict)
	}
}

// The watch keeps asking a port that is not answering yet, and reports it once it does: the
// tunnel-started-beside-a-booting-Liferay case.
func TestLiferayWatch_IdentifiesALiferayThatStartsLater(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // still starting
			return
		}
		w.Header().Set("Liferay-Portal", "Liferay Digital Experience Platform")
	}))
	t.Cleanup(srv.Close)
	port := portOf(t, srv)

	var got []int
	left := LiferayWatch{
		Host: "127.0.0.1", Ports: []int{port}, Interval: time.Millisecond, Deadline: 5 * time.Second,
		OnIdentified: func(p int, _ string) { got = append(got, p) },
	}.Run(context.Background())
	if len(got) != 1 || got[0] != port || len(left) != 0 {
		t.Errorf("identified %v, left %v, want [%d] and none left", got, left, port)
	}
	if calls.Load() < 3 {
		t.Errorf("only %d probes; the watch gave up before the server started", calls.Load())
	}
}

// The watch is bounded: a port that never answers is given up on, not probed forever.
func TestLiferayWatch_GivesUpAtTheDeadline(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	left := LiferayWatch{Host: "127.0.0.1", Ports: []int{closed}, Interval: 10 * time.Millisecond,
		Deadline: 100 * time.Millisecond}.Run(context.Background())
	if len(left) != 1 || left[0] != closed {
		t.Errorf("left %v, want [%d]", left, closed)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the watch ran %s past a 100ms deadline", elapsed)
	}
}

func TestMarkAndUnmarkLiferayPort(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.MarkLiferayPort(8080, true)
	if !e.IsLiferayPort(8080) || !e.PreservesHostFor(8080) || e.PreservesHostFor(3000) {
		t.Fatal("marking 8080 with preserve did not turn it on for 8080 alone")
	}
	e.MarkLiferayPort(8081, false)
	if !e.IsLiferayPort(8081) || e.PreservesHostFor(8081) {
		t.Error("marking without preserve must identify the port and leave its Host rewritten")
	}
	e.UnmarkLiferayPort(8080)
	if e.IsLiferayPort(8080) || e.PreservesHostFor(8080) {
		t.Error("unmarking left 8080 identified or preserving the Host")
	}
}
