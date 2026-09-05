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

func TestEnsureVisitorInXForwardedFor(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		visitorIP  string
		out        http.Header
		want       string
		wantAbsent bool
	}{
		{
			// The #1737 case. The peer append that follows contributes this address, and
			// contributes it from the connection rather than from a header.
			name:       "visitor is the peer: left to the peer append",
			remoteAddr: "192.0.2.1:1234",
			visitorIP:  "192.0.2.1",
			out:        http.Header{},
			wantAbsent: true,
		},
		{
			name:       "visitor is the peer: a prior chain is left untouched",
			remoteAddr: "192.0.2.1:1234",
			visitorIP:  "192.0.2.1",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7",
		},
		{
			name:       "visitor behind a trusted peer is introduced to an empty chain",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{},
			want:       "203.0.113.7",
		},
		{
			name:       "visitor behind a trusted peer extends a prior chain",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "198.51.100.9",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7, 198.51.100.9",
		},
		{
			name:       "a chain already ending with the visitor is not repeated",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7",
		},
		{
			// The rightmost entry can be the tail of a comma-separated value rather than
			// its own header line; unwrapping it is what makes the check work at all.
			name:       "the repeat check reads the tail of a comma-separated value",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{"X-Forwarded-For": {"192.0.2.4, 203.0.113.7"}},
			want:       "192.0.2.4, 203.0.113.7",
		},
		{
			// Only the LAST entry counts. The same address earlier in the chain is a
			// different hop's record and does not make this one redundant.
			name:       "the visitor earlier in the chain is still appended",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7, 192.0.2.4"}},
			want:       "203.0.113.7, 192.0.2.4, 203.0.113.7",
		},
		{
			name:       "folds multiple header values into one",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "198.51.100.9",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7", "192.0.2.4"}},
			want:       "203.0.113.7, 192.0.2.4, 198.51.100.9",
		},
		{
			// Go issue 38079: a present-but-nil value is the opt-out, and this helper may
			// not override it any more than the peer append may.
			name:       "present-but-nil value suppresses the header",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{"X-Forwarded-For": nil},
			wantAbsent: true,
		},
		{
			name:       "an empty visitor address adds nothing",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "   ",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7",
		},
		{
			// An unparsable peer means the peer append will contribute nothing, so the
			// visitor is the only address left to record.
			name:       "unparsable peer still records the visitor",
			remoteAddr: "not-an-address",
			visitorIP:  "203.0.113.7",
			out:        http.Header{},
			want:       "203.0.113.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := newProxyRequest(tc.remoteAddr, http.Header{})
			pr.Out.Header = tc.out

			EnsureVisitorInXForwardedFor(pr, tc.visitorIP)

			got := pr.Out.Header.Get("X-Forwarded-For")
			if tc.wantAbsent {
				if got != "" {
					t.Errorf("X-Forwarded-For: got %q, want it left unset", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("X-Forwarded-For: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEnsureVisitorInXForwardedFor_ThenAppendPeer covers the pair as the proxies call it:
// the visitor, then the peer, with no hop named twice.
func TestEnsureVisitorInXForwardedFor_ThenAppendPeer(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		visitorIP  string
		out        http.Header
		want       string
	}{
		{
			name:       "direct visitor yields a single entry",
			remoteAddr: "192.0.2.1:1234",
			visitorIP:  "192.0.2.1",
			out:        http.Header{},
			want:       "192.0.2.1",
		},
		{
			name:       "behind nginx yields visitor then loopback",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{},
			want:       "203.0.113.7, 127.0.0.1",
		},
		{
			name:       "nginx's own chain is not repeated",
			remoteAddr: "127.0.0.1:4444",
			visitorIP:  "203.0.113.7",
			out:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       "203.0.113.7, 127.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := newProxyRequest(tc.remoteAddr, http.Header{})
			pr.Out.Header = tc.out

			EnsureVisitorInXForwardedFor(pr, tc.visitorIP)
			AppendPeerToXForwardedFor(pr)

			if got := pr.Out.Header.Get("X-Forwarded-For"); got != tc.want {
				t.Errorf("X-Forwarded-For: got %q, want %q", got, tc.want)
			}
		})
	}
}
