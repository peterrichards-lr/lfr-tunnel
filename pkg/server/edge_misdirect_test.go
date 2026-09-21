package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// A control-plane request that reaches an Edge must say so, not refuse the credential (#2123).
//
// Reproduced in production on 2026-09-21: the owner's Inspector, connected to apac, was told
//
//	HTTP 401 {"error": "Unauthorized"}
//
// about the same personal access token that had registered the tunnel through that very node
// seconds earlier. validatePAT returns false on `s.db == nil` before it reads the token at all.

// apiHost is a host this gateway recognises as its own. Anything else is tunnel traffic, and
// the router proxies it long before the API routes are consulted.
const apiHost = "lfr-demo.se"

// edgeServer is a gateway with no database, which is what makes it unable to answer.
func edgeServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(&config.ServerConfig{
		ControlPlaneURL: "https://tunnel.lfr-demo.se",
		EdgeToken:       "edge-token",
		Domains:         []string{"lfr-demo.se"},
		// No DBPath: this is the whole point.
	})
	if err != nil {
		t.Fatalf("building an edge server: %v", err)
	}
	return s
}

// The routes are read OUT OF ServeHTTP rather than listed here. A list would be a snapshot of
// what somebody remembered on the day; reading the source means a route added under one of these
// groups tomorrow is covered tomorrow, which is the failure this guards.
func controlPlaneRoutesInSource(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	re := regexp.MustCompile(`r\.URL\.Path == "(/api/(?:portal|admin)/[^"]*)"`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		seen[m[1]] = true
	}
	// Prefix-matched groups reach handlers whose paths are not literals; cover one of each.
	seen["/api/portal/reservations/some-id"] = true
	seen["/api/admin/users/some-id"] = true

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) < 10 {
		t.Fatalf("found only %d control-plane routes in server.go; this check has stopped "+
			"recognising them, which is worse than not checking", len(out))
	}
	return out
}

func TestAnEdgeAnswersControlPlaneRequestsHonestly(t *testing.T) {
	s := edgeServer(t)

	// A distinct source address per request. Sharing one trips the gateway's own rate limiter
	// partway down the list, and a 429 would hide whether the route answers honestly.
	ip := 0

	for _, path := range controlPlaneRoutesInSource(t) {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				ip++
				req := httptest.NewRequest(method, path, strings.NewReader("{}"))
				// httptest defaults the Host to example.com, which this gateway reads as
				// tunnel traffic and proxies -- the request never reaches the API router at
				// all. The first version of this test asserted against a 404 from the proxy,
				// which is a state production cannot produce for a portal call.
				req.Host = apiHost
				req.RemoteAddr = fmt.Sprintf("192.0.2.%d:12345", ip%254+1)
				req.Header.Set("X-Auth-Token", "a-perfectly-good-pat")
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, req)

				if rec.Code == http.StatusUnauthorized {
					t.Fatalf("%s %s answered 401 about a credential it never examined; that is "+
						"the defect -- it sends the reader to check tokens, permissions and "+
						"expiry, none of which is wrong", method, path)
				}
				if rec.Code != http.StatusMisdirectedRequest {
					t.Fatalf("%s %s answered %d, want 421 Misdirected Request", method, path, rec.Code)
				}
				body := rec.Body.String()
				if !strings.Contains(body, "tunnel.lfr-demo.se") {
					t.Errorf("%s %s does not name the control plane: %s", method, path, body)
				}
				if got := rec.Header().Get("X-Control-Plane"); got != "https://tunnel.lfr-demo.se" {
					t.Errorf("%s %s X-Control-Plane = %q", method, path, got)
				}
			}
		})
	}
}

// The refusal must not swallow the routes an Edge genuinely serves. An Edge that stopped
// answering these would be worse than one that answers 401 to the portal.
func TestAnEdgeStillServesWhatItIsFor(t *testing.T) {
	s := edgeServer(t)

	for _, path := range []string{"/api/healthz", "/api/version"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = apiHost
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code == http.StatusMisdirectedRequest {
			t.Errorf("%s was refused as a control-plane path; an edge serves it", path)
		}
	}
}

// Central answers these itself and must never claim to be misdirected.
func TestTheControlPlaneNeverRefusesItsOwnRoutes(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(&config.ServerConfig{
		DBPath:  dir + "/test.db",
		Domains: []string{"lfr-demo.se"},
	})
	if err != nil {
		t.Fatalf("building a control plane: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/portal/reservations", nil)
	req.Host = apiHost
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code == http.StatusMisdirectedRequest {
		t.Fatal("central refused its own route as misdirected; the check is keyed on having no " +
			"database, and central has one")
	}
}

// The path is attacker-controlled and is echoed into a hand-written JSON body.
func TestAQuotedPathCannotBreakOutOfTheMessage(t *testing.T) {
	s := edgeServer(t)
	req := httptest.NewRequest(http.MethodGet, `/api/portal/x`, nil)
	req.Host = apiHost
	req.URL.Path = `/api/portal/"},"injected":{"`
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), `"injected"`) {
		t.Errorf("a quote in the path escaped the JSON string: %s", rec.Body.String())
	}
}
