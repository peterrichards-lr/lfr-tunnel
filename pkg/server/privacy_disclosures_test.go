package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The served /privacy page is a second privacy text (#1954). scripts/check-privacy-disclosures.cjs
// holds it against PRIVACY.md, but it reads FILES -- it cannot tell whether what it checked is what
// a user is served. This test covers the other half of that path.
//
// It also replaces an assertion that could not fail. TestServer_FallbackPages asserts only
// "not 500", and handlePrivacyFallback writes StatusOK BEFORE it renders
// (pkg/server/server.go:5798) -- so a template that fails to parse still produces a 200 carrying
// the hardcoded "Under maintenance" stub, and that assertion is satisfied by the failure it exists
// to catch. Here the absence of that stub is asserted directly.

var disclosureMarkerRe = regexp.MustCompile(`data-disclosure="([^"]*)"`)

// privacyDisclosureMarkers returns the sorted, de-duplicated disclosure letters in html.
func privacyDisclosureMarkers(html string) []string {
	seen := map[string]bool{}
	for _, m := range disclosureMarkerRe.FindAllStringSubmatch(html, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// servePrivacy renders /privacy for one locale through the real handler.
func servePrivacy(t *testing.T, srv *Server, lang string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.com/privacy?lang="+lang, nil)
	if err != nil {
		t.Fatalf("building request for %q: %v", lang, err)
	}
	w := httptest.NewRecorder()
	srv.handlePrivacyFallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/privacy?lang=%s returned %d, want 200", lang, w.Code)
	}
	body := w.Body.String()

	// THE assertion that distinguishes "rendered the policy" from "failed and degraded". The
	// handler swallows a render error and writes this stub, with a 200 already on the wire.
	if strings.Contains(body, "Under maintenance") {
		t.Fatalf("/privacy?lang=%s served the hardcoded degradation stub, so the template did not render:\n%s",
			lang, body)
	}
	return body
}

// TestPrivacyPageServesEveryDisclosureInEveryLanguage asserts that every locale is served the same
// set of disclosure categories English is served.
//
// English is the reference rather than a hardcoded A-E list on purpose: when PRIVACY.md gains a
// category, check-privacy-disclosures.cjs is what requires the templates to gain it, and this test
// then requires every language to be served it. Hardcoding the letters here would mean editing
// this file to describe a gap instead of failing on it.
func TestPrivacyPageServesEveryDisclosureInEveryLanguage(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	want := privacyDisclosureMarkers(servePrivacy(t, srv, "en"))

	// Anti-vacuity. If English serves no markers, every comparison below is trivially satisfied
	// and this test passes forever on a page that discloses nothing.
	if len(want) == 0 {
		t.Fatal("the English /privacy page served no data-disclosure markers at all; " +
			"refusing to report that the other locales match it")
	}

	// Every locale ResolveLocale accepts, not only the five with their own template: the ones
	// without fall back to the English template (renderEmailTemplate), and that fallback serving a
	// complete page is itself the property. A locale that silently served a page with no
	// disclosures would be the #1954 defect in a new form.
	for _, lang := range []string{"en", "es", "fr", "ro", "ar", "de", "pt", "ko", "ja", "zh"} {
		t.Run(lang, func(t *testing.T) {
			got := privacyDisclosureMarkers(servePrivacy(t, srv, lang))
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("/privacy?lang=%s serves disclosures %v, English serves %v", lang, got, want)
			}
		})
	}
}

// TestPrivacyPageDeclaresItsLocaleAndDirection pins the RTL contract. The Arabic page is the same
// markup as the others and relies entirely on dir="rtl" reaching the root element; if Dir stopped
// being threaded through, the page would still render, still pass every check above, and be laid
// out left-to-right for every Arabic reader.
func TestPrivacyPageDeclaresItsLocaleAndDirection(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	for lang, wantDir := range map[string]string{"en": "ltr", "es": "ltr", "ar": "rtl"} {
		body := servePrivacy(t, srv, lang)
		if !strings.Contains(body, `lang="`+lang+`"`) {
			t.Errorf("/privacy?lang=%s does not declare lang=%q on the root element", lang, lang)
		}
		if !strings.Contains(body, `dir="`+wantDir+`"`) {
			t.Errorf("/privacy?lang=%s does not declare dir=%q", lang, wantDir)
		}
	}
}

// serveDashboardShell renders Portal V1 through the real router and returns the document.
//
// Through ServeHTTP rather than by calling serveDashboardHTML directly: what #2271 changed is that
// the handler now needs the REQUEST, and a test that hands it one itself cannot notice a route that
// stops passing a real one. The host has to be a configured domain or this is a data-plane request
// and never reaches the portal at all.
func serveDashboardShell(t *testing.T, srv *Server, path string, headers map[string]string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200", path, w.Code)
	}

	// The document depends on a request header now, so a cache keyed on the URL alone could hand
	// one visitor's locale to the next. `?lang=` is already part of that key; Accept-Language is
	// not, and the whole point of resolving it server-side is that it reaches first paint.
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Language") {
		t.Errorf("GET %s answered Vary: %q, want it to include Accept-Language", path, v)
	}

	body := w.Body.String()

	// Anchor, per the rule that an absence check needs something present to stand on: every
	// assertion below is about ONE attribute, and would be satisfied by an error page, a redirect
	// body or an empty response just as happily as by the portal. This marker is written by
	// serveDashboardHTML's cache-busting rewrite and by nothing else.
	if !strings.Contains(body, "/static/dashboard.js?v=") {
		t.Fatalf("GET %s did not serve the Portal V1 shell (no cache-busted dashboard.js):\n%.400s", path, body)
	}
	return body
}

// TestPortalShellsDeclareTheResolvedLocale is TestPrivacyPageDeclaresItsLocaleAndDirection's
// contract applied to the two documents the browser parses FIRST (#2271).
//
// #2262 made both portals assign document.documentElement.lang wherever they assign dir, which
// fixed the steady state and could not reach the window before the bundle runs: `<html lang="en">`
// was literal in pkg/server/dashboard.html and ui/index.html, so every reload declared English
// until applyTranslations() (V1) or the I18nProvider effect (V2) ran. A screen reader that has
// begun announcing the document does not necessarily re-voice it when the attribute changes
// underneath it, and an RTL layout painted LTR first.
//
// Asserted as a property over the whole supported set rather than as the Arabic instance: a fix
// that special-cased one locale, or that threaded dir and forgot lang again, goes red here.
func TestPortalShellsDeclareTheResolvedLocale(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Every locale ResolveLocale accepts (pkg/server/i18n.go). Arabic is the only one whose
	// direction differs, which is exactly why the others are here: dir="ltr" has to be declared
	// too, or an Arabic reader who switches back is left in an RTL document.
	locales := []string{"en", "es", "fr", "de", "pt", "ko", "ja", "zh", "ro", "ar"}

	t.Run("v1 honours ?lang=", func(t *testing.T) {
		for _, lang := range locales {
			body := serveDashboardShell(t, srv, "/admin?lang="+lang, nil)
			assertRootElementDeclares(t, body, "/admin?lang="+lang, lang, GetDirection(lang))
		}
	})

	t.Run("v1 honours Accept-Language", func(t *testing.T) {
		// The half of the resolution the server can do for a visitor who has never chosen: no
		// query string, no localStorage the server could read. `?lang=` alone passing would leave
		// every first-time RTL visitor with an LTR first paint.
		for _, lang := range locales {
			body := serveDashboardShell(t, srv, "/", map[string]string{
				"Accept-Language": lang + "-XX," + lang + ";q=0.9,en;q=0.8",
			})
			assertRootElementDeclares(t, body, "/ with Accept-Language: "+lang, lang, GetDirection(lang))
		}
	})

	t.Run("v1 serves the shell under /portal/ and /admin/ too", func(t *testing.T) {
		// #1513 made V1 answer beneath those prefixes as well, from a second call site. A fix
		// applied to one call site and not the other is this repo's most common defect shape.
		for _, path := range []string{"/portal", "/portal/tunnels", "/admin/users"} {
			body := serveDashboardShell(t, srv, path+"?lang=ar", nil)
			assertRootElementDeclares(t, body, path, "ar", "rtl")
		}
	})

	t.Run("an unsupported locale declares English, not a language it is not rendering", func(t *testing.T) {
		// BOUNDING, not FIRING: `lang="en"` is what the unfixed code served too, so this case
		// passes against it and is no evidence the fix works. It pins the deliberate edge, which
		// is reachable -- `?lang=` is arbitrary input, and Portal V2 seeds the shared `lfr_lang`
		// preference from navigator.language, so `it` arrives from a browser nobody configured.
		// The server answers an unsupported locale with the English bundle (handleGetI18n), so
		// echoing `it` would declare a language the page is not rendering.
		body := serveDashboardShell(t, srv, "/admin?lang=it", nil)
		assertRootElementDeclares(t, body, "/admin?lang=it", "en", "ltr")
	})

	t.Run("v2 shell", func(t *testing.T) {
		// The route cannot be asserted here. Portal V2's shell reaches the binary through
		// `//go:embed ui-dist/*`, which is generated and never committed (#1196) -- CI's Go test
		// jobs stub it as an EMPTY pkg/server/ui-dist/index.html, so a served-route assertion
		// would be measuring a zero-byte file and passing or failing for reasons unrelated to
		// locale. What is asserted instead is the same production function applied to the same
		// production source file the build copies in, so a shell whose root element drifts out of
		// the shape the rewrite needs fails here. The served route is covered against a real build
		// by tests/e2e/ui/tests/portal_shell_locale_first_byte.spec.ts.
		shellBytes, err := os.ReadFile(filepath.Join("..", "..", "ui", "index.html"))
		if err != nil {
			t.Fatalf("reading Portal V2's shell: %v (this test is worthless if the path moved)", err)
		}
		shell := string(shellBytes)

		// Anti-vacuity, both halves. withDocumentLocale returns a document with no root element
		// tag unchanged, so without the first check a moved or emptied file would report success;
		// without the second, a shell that already said lang="ar" would satisfy the assertion
		// below whatever the function did.
		if htmlOpenTagRe.FindStringIndex(shell) == nil {
			t.Fatal("ui/index.html has no root element tag, so nothing here can be rewritten")
		}
		if strings.Contains(shell, `lang="ar"`) {
			t.Fatal("ui/index.html already declares Arabic; this test could not tell a rewrite from the source")
		}

		for _, lang := range locales {
			got := withDocumentLocale(shell, lang, GetDirection(lang))
			assertRootElementDeclares(t, got, "ui/index.html rewritten to "+lang, lang, GetDirection(lang))
		}
	})
}

// assertRootElementDeclares fails unless doc's FIRST root element tag carries exactly lang and dir.
//
// It reads the tag rather than the document because `strings.Contains(body, "lang=\"ar\"")` is
// satisfied by any element anywhere -- a `lang` on a <span>, or the string inside a script -- and
// because a duplicate attribute is the specific way this fix could regress: HTML keeps the FIRST
// of a repeated attribute, so appending `lang="ar"` after a hardcoded `lang="en"` changes nothing
// a browser does while leaving the document containing the text being searched for.
func assertRootElementDeclares(t *testing.T, doc, what, wantLang, wantDir string) {
	t.Helper()

	tag := htmlOpenTagRe.FindString(doc)
	if tag == "" {
		t.Fatalf("%s: served no root element tag at all", what)
	}
	if got := attrCountIn(tag, "lang"); got != 1 {
		t.Errorf("%s: root element carries %d lang attributes, want exactly 1 -- %s", what, got, tag)
	}
	if got := attrCountIn(tag, "dir"); got != 1 {
		t.Errorf("%s: root element carries %d dir attributes, want exactly 1 -- %s", what, got, tag)
	}
	if !strings.Contains(tag, `lang="`+wantLang+`"`) {
		t.Errorf("%s: root element is %s, want lang=%q", what, tag, wantLang)
	}
	if !strings.Contains(tag, `dir="`+wantDir+`"`) {
		t.Errorf("%s: root element is %s, want dir=%q", what, tag, wantDir)
	}
}

func attrCountIn(tag, name string) int {
	return len(regexp.MustCompile(`(?is)\s`+name+`\s*=`).FindAllString(tag, -1))
}

// TestWithDocumentLocaleRewritesRatherThanAppends covers the cases neither shell exhibits today and
// which a future edit to either one could introduce -- an unquoted or single-quoted attribute, an
// unrelated attribute that must survive, an attribute order that puts dir first.
//
// Written against the real function, with the real shells as two of the inputs.
func TestWithDocumentLocaleRewritesRatherThanAppends(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no attributes", "<!DOCTYPE html><html><head></head></html>", `<html lang="ja" dir="ltr">`},
		{"the shipped shape", `<!DOCTYPE html><html lang="en" dir="ltr">`, `<html lang="ja" dir="ltr">`},
		{"single-quoted", `<html lang='en'>`, `<html lang="ja" dir="ltr">`},
		{"unquoted", `<html lang=en dir=ltr>`, `<html lang="ja" dir="ltr">`},
		{"dir first", `<html dir="rtl" lang="ar">`, `<html lang="ja" dir="ltr">`},
		{"other attributes survive", `<html lang="en" class="no-js" data-x="1">`, `<html lang="ja" dir="ltr" class="no-js" data-x="1">`},
		{"uppercase tag", `<HTML LANG="EN">`, `<html lang="ja" dir="ltr">`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withDocumentLocale(tc.in, "ja", "ltr")
			if !strings.Contains(got, tc.want) {
				t.Errorf("withDocumentLocale(%q) = %q, want it to contain %q", tc.in, got, tc.want)
			}
			// The point of the function, stated separately: the old value is GONE, not shadowed.
			if strings.Contains(strings.ToLower(htmlOpenTagRe.FindString(got)), "en") {
				t.Errorf("withDocumentLocale(%q) = %q -- the previous locale survived on the tag", tc.in, got)
			}
		})
	}

	// A document with nothing to rewrite comes back byte-identical rather than half-built. This is
	// the CI stub (an empty ui-dist/index.html), and a function that synthesised a tag would make
	// that stub look like a portal.
	for _, empty := range []string{"", "not html at all"} {
		if got := withDocumentLocale(empty, "ja", "ltr"); got != empty {
			t.Errorf("withDocumentLocale(%q) = %q, want it unchanged", empty, got)
		}
	}
}
