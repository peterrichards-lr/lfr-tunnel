package client

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// preventsUnconditionalReuse reports whether a Cache-Control value stops a browser serving the
// response again without asking the origin first.
//
// Deliberately narrow: "has a Cache-Control at all" would be satisfied by the favicon's
// `public, max-age=86400`, which is the opposite policy and a correct one for that route (#2182).
func preventsUnconditionalReuse(value string) bool {
	for _, directive := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(directive)) {
		case "no-store", "no-cache":
			return true
		}
	}
	return false
}

// getInspector issues a GET against a running Inspector and returns the response headers and body.
func getInspector(t *testing.T, port int, path string) (http.Header, []byte) {
	t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
	if err != nil {
		t.Fatalf("GET %s from the Inspector: %v", path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("closing the Inspector response body for %s: %v", path, cerr)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the Inspector response body for %s: %v", path, err)
	}
	return resp.Header, body
}

// TestInspectorDashboardIsNotCached is the reported defect (#2182): the dashboard HTML went out
// with no Cache-Control, no ETag and no Last-Modified, so a browser was free to invent its own
// expiry for it and keep showing the pre-upgrade UI after `lfr-tunnel -upgrade`.
//
// Two things make this an assertion about the DASHBOARD rather than about "some route":
//
//   - the body is compared against the rendered dashboard, so only the dashboard handler can
//     satisfy it. /favicon.ico sets a Cache-Control of its own, and an assertion that merely looked
//     for the header's presence would pass on that route while the dashboard stayed uncached.
//     Compared against RenderDashboardHTML(port) rather than the raw DashboardHTML embed because
//     the page is stamped with the port it bound as it is served (#2190); the embed is a template,
//     not the artefact the browser receives.
//   - the value has to prevent unconditional reuse, not merely exist. The favicon's
//     `public, max-age=86400` is a Cache-Control and would fail this, which is the point.
//
// Driven through StartInspector over a real socket rather than against a re-declared mux: the
// header is set by noStoreByDefault in front of the handler chain, so a test that rebuilt the
// routes would be testing its own copy of the fix and would pass with production unchanged.
func TestInspectorDashboardIsNotCached(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	port := startInspectorForTest(t, engine, 56091)

	header, body := getInspector(t, port, "/")

	want := RenderDashboardHTML(port)
	if !bytes.Equal(body, want) {
		t.Fatalf("GET / did not serve the rendered dashboard (%d bytes read, %d expected); "+
			"the rest of this test would be asserting against the wrong route",
			len(body), len(want))
	}

	got := header.Get("Cache-Control")
	if !preventsUnconditionalReuse(got) {
		t.Errorf("the dashboard was served with Cache-Control %q, which lets a browser reuse it "+
			"without revalidating -- an upgrade can then leave the previous version's UI on screen (#2182); "+
			"want a value containing no-store or no-cache", got)
	}
}

// TestInspectorCacheControlByRoute pins the class the dashboard was one instance of: an Inspector
// response whose body is not constant, served with no freshness information.
//
// The route list below is every mux.HandleFunc in inspector.go reachable by GET. Re-derive it with:
//
//	awk '/mux\.HandleFunc\(/ { r=$0; sub(/.*mux\.HandleFunc\(/,"",r); sub(/,.*/,"",r); print r }' \
//	    pkg/client/inspector.go
//
// The POST-only routes (/api/restart, /api/access-control, /api/maintenance, /api/replay) answer a
// GET with 405, which is itself heuristically cacheable, so they are exercised here too.
//
// /favicon.ico is the deliberate exception and is asserted as such rather than skipped: it is the
// one asset that is worth caching, and it must keep saying so. If this case ever goes red, someone
// has changed a considered policy and should confirm they meant to -- it is not a licence to
// loosen the rest.
func TestInspectorCacheControlByRoute(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	port := startInspectorForTest(t, engine, 56121)

	uncached := []string{
		"/",            // dashboard HTML, embedded -- changes on upgrade
		"/settings",    // same handler, same HTML
		"/logs",        // same handler, same HTML
		"/api/state",   // live capture state
		"/api/healthz", // live connectivity
		"/api/info",    // live state, and carries the version string
		"/api/logs",    // console log
		"/api/logs/" + LogKindTraffic,
		"/api/logs/" + LogKindError,
		"/api/config",             // live client config
		"/api/restart",            // 405 to a GET, and a 405 is cacheable by default
		"/api/access-control",     // 405
		"/api/maintenance",        // 405
		"/api/replay",             // 405
		"/no-such-inspector-page", // 404, also cacheable by default
	}

	for _, path := range uncached {
		header, _ := getInspector(t, port, path)
		got := header.Get("Cache-Control")
		if !preventsUnconditionalReuse(got) {
			t.Errorf("%s was served with Cache-Control %q; every Inspector route except /favicon.ico "+
				"must prevent unconditional reuse (#2182)", path, got)
		}
	}

	// BOUNDING, not FIRING: this passed before the fix and must keep passing after it.
	const wantFavicon = "public, max-age=86400"
	header, _ := getInspector(t, port, "/favicon.ico")
	if got := header.Get("Cache-Control"); got != wantFavicon {
		t.Errorf("/favicon.ico was served with Cache-Control %q, want %q -- the favicon is the one "+
			"route that is deliberately cached and the default must not have swallowed it", got, wantFavicon)
	}
}
