package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Saving access control from the Inspector failed with HTTP 401 for EVERY mode, Public
// included, whenever the tunnel was served by an edge -- and then applied anyway at the next
// re-registration, because the engine had already been written (#2116).
//
// Reproduced live on 2026-09-21 against apac:
//
//	Gateway rejected update (HTTP 401: {"error": "Unauthorized"})
//
// The cause is one line of addressing. Reservations live in central's database; an edge has
// none, and validatePAT returns false on `s.db == nil` before it ever looks at the token.

// The request must reach the CONTROL PLANE, not the gateway serving the tunnel.
//
// Both stubs are stood up and only one may be called: asserting "central was called" alone
// would pass if the client had posted to both, and asserting "the edge was not called" alone
// would pass if it had posted to nothing at all.
func TestAccessControlIsSentToTheControlPlaneNotTheServingGateway(t *testing.T) {
	var edgeHits, centralHits int

	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		edgeHits++
		// What a real edge does: no database, so the token is refused unread.
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer edge.Close()

	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		centralHits++
		if got := r.Header.Get("X-Auth-Token"); got != "pat-token" {
			t.Errorf("control plane received X-Auth-Token %q, want the client's token", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer central.Close()

	engine := newAccessControlTestEngine(edge.URL, central.URL)
	port := startInspectorForTest(t, engine, 56001)

	if code, body := postAccessControl(t, port, `{"passcode":"s3cret","whitelist_ips":"","access_mode":"passcode"}`); code != http.StatusOK {
		t.Fatalf("save returned HTTP %d: %s", code, body)
	}

	if centralHits != 1 {
		t.Errorf("the control plane was called %d time(s), want exactly 1", centralHits)
	}
	if edgeHits != 0 {
		t.Errorf("the gateway serving the tunnel was called %d time(s); it has no database, so "+
			"every save addressed to it is refused 401 whatever the mode", edgeHits)
	}
}

// With no control plane advertised there is nowhere else to go, so the serving gateway is still
// used. Without this the fix would break the single-node case, where they are the same box.
func TestAccessControlFallsBackToTheServingGatewayWhenNoControlPlaneIsKnown(t *testing.T) {
	var hits int
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	engine := newAccessControlTestEngine(gateway.URL, "")
	port := startInspectorForTest(t, engine, 56031)

	if code, body := postAccessControl(t, port, `{"passcode":"s3cret","whitelist_ips":"","access_mode":"passcode"}`); code != http.StatusOK {
		t.Fatalf("save returned HTTP %d: %s", code, body)
	}
	if hits != 1 {
		t.Errorf("the serving gateway was called %d time(s), want 1 when no control plane is known", hits)
	}
}

// A refused save must leave the engine as it was. It used to write the values first and report
// the failure afterwards, so registration carried the rejected passcode to the gateway at the
// next reconnect -- the dialog said the change failed and the tunnel started demanding it.
func TestARefusedSaveDoesNotChangeTheEngine(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer refusing.Close()

	engine := newAccessControlTestEngine(refusing.URL, refusing.URL)
	engine.AccessMode = "public"
	engine.Passcode = ""
	port := startInspectorForTest(t, engine, 56061)

	code, body := postAccessControl(t, port, `{"passcode":"s3cret","whitelist_ips":"","access_mode":"passcode"}`)
	if code == http.StatusOK {
		t.Fatalf("a refused save reported success: %s", body)
	}

	engine.mu.RLock()
	mode, passcode := engine.AccessMode, engine.Passcode
	engine.mu.RUnlock()

	if passcode != "" || mode != "public" {
		t.Errorf("after a REFUSED save the engine holds mode=%q passcode set=%v; registration "+
			"sends these, so the rejected change would apply itself on the next reconnect",
			mode, passcode != "")
	}
}

// Whether the form may be used at all, decided by the same function the save path calls so the
// two cannot disagree. A field that can never be applied is disabled, not left to fail on submit.
func TestAccessControlEditabilityMatchesWhatTheSaveWouldAccept(t *testing.T) {
	cases := []struct {
		name     string
		assigned string
		urls     []string
		cpUp     bool
		cpKnown  bool
		editable bool
		mentions string
	}{
		{"connected to a normal subdomain", "peters", []string{"https://peters.lfr-demo.se"}, true, true, true, ""},
		{"not connected yet", "", nil, true, true, false, "connected"},
		{"a custom domain", "vanity.example.com", []string{"https://vanity.example.com"}, true, true, false, "portal"},
		// Configuration central stores must be changed at central. While central is
		// unreachable it cannot be changed -- and the edge keeps enforcing what it holds.
		{"control plane down", "peters", []string{"https://peters.lfr-demo.se"}, false, true, false, "unreachable"},
		// Not asked yet is not the same as down. A client that has just started has pinged
		// nothing, and disabling on that basis flashes the panel off and on at every launch.
		{"control plane not probed yet", "peters", []string{"https://peters.lfr-demo.se"}, false, false, true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			editable, reason := accessControlEditability(tc.assigned, tc.urls, tc.cpUp, tc.cpKnown)
			if editable != tc.editable {
				t.Fatalf("editable=%v, want %v (reason %q)", editable, tc.editable, reason)
			}
			if tc.editable {
				if reason != "" {
					t.Errorf("an editable form carries the excuse %q", reason)
				}
				return
			}
			if !strings.Contains(strings.ToLower(reason), tc.mentions) {
				t.Errorf("reason %q does not tell the reader about %q", reason, tc.mentions)
			}
			// The disabled state must agree with the endpoint, or the form forbids something
			// that would have worked -- except while the control plane is down, which is a
			// refusal about reachability rather than about the assignment.
			if tc.cpUp || !tc.cpKnown {
				if _, _, err := splitAssignedHost(tc.assigned, tc.urls); err == nil && strings.TrimSpace(tc.assigned) != "" {
					t.Error("the form is disabled for an assignment the save path would have accepted")
				}
			}
		})
	}
}

// A disabled panel must never read as "your tunnel is now open to everyone". The edge goes on
// enforcing the access control it holds on the lease, and the sentence has to say so -- this is
// the difference between a reassuring message and an alarming one.
func TestTheControlPlaneOutageMessageSaysTheTunnelIsStillProtected(t *testing.T) {
	_, reason := accessControlEditability("peters", []string{"https://peters.lfr-demo.se"}, false, true)
	lower := strings.ToLower(reason)
	for _, want := range []string{"still", "back"} {
		if !strings.Contains(lower, want) {
			t.Errorf("the outage message %q does not say %q; a reader has to be told the tunnel "+
				"is still enforcing its settings and that the fields come back", reason, want)
		}
	}
}

// A permanent refusal must not be reported as a passing outage. A custom domain is never
// editable here, control plane or no control plane, and telling someone to wait for central
// would have them waiting forever.
func TestAPermanentRefusalOutranksAnOutage(t *testing.T) {
	_, reason := accessControlEditability("vanity.example.com", []string{"https://vanity.example.com"}, false, true)
	if strings.Contains(strings.ToLower(reason), "unreachable") {
		t.Errorf("a custom domain was reported as a control-plane outage (%q); it will still be "+
			"a custom domain when the outage clears", reason)
	}
}

func newAccessControlTestEngine(serverURL, centralURL string) *InterceptorEngine {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.Token = "pat-token"
	engine.ServerURL = serverURL
	engine.SubdomainAss = "peters"
	engine.PublicURLs = []string{"https://peters.lfr-demo.se"}
	engine.SetCentralURL(centralURL)
	return engine
}

// A fixed base rather than port 0: StartInspector reports back the port it asked for, so a
// kernel-assigned one would leave the test posting to the wrong place. It walks upwards on
// "address already in use", and the bases below are spaced further apart than it walks.
func startInspectorForTest(t *testing.T, engine *InterceptorEngine, base int) int {
	t.Helper()
	port, err := StartInspector(base, engine)
	if err != nil {
		t.Fatalf("StartInspector: %v", err)
	}
	return port
}

func postAccessControl(t *testing.T, port int, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(
		"http://127.0.0.1:"+strconv.Itoa(port)+"/api/access-control",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("posting to the Inspector: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("closing the Inspector response body: %v", cerr)
		}
	}()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// Guards the CLASS, not this one call. A control-plane path addressed at the gateway serving the
// tunnel is refused whenever that gateway is an edge, and the refusal names authentication rather
// than addressing -- which is why this took a live session on apac to find rather than a test.
//
// There is exactly one such call today. That is the moment to fence it: the next one will be
// written by copying this one.
func TestNoControlPlaneCallIsAddressedAtTheServingGateway(t *testing.T) {
	// Paths only central can answer, because only central has the database behind them.
	controlPlanePaths := []string{"/api/portal/", "/api/admin/"}

	roots := []string{".", "../../cmd/lfr-tunnel"}
	checked := 0

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, e.Name())
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				if !mentionsAny(line, controlPlanePaths) {
					continue
				}
				// Only URL construction, not a comment or a path comparison.
				if !strings.Contains(line, "Sprintf") && !strings.Contains(line, "+") {
					continue
				}
				checked++
				if strings.Contains(line, "serverURL") || strings.Contains(line, "ServerURL") {
					t.Errorf("%s:%d addresses a control-plane path at the gateway serving the "+
						"tunnel:\n    %s\nAn edge has no database, so validatePAT refuses the "+
						"token unread and the call 401s. Use the control-plane URL.",
						path, i+1, strings.TrimSpace(line))
				}
			}
		}
	}

	// A guard that matches nothing passes forever. If the call was renamed or moved, this must
	// say so rather than quietly vouch for an empty set.
	if checked == 0 {
		t.Fatal("found no control-plane URL construction at all. Either it moved, or this check " +
			"no longer recognises the shape of one -- which is worse than not checking.")
	}
}

func mentionsAny(line string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(line, n) {
			return true
		}
	}
	return false
}
