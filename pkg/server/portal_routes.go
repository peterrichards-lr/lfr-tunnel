package server

import (
	"io/fs"
	"log"
	"net/http"
	"strings"
)

// Portal page loads (#2528, epic #2524).
//
// V2 is the portal, at /portal. V1 is kept for admins only, at classic_portal_path, in case V2
// missed something. Every older entry point redirects to the V2 page that shows the same thing:
//
//	/                       -> /portal
//	/admin, /admin/<v1>     -> /portal, or the V2 page for that V1 section
//	/portal/<v1>            -> the V2 page for that V1 section, where it is not that path already
//	/portalv2/<rest>        -> /portal/<rest>
//
// 302, not 301: browsers keep a 301 indefinitely, so a mistake here -- or deciding to undo it --
// would stay stuck in every browser that saw it (the reason #1477 chose 302). The query string is
// kept on every redirect: a magic link's token, ?lang=, ?cli_login=1 and ?sso_mfa=1 all ride on it.
//
// The table of V1 sections is PortalV1SectionToV2. A fragment (/portal#users, from bookmarks made
// before #1513) never reaches the server, so it is not mapped: it lands on V2's dashboard.

// routePortalPage serves or redirects a GET or HEAD for a portal page, and reports whether it did.
func (s *Server) routePortalPage(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path

	if s.isClassicPortalPath(p) {
		// The path is unadvertised, but that is not the control: this check is. Anyone without an
		// admin session -- signed out included -- gets V2, the same as anyone else.
		if _, _, ok := s.portalSessionAdmin(r); ok {
			s.serveDashboardHTML(w, r)
		} else {
			redirectKeepingQuery(w, r, PortalV2Base)
		}
		return true
	}

	// A sign-in already in flight when the gateway moved V2 to /portal: V1 emailed magic links as
	// /portal?token=... and sent SSO's code prompt to /admin?sso_mfa=1. V2 reads both only on its
	// sign-in page -- at its root the router goes to /dashboard and drops the query.
	if (p == "/" || p == "/admin" || p == PortalV2Base || p == PortalV2Base+"/") && isSignInQuery(r) {
		redirectKeepingQuery(w, r, PortalV2Base+"/login")
		return true
	}

	switch {
	case p == "/":
		redirectKeepingQuery(w, r, PortalV2Base)
		return true

	case p == "/admin":
		redirectKeepingQuery(w, r, PortalV2Base)
		return true

	case strings.HasPrefix(p, "/admin/"):
		target := v1SectionRedirect(strings.TrimSuffix(strings.TrimPrefix(p, "/admin/"), "/"))
		if target == "" {
			target = PortalV2Base
		}
		redirectKeepingQuery(w, r, target)
		return true

	case p == "/portalv2" || strings.HasPrefix(p, "/portalv2/"):
		redirectKeepingQuery(w, r, PortalV2Base+strings.TrimPrefix(p, "/portalv2"))
		return true

	case p == PortalV2Base || strings.HasPrefix(p, PortalV2Base+"/"):
		// A V1 section link, /portal/<section>, from an old email or bookmark. Only where the V2
		// page has a different path: /portal/account and /portal/analytics are V2 pages already,
		// and redirecting them to themselves would loop.
		rest := strings.TrimPrefix(p, PortalV2Base+"/")
		if rest != p && rest != "" && !strings.Contains(rest, "/") {
			if target := v1SectionRedirect(rest); target != "" && target != p {
				redirectKeepingQuery(w, r, target)
				return true
			}
		}
		s.serveV2(w, r)
		return true
	}
	return false
}

// isSignInQuery reports whether the query carries a magic-link token or SSO's code prompt.
func isSignInQuery(r *http.Request) bool {
	q := r.URL.Query()
	return q.Get("token") != "" || q.Get("sso_mfa") == "1"
}

// isClassicPortalPath reports whether p is Portal V1's configured path or beneath it.
func (s *Server) isClassicPortalPath(p string) bool {
	base := s.cfg.ClassicPortalPath
	return base != "" && (p == base || strings.HasPrefix(p, base+"/"))
}

// classicPortalPathFor returns classic_portal_path when the request's session is an admin's, and
// "" otherwise: the path is given to admins and to nobody else (#2528).
func (s *Server) classicPortalPathFor(r *http.Request) string {
	if s.cfg.ClassicPortalPath == "" {
		return ""
	}
	if _, _, ok := s.portalSessionAdmin(r); !ok {
		return ""
	}
	return s.cfg.ClassicPortalPath
}

func redirectKeepingQuery(w http.ResponseWriter, r *http.Request, target string) {
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// serveV2 serves the V2 single-page app: a file from ui-dist when the path names one, and the
// app shell for every other path, which the router then reads.
func (s *Server) serveV2(w http.ResponseWriter, r *http.Request) {
	const shell = "index.html"
	subFS, err := fs.Sub(uiDistFS, "ui-dist")
	if err != nil {
		http.Error(w, "UI not built. Run 'make build' first.", http.StatusInternalServerError)
		return
	}
	// ui-dist is generated and no longer committed (#1196), so a binary built
	// with a bare `go build` embeds only the .gitkeep placeholder. Without this
	// the SPA fallback below would ask for an index.html that isn't there and
	// return a bare 404, which reads as a routing bug rather than a missing
	// build step.
	if _, statErr := fs.Stat(subFS, shell); statErr != nil {
		http.Error(w, "UI not built. Run 'make build' first.", http.StatusInternalServerError)
		return
	}

	cleanPath := strings.TrimPrefix(r.URL.Path, PortalV2Base)
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	if cleanPath == "" {
		cleanPath = shell
	}

	// Check if the file exists in the embedded FS
	if f, err := subFS.Open(cleanPath); err != nil {
		// SPA Fallback: Serve index.html for client-side routing
		r.URL.Path = PortalV2Base + "/"
		cleanPath = shell
	} else if err := f.Close(); err != nil {
		log.Printf("[Warning] Failed to close %s: %v", cleanPath, err)
	}

	if strings.HasSuffix(cleanPath, ".html") {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	}

	// The SPA shell is served with its root element rewritten to the resolved locale
	// (#2271), the same contract V1 gets in serveDashboardHTML and /privacy has had
	// since #1954. Every other file under ui-dist is a hashed asset and goes through
	// the FileServer below untouched.
	//
	// Written directly rather than through http.ServeContent because the body is no
	// longer the file: its length and modification time no longer describe what is on
	// the wire, and a Last-Modified/ETag derived from the embedded file would let a
	// cache serve one visitor's locale to the next. The no-store header above is
	// already set for exactly this document.
	if cleanPath == shell {
		if doc, readErr := fs.ReadFile(subFS, shell); readErr == nil {
			lang := s.ResolveLocale(r)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Vary", "Accept-Language")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(withDocumentLocale(string(doc), lang, GetDirection(lang)))); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
	}

	// Strip the base so the file server reads the path inside ui-dist.
	http.StripPrefix(PortalV2Base, http.FileServer(http.FS(subFS))).ServeHTTP(w, r)
}
