package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGuardLocalOnly is the regression test for #1138. The Inspector binds loopback, which
// keeps it off the network but not away from the browser: any page a developer visits can
// drive its five mutating endpoints cross-origin, and CORS blocks reading the response but
// not the side effect.
func TestGuardLocalOnly(t *testing.T) {
	const port = 4040

	reached := false
	guarded := guardLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), port, true)

	cases := []struct {
		name       string
		origin     string
		host       string
		wantStatus int
	}{
		// Non-browser callers send no Origin and must keep working untouched.
		{"no origin is allowed", "", "127.0.0.1:4040", http.StatusOK},
		{"own origin is allowed", "http://127.0.0.1:4040", "127.0.0.1:4040", http.StatusOK},
		{"localhost spelling of own origin", "http://localhost:4040", "localhost:4040", http.StatusOK},

		// The drive-by case: a page the developer happens to be visiting.
		{"foreign origin is rejected", "https://evil.example", "127.0.0.1:4040", http.StatusForbidden},
		// Same host, different port is still a different origin.
		{"loopback on another port is rejected", "http://127.0.0.1:9999", "127.0.0.1:4040", http.StatusForbidden},

		// DNS rebinding: attacker's hostname resolved to 127.0.0.1, so the request
		// arrives with no Origin but a foreign Host.
		{"rebound host is rejected", "", "evil.example", http.StatusForbidden},
		{"rebound host with port is rejected", "", "evil.example:4040", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodPost, "/api/maintenance", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusForbidden && reached {
				t.Error("the handler ran anyway -- for a mutating endpoint the side effect is the damage, whether or not the response is readable")
			}
		})
	}
}

// TestGuardLocalOnlyWithoutHostCheck covers the Docker bind, where the legitimate Host is
// whatever the operator mapped and cannot be predicted. The Origin check still applies.
func TestGuardLocalOnlyWithoutHostCheck(t *testing.T) {
	guarded := guardLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), 4040, false)

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Host = "some-container-name:4040"
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("an unpredictable Host must be accepted when not bound to loopback, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/replay", nil)
	req.Host = "some-container-name:4040"
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a foreign origin must still be rejected, got %d", rec.Code)
	}
}

func TestIsLoopbackHostname(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "127.0.0.53", "::1", "[::1]", "LOCALHOST"} {
		if !isLoopbackHostname(h) {
			t.Errorf("%q should be loopback", h)
		}
	}
	for _, h := range []string{"evil.example", "192.168.1.10", "0.0.0.0", ""} {
		if isLoopbackHostname(h) {
			t.Errorf("%q should not be loopback", h)
		}
	}
}
