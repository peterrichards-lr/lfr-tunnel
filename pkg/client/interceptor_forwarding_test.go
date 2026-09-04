package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// Characterisation tests for the interceptor's reverse proxy, written against the
// Director-based implementation before the migration to Rewrite (#1704) and expected to
// pass unchanged after it.
//
// They pin the three things the migration could silently lose:
//
//   - the X-Forwarded-For chain, which httputil.ReverseProxy appends to for a Director
//     but not for a Rewrite;
//   - the inbound X-Forwarded-Host / X-Forwarded-Proto, which ReverseProxy strips before
//     calling a Rewrite and which interceptorTransport.RoundTrip reads off the *outbound*
//     request to rewrite Location headers and cookie domains (interceptor.go:1141);
//   - Host, for both values of the user-facing PreserveHost option.

// captureTarget starts a backend that records what the proxy actually sent it.
func captureTarget(t *testing.T) (*httptest.Server, *http.Request, int) {
	t.Helper()

	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		got.Host = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse backend port: %v", err)
	}
	return srv, &got, port
}

func TestInterceptPort_ForwardingHeadersAndHost(t *testing.T) {
	cases := []struct {
		name         string
		preserveHost bool
		inboundHost  string
		inboundXFF   string
		wantHost     func(targetPort int) string
		wantXFF      string
	}{
		{
			name:         "preserve host, no inbound XFF",
			preserveHost: true,
			inboundHost:  "demo.lfr-demo.se",
			wantHost:     func(int) string { return "demo.lfr-demo.se" },
			// ReverseProxy appends the connecting peer -- the local test client.
			wantXFF: "127.0.0.1",
		},
		{
			name:         "preserve host, inbound XFF is extended not replaced",
			preserveHost: true,
			inboundHost:  "demo.lfr-demo.se",
			inboundXFF:   "203.0.113.7",
			wantHost:     func(int) string { return "demo.lfr-demo.se" },
			wantXFF:      "203.0.113.7, 127.0.0.1",
		},
		{
			name:         "rewrite host to the local target",
			preserveHost: false,
			inboundHost:  "demo.lfr-demo.se",
			inboundXFF:   "203.0.113.7, 198.51.100.9",
			wantHost: func(targetPort int) string {
				return fmt.Sprintf("127.0.0.1:%d", targetPort)
			},
			wantXFF: "203.0.113.7, 198.51.100.9, 127.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got, targetPort := captureTarget(t)

			engine := NewInterceptorEngine("127.0.0.1", []string{"X-Custom-Injected: yes"})
			engine.PreserveHost = tc.preserveHost

			interceptPort, err := engine.InterceptPort(targetPort)
			if err != nil {
				t.Fatalf("InterceptPort: %v", err)
			}

			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/web/guest", interceptPort), nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Host = tc.inboundHost
			req.Header.Set("X-Forwarded-Host", "public.lfr-demo.se")
			req.Header.Set("X-Forwarded-Proto", "https")
			if tc.inboundXFF != "" {
				req.Header.Set("X-Forwarded-For", tc.inboundXFF)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request through interceptor: %v", err)
			}
			_ = resp.Body.Close() //nolint:errcheck

			if want := tc.wantHost(targetPort); got.Host != want {
				t.Errorf("Host: got %q, want %q", got.Host, want)
			}
			if v := got.Header.Get("X-Forwarded-For"); v != tc.wantXFF {
				t.Errorf("X-Forwarded-For: got %q, want %q", v, tc.wantXFF)
			}
			// The interceptor is not the edge, so it must pass the public host and scheme
			// through untouched rather than overwriting them with its own local view.
			if v := got.Header.Get("X-Forwarded-Host"); v != "public.lfr-demo.se" {
				t.Errorf("X-Forwarded-Host: got %q, want %q", v, "public.lfr-demo.se")
			}
			if v := got.Header.Get("X-Forwarded-Proto"); v != "https" {
				t.Errorf("X-Forwarded-Proto: got %q, want %q", v, "https")
			}
			if v := got.Header.Get("X-Custom-Injected"); v != "yes" {
				t.Errorf("configured header X-Custom-Injected: got %q, want %q", v, "yes")
			}
			if got.URL.Path != "/web/guest" {
				t.Errorf("path: got %q, want %q", got.URL.Path, "/web/guest")
			}
		})
	}
}

// TestInterceptPort_QueryStringPreserved pins the URL rewriting that
// NewSingleHostReverseProxy's Director performed, which a Rewrite has to reimplement.
func TestInterceptPort_QueryStringPreserved(t *testing.T) {
	_, got, targetPort := captureTarget(t)

	engine := NewInterceptorEngine("127.0.0.1", nil)
	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("InterceptPort: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/search?q=liferay&page=2", interceptPort)) //nolint:noctx
	if err != nil {
		t.Fatalf("request through interceptor: %v", err)
	}
	_ = resp.Body.Close() //nolint:errcheck

	if got.URL.Path != "/api/search" {
		t.Errorf("path: got %q, want %q", got.URL.Path, "/api/search")
	}
	if got.URL.RawQuery != "q=liferay&page=2" {
		t.Errorf("query: got %q, want %q", got.URL.RawQuery, "q=liferay&page=2")
	}
}
