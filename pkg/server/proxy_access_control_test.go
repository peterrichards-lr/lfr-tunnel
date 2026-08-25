package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	chserver "github.com/jpillora/chisel/server"
	"lfr-tunnel/pkg/config"
)

func newAccessTestRegistry(t *testing.T) *Registry {
	t.Helper()
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("chisel server: %v", err)
	}
	return NewRegistry(chiselServer)
}

func leaseFor(reg *Registry, host, subdomain string) *TunnelLease {
	lease := &TunnelLease{
		UserID:          "u1",
		SubdomainPrefix: subdomain,
		FullHost:        host,
		SessionToken:    "tok",
	}
	reg.Lock()
	reg.leases[host] = lease
	reg.sessionLeases["tok"] = append(reg.sessionLeases["tok"], lease)
	reg.Unlock()
	return lease
}

// #1367: an edge is stateless, so p.db is nil. Before access control moved onto the lease, this
// meant an edge evaluated no rules at all and served every protected tunnel to anyone. The gap
// was latent only because edges served no traffic (#1243) -- which #1244/#1249 exist to change.
func TestAccessControls_EnforcedOnAnEdgeWithNoDatabase(t *testing.T) {
	reg := newAccessTestRegistry(t)
	leaseFor(reg, "protected.lfr-demo.se", "protected")
	reg.SetAccessControlsForHost("protected.lfr-demo.se", "hunter2", "", "or")

	p := NewProxyHandler(reg, config.DefaultServerConfig())
	p.db = nil // an edge

	req := httptest.NewRequest("GET", "http://protected.lfr-demo.se/secret", nil)
	req.Host = "protected.lfr-demo.se"
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an edge served a passcode-protected tunnel without asking for the passcode: got %d", rec.Code)
	}
}

func TestAccessControls_EdgeEnforcesTheIPWhitelist(t *testing.T) {
	reg := newAccessTestRegistry(t)
	leaseFor(reg, "protected.lfr-demo.se", "protected")
	reg.SetAccessControlsForHost("protected.lfr-demo.se", "", "192.0.2.0/24", "or")

	p := NewProxyHandler(reg, config.DefaultServerConfig())
	p.db = nil

	req := httptest.NewRequest("GET", "http://protected.lfr-demo.se/secret", nil)
	req.Host = "protected.lfr-demo.se"
	req.RemoteAddr = "203.0.113.9:5555" // outside the whitelist
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("an edge served an IP-restricted tunnel to an address outside the whitelist: got %d", rec.Code)
	}
}

// An open tunnel is the common case, and the whole point of #1329: it must not touch the
// database. Enforced by giving the handler no database at all -- a query would panic or fail
// rather than quietly succeed.
func TestAccessControls_OpenTunnelNeedsNoDatabase(t *testing.T) {
	reg := newAccessTestRegistry(t)
	leaseFor(reg, "open.lfr-demo.se", "open")

	p := NewProxyHandler(reg, config.DefaultServerConfig())
	p.db = nil

	req := httptest.NewRequest("GET", "http://open.lfr-demo.se/", nil)
	req.Host = "open.lfr-demo.se"
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	// 502 means it got past access control and tried to reach the (absent) local server, which
	// is the pass-through outcome.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("an unprotected tunnel was not passed through: got %d", rec.Code)
	}
}

// A portal edit has to reach live leases, or turning a passcode *on* would do nothing until the
// client happened to reconnect.
func TestAccessControls_PortalEditReachesLiveLeases(t *testing.T) {
	reg := newAccessTestRegistry(t)
	leaseFor(reg, "a.lfr-demo.se", "shared")
	leaseFor(reg, "b.lfr-demo.se", "shared")
	leaseFor(reg, "other.lfr-demo.se", "other")

	updated := reg.SetAccessControlsForSubdomain("shared", "hunter2", "192.0.2.0/24", "and")
	if updated != 2 {
		t.Fatalf("updated %d leases, want the 2 for that subdomain", updated)
	}

	reg.RLock()
	pass, wl, mode := reg.leases["a.lfr-demo.se"].AccessControls()
	otherPass, _, _ := reg.leases["other.lfr-demo.se"].AccessControls()
	reg.RUnlock()

	if pass != "hunter2" || wl != "192.0.2.0/24" || mode != "and" {
		t.Errorf("edit did not reach the live lease: %q %q %q", pass, wl, mode)
	}
	if otherPass != "" {
		t.Error("the edit leaked onto a different subdomain's lease")
	}
}

// Registration is what puts the rules on the lease in the first place; if the host it computes
// does not match the one Register created, the rules land on nothing and the tunnel is served
// unprotected.
func TestApplyAccessControlsToLeases_MatchesTheRegisteredHost(t *testing.T) {
	reg := newAccessTestRegistry(t)
	leaseFor(reg, "peters.lfr-demo.se", "peters")
	leaseFor(reg, "peters.lfr-demo.online", "peters")

	applyAccessControlsToLeases(reg, "peters",
		[]string{"lfr-demo.se", "lfr-demo.online"},
		map[string][3]string{
			"lfr-demo.se":     {"se-pass", "", "or"},
			"lfr-demo.online": {"online-pass", "", "or"},
		})

	reg.RLock()
	se, _, _ := reg.leases["peters.lfr-demo.se"].AccessControls()
	online, _, _ := reg.leases["peters.lfr-demo.online"].AccessControls()
	reg.RUnlock()

	// Per domain, not per session: the same subdomain can carry different rules on each.
	if se != "se-pass" || online != "online-pass" {
		t.Errorf("rules were not applied per domain: se=%q online=%q", se, online)
	}
}

func TestAccessControls_DefaultModeIsOr(t *testing.T) {
	lease := &TunnelLease{}
	if _, _, mode := lease.AccessControls(); mode != "or" {
		t.Errorf("default access mode = %q, want \"or\"", mode)
	}
}
