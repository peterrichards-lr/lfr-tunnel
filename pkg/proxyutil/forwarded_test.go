package proxyutil

import (
	"context"
	"net/http"
	"net/http/httputil"
	"testing"
)

// newProxyRequest builds the pair of requests ReverseProxy hands to a Rewrite function:
// In is the request as it arrived, Out is the clone with the four forwarding headers
// already deleted.
func newProxyRequest(remoteAddr string, inbound http.Header) *httputil.ProxyRequest {
	in := &http.Request{
		RemoteAddr: remoteAddr,
		Host:       "demo.lfr-demo.se",
		Header:     inbound,
	}
	out := in.Clone(context.Background())
	for _, h := range forwardedHeaders {
		out.Header.Del(h)
	}
	return &httputil.ProxyRequest{In: in, Out: out}
}

func TestRestoreInboundForwarded(t *testing.T) {
	pr := newProxyRequest("198.51.100.25:5000", http.Header{
		"X-Forwarded-For":   {"203.0.113.7", "198.51.100.9"},
		"X-Forwarded-Host":  {"vanity.example.com"},
		"X-Forwarded-Proto": {"https"},
		"Forwarded":         {"for=203.0.113.7;proto=https"},
		"X-Real-Ip":         {"203.0.113.7"},
	})

	RestoreInboundForwarded(pr)

	if got := pr.Out.Header.Get("X-Forwarded-Host"); got != "vanity.example.com" {
		t.Errorf("X-Forwarded-Host: got %q, want %q", got, "vanity.example.com")
	}
	if got := pr.Out.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto: got %q, want %q", got, "https")
	}
	if got := pr.Out.Header.Get("Forwarded"); got != "for=203.0.113.7;proto=https" {
		t.Errorf("Forwarded: got %q", got)
	}
	if got := len(pr.Out.Header["X-Forwarded-For"]); got != 2 {
		t.Fatalf("X-Forwarded-For: got %d values, want 2", got)
	}

	// The restored values must be a copy: mutating the outbound request must not reach
	// back into the request the caller is still serving.
	pr.Out.Header.Set("X-Forwarded-Host", "rewritten.example.com")
	if got := pr.In.Header.Get("X-Forwarded-Host"); got != "vanity.example.com" {
		t.Errorf("inbound X-Forwarded-Host was mutated: got %q", got)
	}
}

func TestRestoreInboundForwarded_AbsentHeadersStayAbsent(t *testing.T) {
	pr := newProxyRequest("198.51.100.25:5000", http.Header{})

	RestoreInboundForwarded(pr)

	for _, h := range forwardedHeaders {
		if _, ok := pr.Out.Header[h]; ok {
			t.Errorf("%s was created on the outbound request but was never inbound", h)
		}
	}
}

func TestAppendPeerToXForwardedFor(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		out        http.Header
		want       string
		wantAbsent bool
	}{
		{
			name:       "no prior chain",
			remoteAddr: "198.51.100.25:5000",
			out:        http.Header{},
			want:       "198.51.100.25",
		},
		{
			name:       "extends a prior chain",
			remoteAddr: "198.51.100.25:5000",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7, 198.51.100.25",
		},
		{
			name:       "folds multiple header values into one",
			remoteAddr: "198.51.100.25:5000",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7", "192.0.2.4"}},
			want:       "203.0.113.7, 192.0.2.4, 198.51.100.25",
		},
		{
			// Go issue 38079: a present-but-nil value is the opt-out signal.
			name:       "present-but-nil value suppresses the header",
			remoteAddr: "198.51.100.25:5000",
			out:        http.Header{"X-Forwarded-For": nil},
			wantAbsent: true,
		},
		{
			name:       "unparsable peer address leaves the chain alone",
			remoteAddr: "not-an-address",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := newProxyRequest(tc.remoteAddr, http.Header{})
			pr.Out.Header = tc.out

			AppendPeerToXForwardedFor(pr)

			got := pr.Out.Header.Get("X-Forwarded-For")
			if tc.wantAbsent {
				if got != "" {
					t.Errorf("X-Forwarded-For: got %q, want it suppressed", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("X-Forwarded-For: got %q, want %q", got, tc.want)
			}
		})
	}
}
