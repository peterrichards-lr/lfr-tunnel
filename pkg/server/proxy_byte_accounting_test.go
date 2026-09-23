package server

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// The accounting under test is trackingTransport, but the defect it fixes only exists in
// combination with httputil.ReverseProxy: the proxy nils a bodyless request's Body before the
// transport ever sees it ("Issue 16036"), so a body-only measurement records nothing for a GET.
//
// These tests therefore drive a REAL ReverseProxy rather than calling RoundTrip directly. Calling
// it directly would hand the transport a non-nil Body and pass against the broken code -- the
// exact shape §5c warns about, an assertion satisfied by the wrong arrangement.
func serveThroughTrackingProxy(t *testing.T, backend http.HandlerFunc, req func(base string) *http.Request) *TunnelLease {
	t.Helper()

	origin := httptest.NewServer(backend)
	t.Cleanup(origin.Close)

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("parsing the origin URL: %v", err)
	}

	lease := &TunnelLease{}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &trackingTransport{
		roundTripper: http.DefaultTransport,
		lease:        lease,
	}

	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)

	res, err := front.Client().Do(req(front.URL))
	if err != nil {
		t.Fatalf("issuing the request: %v", err)
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			t.Errorf("closing the response body: %v", cerr)
		}
	}()

	// The body must be drained here: BytesOut accrues through a wrapper on res.Body, so a test
	// that never reads it measures the headers alone and would miss a body-counting regression.
	if _, err := io.Copy(io.Discard, res.Body); err != nil {
		t.Fatalf("draining the response body: %v", err)
	}
	return lease
}

// The regression. Before the fix this recorded exactly zero, which is what made "Data In" a flat
// line in both portals (#2177).
//
// Asserting a floor computed from the request line and Host header, not merely "> 0": a stray
// byte from some other source would satisfy "> 0" and leave the defect in place.
func TestBytesInCountsABodylessGET(t *testing.T) {
	const path = "/a/path/long/enough/to/measure"

	lease := serveThroughTrackingProxy(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		},
		func(base string) *http.Request {
			req, err := http.NewRequest(http.MethodGet, base+path, nil)
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			return req
		},
	)

	got := atomic.LoadUint64(&lease.BytesIn)
	if got == 0 {
		t.Fatal("BytesIn is zero for a GET: the request line and headers were not counted, which is #2177 exactly")
	}

	// "GET <path> HTTP/1.1\r\n" plus "Host: 127.0.0.1:NNNNN\r\n" plus the terminating blank line.
	floor := len(http.MethodGet) + 1 + len(path) + 1 + len("HTTP/1.1") + 2 + len("Host: ") + len("127.0.0.1:0") + 2 + 2
	if got < uint64(floor) {
		t.Fatalf("BytesIn = %d, below the %d bytes the request line and Host header alone occupy", got, floor)
	}
}

// A request body must still be counted, and counted ON TOP OF the headers rather than instead of
// them -- the naive repair of #2177 is to move the accounting and lose one of the two.
func TestBytesInCountsARequestBodyOnTopOfItsHeaders(t *testing.T) {
	body := strings.Repeat("x", 4096)

	lease := serveThroughTrackingProxy(t,
		func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("reading the request body at the origin: %v", err)
			}
			_, _ = w.Write([]byte("ok"))
		},
		func(base string) *http.Request {
			req, err := http.NewRequest(http.MethodPost, base+"/upload", strings.NewReader(body))
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			return req
		},
	)

	got := atomic.LoadUint64(&lease.BytesIn)
	if got <= uint64(len(body)) {
		t.Fatalf("BytesIn = %d, which does not exceed the %d-byte body: one of the two contributions went uncounted -- the body wrapper, or the request headers", got, len(body))
	}
}

// Data Out has always counted response bodies; it counts headers now too, because the two series
// are charted against each other and summed into one quota.
func TestBytesOutCountsResponseHeadersNotOnlyTheBody(t *testing.T) {
	body := "0123456789"

	lease := serveThroughTrackingProxy(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-A-Deliberately-Long-Header-Name", strings.Repeat("v", 128))
			_, _ = w.Write([]byte(body))
		},
		func(base string) *http.Request {
			req, err := http.NewRequest(http.MethodGet, base+"/", nil)
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			return req
		},
	)

	got := atomic.LoadUint64(&lease.BytesOut)
	if got <= uint64(len(body)) {
		t.Fatalf("BytesOut = %d, which is not more than the %d-byte body: response headers were not counted", got, len(body))
	}
	if got <= 128 {
		t.Fatalf("BytesOut = %d, less than the single 128-byte header value the origin set", got)
	}
}

// The arithmetic in requestWireHeaderBytes is checked against net/http's OWN serialiser rather
// than against a second copy of the same sum written in the test, which would only prove the two
// copies agree (§5c rule 4: test production, not a mirror of it).
//
// Request.Write emits the request line, Host, the headers and the blank line -- precisely the
// span requestWireHeaderBytes claims to measure -- so for a bodyless request the two are directly
// comparable.
func TestRequestWireHeaderBytesAgreesWithNetHTTPsOwnSerialiser(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.test/some/path?q=1", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("User-Agent", "lfr-tunnel-test")
	req.Header.Set("Accept", "text/html")
	req.Header.Add("Cookie", "a=1")
	req.Header.Add("Cookie", "b=2")

	var buf bytes.Buffer
	if err := req.Write(&buf); err != nil {
		t.Fatalf("serialising the request: %v", err)
	}

	want := buf.Len()
	if got := requestWireHeaderBytes(req); got != want {
		t.Fatalf("requestWireHeaderBytes = %d, net/http wrote %d bytes:\n%s", got, want, buf.String())
	}
}

// Same oracle for the response side.
func TestResponseWireHeaderBytesAgreesWithNetHTTPsOwnSerialiser(t *testing.T) {
	raw := "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nX-Trace: abc123\r\n\r\n"

	res, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	if err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			t.Errorf("closing the response body: %v", cerr)
		}
	}()

	if got := responseWireHeaderBytes(res); got != len(raw) {
		t.Fatalf("responseWireHeaderBytes = %d, the response occupies %d bytes on the wire:\n%s", got, len(raw), raw)
	}
}

// A nil request or response must measure zero rather than panicking: RoundTrip counts before it
// checks anything, and an ErrorHandler path can reach here with neither.
func TestWireHeaderBytesTolerateNil(t *testing.T) {
	if got := requestWireHeaderBytes(nil); got != 0 {
		t.Fatalf("requestWireHeaderBytes(nil) = %d, want 0", got)
	}
	if got := responseWireHeaderBytes(nil); got != 0 {
		t.Fatalf("responseWireHeaderBytes(nil) = %d, want 0", got)
	}
}
