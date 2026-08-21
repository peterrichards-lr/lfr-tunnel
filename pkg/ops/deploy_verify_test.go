package ops

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchServerVersion covers the parsing, including the shapes a restarting gateway
// actually produces while it is coming back up.
func TestFetchServerVersion(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{"normal response", 200, `{"server_version":"v1.47.0"}`, "v1.47.0", false},
		{"extra fields ignored", 200, `{"server_version":"v1.47.0","latest_version":"v1.47.0"}`, "v1.47.0", false},
		{"missing field reads as empty", 200, `{}`, "", false},
		// nginx answers while the service behind it is restarting.
		{"gateway error", 502, `<html>502</html>`, "", true},
		{"maintenance page", 503, `<html>maintenance</html>`, "", true},
		{"unparseable body", 200, `<html>not json</html>`, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body)) //nolint:errcheck
			}))
			defer srv.Close()

			got, err := fetchServerVersion(srv.Client(), srv.URL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVerifyDeployedVersionFailsOnWrongVersion is the case that matters: a deploy that
// silently left the node on its previous version must be reported, not passed over --
// otherwise a scheduled-off edge goes back to sleep still running the old binary.
func TestVerifyDeployedVersionFailsOnWrongVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_version":"v1.46.0"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	err := verifyDeployedVersionAt("http://"+host, "v1.47.0", 2*time.Second, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected a failure when the node is serving the old version")
	}
	if !strings.Contains(err.Error(), "v1.46.0") || !strings.Contains(err.Error(), "v1.47.0") {
		t.Errorf("the error should name both what is serving and what was expected, got: %v", err)
	}
}

// TestVerifyDeployedVersionSucceedsOnceItCatchesUp covers the normal case, where the first
// poll or two fail because the service is still restarting.
func TestVerifyDeployedVersionSucceedsOnceItCatchesUp(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_version":"v1.47.0"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	if err := verifyDeployedVersionAt(srv.URL, "v1.47.0", 5*time.Second, 100*time.Millisecond); err != nil {
		t.Errorf("expected success once the service came back, got: %v", err)
	}
	if calls < 3 {
		t.Errorf("expected the check to retry while the service restarted, got %d calls", calls)
	}
}
