package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// One set of theme files, shared by both portals (#1522).
//
// The tokens used to be duplicated: Portal V1 defined its own twenty in dashboard.css, Portal V2
// defined sixty-odd in ui/src/themes. So `liferay` existed only in V2 -- #1201 papered over that
// by showing V1 users a disabled option -- and the contrast gate, which discovers theme files,
// covered V2 alone while V1's colours went unchecked.
//
// They now live in static/themes and are consumed twice over: V2 @imports them into its bundle
// at build time, V1 links them at runtime. That second path is the fragile one, because it
// depends on the files being embedded and served, and nothing else in the suite would notice if
// they stopped being.

// TestSharedThemesAreEmbedded is the load-bearing assumption: `//go:embed static/*` has to reach
// into the new subdirectory. If it does not, V1 links three stylesheets that 404 and silently
// renders with no tokens at all.
func TestSharedThemesAreEmbedded(t *testing.T) {
	for _, name := range []string{"dark", "light", "liferay"} {
		path := "static/themes/" + name + ".css"
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is not embedded, so Portal V1 would 404 on it: %v", path, err)
		}
		if !strings.Contains(string(data), "--bg-base") {
			t.Errorf("%s is embedded but carries no tokens", path)
		}
	}
}

// TestSharedThemesAreServed — embedded is not the same as reachable. V1 links them by URL.
func TestSharedThemesAreServed(t *testing.T) {
	for _, name := range []string{"dark", "light", "liferay"} {
		url := "/static/themes/" + name + ".css"
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+url, nil)
		w := httptest.NewRecorder()
		http.FileServer(http.FS(staticFS)).ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s returned %d; Portal V1 links this and would render untokenised", url, w.Code)
		}
	}
}

// TestPortalV1LinksTheSharedThemes — the link tags have to exist, and have to come BEFORE
// dashboard.css so V1's own component rules can still override a token.
func TestPortalV1LinksTheSharedThemes(t *testing.T) {
	html, err := staticFS.ReadFile("../dashboard.html")
	if err != nil {
		// dashboard.html is embedded separately, not under static/.
		raw, readErr := os.ReadFile(filepath.Join("dashboard.html"))
		if readErr != nil {
			t.Fatalf("could not read dashboard.html: %v / %v", err, readErr)
		}
		html = raw
	}
	doc := string(html)

	last := -1
	for _, name := range []string{"dark", "light", "liferay"} {
		idx := strings.Index(doc, "/static/themes/"+name+".css")
		if idx < 0 {
			t.Fatalf("Portal V1 does not link the shared %s theme", name)
		}
		last = max(last, idx)
	}

	own := strings.Index(doc, "/static/dashboard.css")
	if own < 0 {
		t.Fatal("dashboard.css link disappeared")
	}
	if last > own {
		t.Error("the shared themes are linked AFTER dashboard.css, so a V1 component rule can no " +
			"longer override a token -- order is the only thing making that possible")
	}
}

// TestPortalV1DefinesNoThemeTokens is the property that stops the duplication returning.
//
// V1 may keep component rules that key off a theme -- :root[data-theme='light'] .config-tree-key
// and friends are V1 markup. What it must not do is declare custom properties in a :root block
// again, because then the two portals disagree and nothing says so.
func TestPortalV1DefinesNoThemeTokens(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "dashboard.css"))
	if err != nil {
		t.Fatalf("reading dashboard.css: %v", err)
	}
	// Comments first: the header explaining this very rule names tokens.
	stripped := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), "")

	// A :root selector that is not qualified by a descendant -- i.e. a token block rather than
	// a component rule like `:root[data-theme='light'] .config-tree-key`.
	blocks := regexp.MustCompile(`:root(\[[^\]]*\])?\s*\{([^}]*)\}`).FindAllStringSubmatch(stripped, -1)
	for _, b := range blocks {
		if strings.Contains(b[2], "--") {
			t.Errorf("dashboard.css declares theme tokens again:\n%s\n\n"+
				"Tokens belong in static/themes/, shared with Portal V2 (#1522). Component rules "+
				"are fine; a :root block full of custom properties is how the two portals drifted.",
				strings.TrimSpace(b[0][:min(200, len(b[0]))]))
		}
	}
}
