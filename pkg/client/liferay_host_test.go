package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// #2561. Liferay's 500 body is generic, so detection is a differential probe: the same GET with
// the local Host. These backends stand in for Liferay by refusing one Host and serving another;
// none of them return anything a body match could key on.

const rejectedPublicHost = "dev-tomcat.lfr-demo.se"

// hostBackend answers 500 to every Host in refuse and 200 otherwise, counting requests that did
// NOT carry a refused Host (the probe's local-Host request, in these tests).
type hostBackend struct {
	srv        *httptest.Server
	port       int
	otherHosts atomic.Int32
}

func newHostBackend(t *testing.T, refuseAll bool, refuse ...string) *hostBackend {
	t.Helper()
	b := &hostBackend{}
	refused := map[string]bool{}
	for _, h := range refuse {
		refused[h] = true
	}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := splitHostPortLoose(host); err == nil {
			host = h
		}
		if !refused[host] {
			b.otherHosts.Add(1)
		}
		if refuseAll || refused[host] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(b.srv.Close)
	u, err := url.Parse(b.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if b.port, err = strconv.Atoi(u.Port()); err != nil {
		t.Fatal(err)
	}
	return b
}

func splitHostPortLoose(hp string) (string, string, error) {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return hp, "", fmt.Errorf("no port")
	}
	return hp[:i], hp[i+1:], nil
}

func TestProbeHostAcceptance(t *testing.T) {
	t.Run("500 for the public host, 200 for the local one: rejected", func(t *testing.T) {
		b := newHostBackend(t, false, rejectedPublicHost)
		r := ProbeHostAcceptance(context.Background(), http.DefaultTransport, b.srv.URL, "/", rejectedPublicHost, "127.0.0.1:"+strconv.Itoa(b.port))
		if r.Verdict != HostRejected || r.PublicStatus != 500 || r.LocalStatus != 200 {
			t.Errorf("got %+v, want HostRejected with 500/200", r)
		}
	})
	t.Run("500 for both: unknown, not rejected", func(t *testing.T) {
		b := newHostBackend(t, true)
		r := ProbeHostAcceptance(context.Background(), http.DefaultTransport, b.srv.URL, "/", rejectedPublicHost, "127.0.0.1:"+strconv.Itoa(b.port))
		if r.Verdict != HostAcceptanceUnknown || !strings.Contains(r.Detail, "also got a 500") {
			t.Errorf("got %+v, want unknown because the local host also got a 500", r)
		}
	})
	t.Run("the public host is served: accepted", func(t *testing.T) {
		b := newHostBackend(t, false)
		r := ProbeHostAcceptance(context.Background(), http.DefaultTransport, b.srv.URL, "/", rejectedPublicHost, "127.0.0.1")
		if r.Verdict != HostAccepted || r.PublicStatus != 200 {
			t.Errorf("got %+v, want HostAccepted", r)
		}
	})
	t.Run("nothing listening: unknown", func(t *testing.T) {
		b := newHostBackend(t, false)
		b.srv.Close()
		r := ProbeHostAcceptance(context.Background(), http.DefaultTransport, b.srv.URL, "/", rejectedPublicHost, "127.0.0.1")
		if r.Verdict != HostAcceptanceUnknown || r.Detail == "" {
			t.Errorf("got %+v, want unknown with a reason", r)
		}
	})
}

func TestLiferayHostHint_NamesBothFixesAndTheWildcard(t *testing.T) {
	hint := LiferayHostHint(rejectedPublicHost + ":443")
	for _, want := range []string{
		"Control Panel > Instance Settings > Virtual Hosts",
		"virtual.hosts.valid.hosts",
		"*.lfr-demo.se",
		"set dev-tomcat.lfr-demo.se as a Virtual Host",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint does not contain %q:\n%s", want, hint)
		}
	}
	if strings.Contains(LiferayHostHint("10.0.0.5"), "*.") {
		t.Error("an IP address was given a wildcard")
	}
}

// captureHints replaces the hint printer for one test.
func captureHints(t *testing.T) chan string {
	t.Helper()
	ch := make(chan string, 10)
	old := emitHostRejectionHint
	emitHostRejectionHint = func(msg string) { ch <- msg }
	t.Cleanup(func() { emitHostRejectionHint = old })
	return ch
}

// getThrough sends a GET with the given Host through the interceptor and returns the status
// the visitor saw.
func getThrough(t *testing.T, interceptPort int, host string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/web/guest", interceptPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request through interceptor: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	return resp.StatusCode
}

func liferayEngine(t *testing.T, port int, preserve bool) int {
	t.Helper()
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetLiferayPorts([]int{port})
	if preserve {
		engine.SetPreserveHostPorts([]int{port})
	}
	listener, err := engine.InterceptPort(port)
	if err != nil {
		t.Fatalf("InterceptPort: %v", err)
	}
	return listener
}

// At runtime: one hint per hostname, however many requests fail, and the visitor still gets
// Liferay's own 500.
func TestRuntimeHostRejection_HintsOncePerHostname(t *testing.T) {
	hints := captureHints(t)
	b := newHostBackend(t, false, rejectedPublicHost)
	listener := liferayEngine(t, b.port, true)

	for i := 0; i < 3; i++ {
		if status := getThrough(t, listener, rejectedPublicHost); status != http.StatusInternalServerError {
			t.Fatalf("visitor saw %d, want Liferay's own 500 passed through unchanged", status)
		}
	}

	select {
	case hint := <-hints:
		for _, want := range []string{rejectedPublicHost, "Invalid host name", "virtual.hosts.valid.hosts", "Virtual Hosts"} {
			if !strings.Contains(hint, want) {
				t.Errorf("hint does not contain %q:\n%s", want, hint)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hint after three 500s for a hostname the local Host is served for")
	}
	// Exactly one probe request reached the backend with the local Host.
	time.Sleep(200 * time.Millisecond)
	if n := b.otherHosts.Load(); n != 1 {
		t.Errorf("the probe ran %d times for one hostname, want 1", n)
	}
	select {
	case extra := <-hints:
		t.Errorf("a second hint for the same hostname: %s", extra)
	default:
	}
}

// A target that 500s for every Host is broken for another reason: no hint.
func TestRuntimeHostRejection_NoHintWhenTheLocalHostAlso500s(t *testing.T) {
	hints := captureHints(t)
	b := newHostBackend(t, true)
	listener := liferayEngine(t, b.port, true)

	getThrough(t, listener, rejectedPublicHost)
	// The probe's local-Host request is the signal that it ran (refuseAll still counts it).
	if !waitFor(func() bool { return b.otherHosts.Load() >= 2 }) {
		t.Fatalf("the probe never reached the backend (%d requests)", b.otherHosts.Load())
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case hint := <-hints:
		t.Errorf("hinted although the local Host also got a 500:\n%s", hint)
	default:
	}
}

// Without preserve_host Liferay never saw the public hostname, so a 500 cannot be a refusal of
// it, and no probe is sent.
func TestRuntimeHostRejection_NotProbedWithoutPreserveHost(t *testing.T) {
	captureHints(t)
	// Every request is answered 500 and counted, so a probe would show up as a second request.
	b := newHostBackend(t, true)
	listener := liferayEngine(t, b.port, false)

	if status := getThrough(t, listener, rejectedPublicHost); status != http.StatusInternalServerError {
		t.Fatalf("fixture: the rewritten request should have got the backend's 500, got %d", status)
	}
	time.Sleep(300 * time.Millisecond)
	if n := b.otherHosts.Load(); n != 1 {
		t.Errorf("the backend saw %d requests for one visitor request; a probe was sent although "+
			"preserve_host is off for the port", n)
	}
}
