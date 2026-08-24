package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

// An edge answers on its regional names but must issue tunnels on the shared domain, or the
// serving node ends up in the visitor's URL and a planned move changes it (#1285).
func TestGetActiveDomainsForRequest_TunnelDomainsRestrictIssuance(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"in.lfr-demo.se", "aws-edge-in.lfr-demo.se", "lfr-demo.se"}
	cfg.TunnelDomains = []string{"lfr-demo.se"}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Every host a client might have reached this edge on has to yield the shared domain --
	// including the regional one, which the contextual rule would otherwise match first.
	for _, host := range []string{"in.lfr-demo.se", "aws-edge-in.lfr-demo.se", "lfr-demo.se"} {
		req := httptest.NewRequest(http.MethodPost, "/api/register", nil)
		req.Host = host
		got := topRankedDomain(srv.getActiveDomainsForRequest(req, nil))
		if len(got) != 1 || got[0] != "lfr-demo.se" {
			t.Errorf("registering via %s: got domains %v, want [lfr-demo.se]", host, got)
		}
	}
}

// Unset means every served domain is issuable, which is what central and every single-gateway
// deployment relies on. This must not change under them.
func TestGetActiveDomainsForRequest_NoTunnelDomainsKeepsAllDomains(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se", "lfr-demo.online"}
	cfg.DomainAllocationRule = "preference"

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/register", nil)
	req.Host = "tunnel.example.invalid"
	got := srv.getActiveDomainsForRequest(req, nil)
	if len(got) != 2 || got[0] != "lfr-demo.se" || got[1] != "lfr-demo.online" {
		t.Errorf("got %v, want both configured domains in order", got)
	}
}

// A tunnel domain this gateway does not serve would produce a host no vhost matches: the
// client is told it succeeded and every visitor gets a 404. Dropped at startup instead.
func TestValidateTunnelDomains_DropsUnservedEntries(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:       []string{"in.lfr-demo.se", "LFR-Demo.se"},
		TunnelDomains: []string{" lfr-demo.se ", "", "somebody-elses.example"},
	}

	validateTunnelDomains(cfg)

	if len(cfg.TunnelDomains) != 1 || cfg.TunnelDomains[0] != "lfr-demo.se" {
		t.Fatalf("got %v, want only the served, normalised entry", cfg.TunnelDomains)
	}
}

// And when nothing survives, issuance falls back to the full domains list rather than to
// nothing at all -- a gateway that cannot issue anywhere is worse than the old behaviour.
func TestTunnelDomains_FallsBackWhenAllEntriesDropped(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"lfr-demo.se"}
	cfg.TunnelDomains = []string{"typo.example"}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	got := srv.tunnelDomains()
	if len(got) != 1 || got[0] != "lfr-demo.se" {
		t.Errorf("got %v, want the served domains as a fallback", got)
	}
}
