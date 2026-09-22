package server

import (
	"os"
	"strings"
	"testing"
)

// Custom header VALUES must not reach the browser (#2150).
//
// The portal's tunnel details panel asks "is anything being injected into this tunnel's
// requests?", and the names answer it. The values are chosen by the user, and
// `-header "Authorization=Bearer ..."` is an ordinary use -- so publishing the map would hand
// every admin a working credential for every protected tunnel. That is not hypothetical: this
// same feed carried session tokens and Basic Auth credentials until #2137.
func TestSortedHeaderNamesReturnsNamesAndNeverValues(t *testing.T) {
	headers := map[string]string{
		"X-Real-IP":     "10.0.0.1",
		"Authorization": "Bearer super-secret-token",
		"X-Frame":       "DENY",
	}

	got := sortedHeaderNames(headers)

	want := []string{"Authorization", "X-Frame", "X-Real-IP"}
	if len(got) != len(want) {
		t.Fatalf("sortedHeaderNames returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			// Sorted, because this is built from a map and Go randomises map iteration
			// deliberately -- the defect that reshuffled every portal table (#2143).
			t.Errorf("entry %d = %q, want %q (order must be stable)", i, got[i], want[i])
		}
	}

	// Nothing returned may be a VALUE from the map. Checked by comparing against the values
	// themselves rather than by looking for a known substring: a future implementation that
	// returned "name: value" pairs would pass a substring check on the name alone.
	for _, name := range got {
		for headerName, headerValue := range headers {
			if name == headerValue {
				t.Errorf("sortedHeaderNames returned %q, which is the VALUE of %q", name, headerName)
			}
			if strings.Contains(name, headerValue) {
				t.Errorf("entry %q embeds the value of %q", name, headerName)
			}
		}
	}
}

// Nil in, nil out, so a tunnel with no custom headers omits the key rather than sending an
// empty array that a renderer has to special-case.
func TestSortedHeaderNamesIsNilForNoHeaders(t *testing.T) {
	if got := sortedHeaderNames(nil); got != nil {
		t.Errorf("sortedHeaderNames(nil) = %v, want nil", got)
	}
	if got := sortedHeaderNames(map[string]string{}); got != nil {
		t.Errorf("sortedHeaderNames(empty) = %v, want nil", got)
	}
}

// The launch context reaches EVERY lease in a session, not just the first.
//
// A client mapping three ports gets three leases, and the portal groups them under one row
// whose details come from whichever lease it holds. Setting only one would make the panel's
// contents depend on which port happened to be first.
func TestSetLaunchContextReachesEveryLeaseInTheSession(t *testing.T) {
	r := NewRegistry(nil)
	token := "session-token-for-test"
	r.leases = map[string]*TunnelLease{
		"a.example.test": {FullHost: "a.example.test", SessionToken: token},
		"b.example.test": {FullHost: "b.example.test", SessionToken: token},
	}
	r.sessionLeases = map[string][]*TunnelLease{
		token: {r.leases["a.example.test"], r.leases["b.example.test"]},
	}

	r.SetLaunchContextForSession(token, []string{"-subdomain", "-region"},
		map[string]string{"region": "LFR_TUNNEL_REGION"})

	for host, lease := range r.leases {
		if len(lease.LaunchFlags) != 2 {
			t.Errorf("%s: LaunchFlags = %v, want both flags", host, lease.LaunchFlags)
		}
		if lease.LaunchOverrides["region"] != "LFR_TUNNEL_REGION" {
			t.Errorf("%s: LaunchOverrides = %v, want the region override", host, lease.LaunchOverrides)
		}
	}
}

// ...and the snapshot the portal actually reads must carry it.
//
// ListLeases copies field by field rather than dereferencing, because the struct holds two
// mutexes. A field added to TunnelLease and not to that copy is silently absent from every
// consumer -- which is how BaseRateLimit came to print 0 for every tunnel (#2006).
func TestListLeasesCarriesTheLaunchContext(t *testing.T) {
	r := NewRegistry(nil)
	r.leases = map[string]*TunnelLease{
		"a.example.test": {
			FullHost:        "a.example.test",
			LaunchFlags:     []string{"-region"},
			LaunchOverrides: map[string]string{"region": "LFR_TUNNEL_REGION"},
		},
	}

	snapshot := r.ListLeases()
	if len(snapshot) != 1 {
		t.Fatalf("ListLeases returned %d leases, want 1", len(snapshot))
	}
	if len(snapshot[0].LaunchFlags) != 1 || snapshot[0].LaunchFlags[0] != "-region" {
		t.Errorf("snapshot LaunchFlags = %v; the copy dropped the field", snapshot[0].LaunchFlags)
	}
	if snapshot[0].LaunchOverrides["region"] != "LFR_TUNNEL_REGION" {
		t.Errorf("snapshot LaunchOverrides = %v; the copy dropped the field", snapshot[0].LaunchOverrides)
	}
}

// Both registration paths stamp the launch context, not just the control plane's.
//
// A client reaching an edge never executes handleRegister's control-plane body, and most of
// the fleet registers that way -- so covering one path leaves the portal blank for almost
// every tunnel. That exact mistake shipped twice: #2130 landed on central and nowhere else,
// and had to be redone as #2139.
//
// A source-level assertion, deliberately: standing up a control plane and an edge to observe
// one assignment is a great deal of machinery around a fact that is plain in the text, and the
// thing worth protecting is that there are TWO call sites rather than one.
func TestBothRegistrationPathsStampTheLaunchContext(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	const call = "s.registry.SetLaunchContextForSession(sessionToken, req.LaunchFlags, req.LaunchOverrides)"
	if got := strings.Count(string(src), call); got != 2 {
		t.Errorf("found %d call(s) to SetLaunchContextForSession, want 2 "+
			"(the control plane's handleRegister and the edge's own); "+
			"an edge-hosted tunnel reports no launch context if either is missing", got)
	}
}

// The telemetry payload publishes header NAMES and never the header map.
//
// Read from the source because the alternative is a server, a database and a reservation to
// observe one map key. The risk being guarded is narrow and textual: someone fixing the
// details panel by reaching for the richer field.
func TestTheTelemetryPayloadNeverCarriesHeaderValues(t *testing.T) {
	src, err := os.ReadFile("telemetry_ws.go")
	if err != nil {
		t.Fatalf("read telemetry_ws.go: %v", err)
	}
	text := string(src)

	// Both blocks -- the local lease and the edge-hosted one. They are duplicated, so a fix
	// applied to one reads as complete while half the fleet still leaks; that is precisely how
	// the passcode hash survived its first fix (#2135).
	if got := strings.Count(text, `"added_header_names"`); got != 2 {
		t.Errorf(`found %d "added_header_names" key(s), want 2 (local and edge leases)`, got)
	}
	if strings.Contains(text, `"added_headers"`) {
		t.Error(`the payload carries "added_headers"; the map holds user-chosen values that ` +
			`are routinely credentials, and this feed reaches every admin`)
	}
}
