package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// Header edits and rate-limit overrides reach the tunnel on whichever gateway holds it, and a
// header edit changes only what it names (#2530).

func TestHeaderPatchChangesOnlyWhatItNames(t *testing.T) {
	cur := map[string]string{"Authorization": "Bearer secret", "x-tenant": "a"}

	got := leaseHeaderPatch{Set: map[string]string{"X-Debug": "1"}}.apply(cur)
	if got["Authorization"] != "Bearer secret" || got["x-tenant"] != "a" || got["X-Debug"] != "1" {
		t.Fatalf("adding X-Debug lost or altered the other headers: %v", got)
	}

	got = leaseHeaderPatch{Remove: []string{"X-Tenant"}}.apply(cur)
	if _, still := got["x-tenant"]; still || got["Authorization"] != "Bearer secret" || len(got) != 1 {
		t.Fatalf("removing X-Tenant should remove it (whatever its spelling) and keep the rest: %v", got)
	}

	got = leaseHeaderPatch{Set: map[string]string{"X-TENANT": "b"}}.apply(cur)
	if _, old := got["x-tenant"]; old || got["X-Tenant"] != "b" || len(got) != 2 {
		t.Fatalf("setting an existing header under another spelling must replace it, not add a second: %v", got)
	}

	if cur["x-tenant"] != "a" || len(cur) != 2 {
		t.Fatalf("apply wrote into the lease's live map, which the proxy reads while serving: %v", cur)
	}
}

func TestHeaderPatchRefusesWhatTheGatewayMustNotInject(t *testing.T) {
	for name, patch := range map[string]leaseHeaderPatch{
		"empty":            {},
		"Host":             {Set: map[string]string{"host": "evil.example"}},
		"Content-Length":   {Set: map[string]string{"Content-Length": "0"}},
		"space in name":    {Set: map[string]string{"X Bad": "1"}},
		"CRLF in value":    {Set: map[string]string{"X-Ok": "a\r\nX-Injected: 1"}},
		"oversized value":  {Set: map[string]string{"X-Ok": strings.Repeat("a", maxLeaseHeaderValue+1)}},
		"NUL in the value": {Set: map[string]string{"X-Ok": "a\x00b"}},
	} {
		if err := patch.validate(); err == nil {
			t.Errorf("%s: accepted, should have been refused", name)
		}
	}
	if err := (leaseHeaderPatch{Set: map[string]string{"X-Api-Key": "k"}, Remove: []string{"X-Old"}}).validate(); err != nil {
		t.Errorf("an ordinary edit was refused: %v", err)
	}
}

// postHeaders drives the real endpoint as sessionToken.
func postHeaders(t *testing.T, srv *Server, sessionToken, fullHost string, change map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	sub, domain, _ := strings.Cut(fullHost, ".")
	body := map[string]any{"subdomain": sub, "domain": domain}
	for k, v := range change {
		body[k] = v
	}
	return postAs(t, srv, srv.handleUpdateReservationHeaders, "/api/portal/reservations/headers", sessionToken, body)
}

func leaseSnapshot(srv *Server, fullHost string) *TunnelLease {
	for _, l := range srv.registry.ListLeases() {
		if l.FullHost == fullHost {
			return l
		}
	}
	return nil
}

func TestHeaderEditOnACentralTunnelKeepsTheOtherHeaders(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()
	owner, session := seedDiagnosticsUser(t, srv, "owner2530@example.com", "user")

	if _, _, err := srv.registry.Register(owner.ID, "hdrs", []PortMapping{{LocalPort: 3000}}, []string{"example.com"}, 0, "203.0.113.9", "", map[string]string{"Authorization": "Bearer secret"}); err != nil {
		t.Fatalf("registering the central lease: %v", err)
	}
	host := "hdrs.example.com"
	if leaseSnapshot(srv, host) == nil {
		t.Fatalf("no lease at %s to edit", host)
	}

	if rec := postHeaders(t, srv, session, host, map[string]any{"set": map[string]string{"X-Debug": "1"}}); rec.Code != http.StatusOK {
		t.Fatalf("adding a header returned %d: %s", rec.Code, rec.Body.String())
	}
	got := leaseSnapshot(srv, host).AddedHeaders
	if got["Authorization"] != "Bearer secret" || got["X-Debug"] != "1" {
		t.Fatalf("after adding X-Debug the lease injects %v; the launch header must survive", got)
	}

	if rec := postHeaders(t, srv, session, host, map[string]any{"remove": []string{"x-debug"}}); rec.Code != http.StatusOK {
		t.Fatalf("removing a header returned %d: %s", rec.Code, rec.Body.String())
	}
	got = leaseSnapshot(srv, host).AddedHeaders
	if _, still := got["X-Debug"]; still || got["Authorization"] != "Bearer secret" {
		t.Fatalf("after removing X-Debug the lease injects %v", got)
	}

	if rec := postHeaders(t, srv, session, host, map[string]any{"set": map[string]string{"Host": "evil.example"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("injecting Host returned %d, want 400", rec.Code)
	}
}

// registerThroughEdgeWithHeaders is registerThroughEdge for a client launched with -header.
func registerThroughEdgeWithHeaders(t *testing.T, edge *Server, authToken, sub string, headers map[string]string) {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: sub,
		Ports:           []PortMapping{{LocalPort: 3001}},
		AuthToken:       authToken,
		ClientVersion:   config.Version,
		ClientOS:        "darwin",
		AddedHeaders:    headers,
	})
	if err != nil {
		t.Fatalf("marshalling the registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://us.lfr-demo.se/api/register", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registering through the edge returned %d: %s", rec.Code, rec.Body.String())
	}
}

// edgeHostFor is the full host central recorded for an edge lease with this prefix.
func edgeHostFor(t *testing.T, central *Server, prefix string) string {
	t.Helper()
	central.edgeLeasesMu.Lock()
	defer central.edgeLeasesMu.Unlock()
	for _, leases := range central.edgeLeases {
		for _, el := range leases {
			if el.Subdomain == prefix {
				return el.FullHost
			}
		}
	}
	t.Fatalf("central has no edge lease for %q", prefix)
	return ""
}

func TestHeaderEditReachesATunnelServedByAnEdge(t *testing.T) {
	f := startEdgeDiagFleet(t)
	registerThroughEdgeWithHeaders(t, f.edge, "pat-edge-diagnostics", "edgehdrs", map[string]string{"Authorization": "Bearer secret"})
	host := edgeHostFor(t, f.central, "edgehdrs")
	if leaseSnapshot(f.central, host) != nil {
		t.Fatalf("fixture: %s is in central's own registry, so this would not test the edge path", host)
	}

	if rec := postHeaders(t, f.central, f.adminSession, host, map[string]any{"set": map[string]string{"X-Debug": "1"}}); rec.Code != http.StatusOK {
		t.Fatalf("adding a header to an edge tunnel returned %d: %s", rec.Code, rec.Body.String())
	}
	waitUntil(t, stated("the edge to inject X-Debug alongside the launch header"), func() bool {
		l := leaseSnapshot(f.edge, host)
		return l != nil && l.AddedHeaders["X-Debug"] == "1" && l.AddedHeaders["Authorization"] == "Bearer secret"
	})
	if el, _ := f.central.findEdgeLease(host); strings.Join(el.AddedHeaderNames, ",") != "Authorization,X-Debug" {
		t.Fatalf("central's copy names %v; the portal would not show the new header", el.AddedHeaderNames)
	}

	if rec := postHeaders(t, f.central, f.adminSession, host, map[string]any{"remove": []string{"Authorization"}}); rec.Code != http.StatusOK {
		t.Fatalf("removing a header from an edge tunnel returned %d: %s", rec.Code, rec.Body.String())
	}
	waitUntil(t, stated("the edge to stop injecting Authorization and keep X-Debug"), func() bool {
		l := leaseSnapshot(f.edge, host)
		if l == nil {
			return false
		}
		_, still := l.AddedHeaders["Authorization"]
		return !still && l.AddedHeaders["X-Debug"] == "1"
	})
}

func TestNobodyElseCanEditAnEdgeTunnelsHeaders(t *testing.T) {
	f := startEdgeDiagFleet(t)
	registerThroughEdgeWithHeaders(t, f.edge, "pat-edge-diagnostics", "edgeown", map[string]string{"Authorization": "Bearer secret"})
	host := edgeHostFor(t, f.central, "edgeown")
	_, stranger := seedDiagnosticsUser(t, f.central, "stranger@example.com", "user")

	if rec := postHeaders(t, f.central, stranger, host, map[string]any{"remove": []string{"Authorization"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("another user's edit of an edge tunnel returned %d, want 403", rec.Code)
	}
	if el, _ := f.central.findEdgeLease(host); len(el.AddedHeaderNames) != 1 {
		t.Fatalf("a refused edit still changed central's copy: %v", el.AddedHeaderNames)
	}
}

func TestRateLimitOverrideReachesATunnelServedByAnEdge(t *testing.T) {
	f := startEdgeDiagFleet(t)
	host := edgeHostFor(t, f.central, "edgediag")

	rec := postAs(t, f.central, func(w http.ResponseWriter, r *http.Request) {
		f.central.handleAdminOverrideRateLimit(w, r, "edgeadmin@example.com")
	}, "/api/admin/leases/rate-limit", f.adminSession, map[string]any{"host": host, "rate_limit": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("overriding an edge tunnel's rate limit returned %d: %s", rec.Code, rec.Body.String())
	}
	waitUntil(t, stated("the edge to hold the tunnel to 7 RPS"), func() bool {
		l := leaseSnapshot(f.edge, host)
		return l != nil && l.RateLimit == 7
	})
	if el, _ := f.central.findEdgeLease(host); el.RateLimit != 7 {
		t.Fatalf("central's copy says %d RPS; the portal would show the old limit", el.RateLimit)
	}
}
