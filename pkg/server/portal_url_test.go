package server

import (
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

// An edge has no portal (#1473).
//
// The base URL is derived by prefixing "tunnel." onto a configured domain, which is correct for
// central -- its domain is the apex, and tunnel.<apex> is its hostname -- and wrong for every
// edge, whose domain is already a subdomain. Live, that produced
// https://tunnel.apac.lfr-demo.se/portal: no DNS record, no vhost, and not even caught by the
// zone wildcard, because *.example.com matches one label and "tunnel.apac" is two.
//
// It surfaces in the 401 a client gets when its token is rejected, so a developer is handed a
// dead link at exactly the moment they need the portal.

func TestGetPortalBaseURL_EdgePointsAtTheControlPlane(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		Domains:         []string{"apac.example.com"},
		ControlPlaneURL: "https://tunnel.example.com",
	}}

	req := httptest.NewRequest("POST", "https://apac.example.com/api/register", nil)
	req.Host = "apac.example.com"

	got := s.getPortalBaseURL(req)
	if got != "https://tunnel.example.com" {
		t.Errorf("an edge must point at the control plane's portal, got %q", got)
	}
	// The specific dead URL this issue is about.
	if got == "https://tunnel.apac.example.com" {
		t.Error("regressed to deriving the portal from the edge's own hostname")
	}
}

// A trailing slash on control_plane_url must not produce a double slash, since the caller
// appends "/portal".
func TestGetPortalBaseURL_TrimsTrailingSlash(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		Domains:         []string{"apac.example.com"},
		ControlPlaneURL: "https://tunnel.example.com/",
	}}
	req := httptest.NewRequest("POST", "https://apac.example.com/api/register", nil)
	req.Host = "apac.example.com"

	if got := s.getPortalBaseURL(req); got != "https://tunnel.example.com" {
		t.Errorf("expected the trailing slash trimmed, got %q", got)
	}
}

// Central must be unaffected: control_plane_url is empty there, so the existing derivation
// stands. Changing central's portal links would be a far worse bug than the one being fixed.
func TestGetPortalBaseURL_CentralUnchanged(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{Domains: []string{"example.com"}}}

	for _, host := range []string{"example.com", "someone.example.com", "tunnel.example.com"} {
		req := httptest.NewRequest("POST", "https://"+host+"/api/register", nil)
		req.Host = host
		if got := s.getPortalBaseURL(req); got != "https://tunnel.example.com" {
			t.Errorf("host %q: expected https://tunnel.example.com, got %q", host, got)
		}
	}
}

// A nil request is a real path (notification emails are sent outside a request), and an edge
// must still not invent its own portal there.
func TestGetPortalBaseURL_EdgeWithNoRequest(t *testing.T) {
	s := &Server{cfg: &config.ServerConfig{
		Domains:         []string{"apac.example.com"},
		ControlPlaneURL: "https://tunnel.example.com",
	}}
	if got := s.getPortalBaseURL(nil); got != "https://tunnel.example.com" {
		t.Errorf("expected the control plane's portal with no request, got %q", got)
	}
}
