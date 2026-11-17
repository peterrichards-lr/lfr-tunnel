package server

import (
	"net/http"
	"strings"
)

// The portal on one host (#2476).
//
// The gateway answers the portal on the apex, tunnel., portal. and api. of every configured
// domain -- in production lfr-demo.se and lfr-demo.online. #2470 moved only SSO sign-in to the
// canonical host, because the identity provider registers one redirect URI; everything else still
// worked everywhere, so users ended up split across hosts with host-only sessions (and the CLI,
// which opens portal.<domain>, landed a signed-in SSO user on a sign-in page). A browser loading a
// portal page on any other control name is now sent to the same path on the canonical host.
//
// Deliberately narrow:
//   - page loads only (GET/HEAD of a portal document path). /api/* -- clients, edges, the portal's
//     own calls -- keeps working on every host, and a redirected POST would lose its body.
//   - not the downloads or install scripts (/static/downloads/..., /install.sh), which curl and
//     PowerShell fetch from the apex; nor assets, which only a page already on some host requests.
//   - only the four control names of a configured domain: a tunnel under the same domain
//     (<name>.lfr-demo.online), an edge's own name, localhost and an IP are never redirected.
//   - 302, not 301: browsers keep a 301 indefinitely, so a wrong canonical setting -- or deciding
//     to undo this -- would stay stuck in every browser that saw it (the reason #1477 chose 302).

// isPortalDocumentPath reports whether p loads a portal page in either arm.
func isPortalDocumentPath(p string) bool {
	switch p {
	case "/", "/portal", "/admin", "/portalv2", "/setup", "/privacy", "/cookies":
		return true
	}
	return strings.HasPrefix(p, "/portal/") || strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/portalv2/")
}

// isConfiguredControlName reports whether host (lower-case, no port) is the apex, tunnel., portal.
// or api. of a configured domain.
func (s *Server) isConfiguredControlName(host string) bool {
	for _, d := range s.cfg.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || host == "tunnel."+d || host == "portal."+d || host == "api."+d {
			return true
		}
	}
	return false
}

// canonicalPortalRedirect returns where to send a portal page load on a non-canonical control
// host, or "" to serve it here. host is the request's host, lower-cased and without a port. The
// canonical host is ssoCanonicalBase's -- control_plane_url when set, else tunnel.<first domain>
// -- so sign-in and browsing agree on one host.
func (s *Server) canonicalPortalRedirect(r *http.Request, host string) string {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ""
	}
	if !isPortalDocumentPath(r.URL.Path) || !s.isConfiguredControlName(host) {
		return ""
	}
	base := s.ssoCanonicalBase(r)
	if base == nil || sameHost(r.Host, base.Host) || strings.EqualFold(host, base.Hostname()) {
		return ""
	}
	target := base.Scheme + "://" + base.Host + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target
}
