package server

import (
	"net/http"
	"net/http/httptest"
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
