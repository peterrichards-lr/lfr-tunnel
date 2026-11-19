package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The three files PortalV1SectionToV2 has to agree with. Read rather than restated, so a
// section added to either portal is checked without anyone remembering this test exists.
const (
	v1DashboardHTML    = "dashboard.html"
	v2AppRoutes        = "../../ui/src/App.tsx"
	v2DashboardSection = "../../ui/src/dashboardSections.ts"
	v2DashboardPage    = "../../ui/src/pages/Dashboard.tsx"
	v2SourceDir        = "../../ui/src"
)

// v2SectionRoute is the route that makes /dashboard/<section> a real address. Without it
// every dashboard section path in the table would fall through to no route at all.
const v2SectionRoute = "/dashboard/:section?"

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// v1Sections returns V1's section names: the <id> of every `<div id="tab-<id>">` panel. The
// install guide's tab-content-* panels carry a class of their own and are not sections, the
// same distinction showTab draws (dashboard.js, #1215).
func v1Sections(html string) map[string]bool {
	html = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(html, " ")
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`<div id="tab-([a-z0-9-]+)"(?: class="hidden")?>`).FindAllStringSubmatch(html, -1) {
		out[m[1]] = true
	}
	return out
}

// v2Routes returns every `path="..."` declared in App.tsx. A route ending in an optional
// segment (`/x/:y?`) serves `/x` as well, so that is added too.
func v2Routes(tsx string) map[string]bool {
	out := map[string]bool{}
	optional := regexp.MustCompile(`/:[A-Za-z]+\?$`)
	for _, m := range regexp.MustCompile(`\bpath="([^"]+)"`).FindAllStringSubmatch(tsx, -1) {
		out[m[1]] = true
		if loc := optional.FindStringIndex(m[1]); loc != nil {
			out[m[1][:loc[0]]] = true
		}
	}
	return out
}

// v2DashboardSections returns DASHBOARD_SECTIONS from dashboardSections.ts as path segment
// -> element id.
func v2DashboardSections(ts string) map[string]string {
	out := map[string]string{}
	start := strings.Index(ts, "DASHBOARD_SECTIONS")
	if start < 0 {
		return out
	}
	body := ts[start:]
	open := strings.Index(body, "= {")
	end := strings.Index(body, "};")
	if open < 0 || end < open {
		return out
	}
	entry := regexp.MustCompile(`(?m)^\s*'?([a-z0-9-]+)'?\s*:\s*'([a-z0-9-]+)',?\s*$`)
	for _, m := range entry.FindAllStringSubmatch(body[open:end], -1) {
		out[m[1]] = m[2]
	}
	return out
}

// jumpNavAnchors returns the fragment targets of the dashboard's jump nav (#1218).
func jumpNavAnchors(tsx string) map[string]bool {
	out := map[string]bool{}
	start := strings.Index(tsx, `className="dashboard-jump-nav"`)
	if start < 0 {
		return out
	}
	end := strings.Index(tsx[start:], "</nav>")
	if end < 0 {
		return out
	}
	for _, m := range regexp.MustCompile(`<a href="#([a-z0-9-]+)"`).FindAllStringSubmatch(tsx[start:start+end], -1) {
		out[m[1]] = true
	}
	return out
}

// portalSectionProblems is the whole check, separated from the file reads so that the
// control below can feed it a table known to be wrong.
func portalSectionProblems(mapping map[string]string, v1 map[string]bool, routes map[string]bool, sections map[string]string) []string {
	var problems []string
	for s := range v1 {
		if _, ok := mapping[s]; !ok {
			problems = append(problems, "V1 section "+s+" has no V2 path in PortalV1SectionToV2")
		}
	}
	for s, path := range mapping {
		if !v1[s] {
			problems = append(problems, "PortalV1SectionToV2 maps "+s+", which is not a V1 section (no #tab-"+s+" in dashboard.html)")
		}
		if routes[path] {
			continue
		}
		if seg, ok := strings.CutPrefix(path, "/dashboard/"); ok {
			if !routes[v2SectionRoute] {
				problems = append(problems, s+" -> "+path+": App.tsx has no "+v2SectionRoute+" route")
			} else if _, known := sections[seg]; !known {
				problems = append(problems, s+" -> "+path+": "+seg+" is not in DASHBOARD_SECTIONS, so V2 would fall back to the top of the dashboard")
			}
			continue
		}
		problems = append(problems, s+" -> "+path+": no route in App.tsx serves this path")
	}
	sort.Strings(problems)
	return problems
}

// TestPortalV1SectionToV2 holds the V1 -> V2 table (#2527) against both portals. The
// redirects in #2528 send every /portal/<section> and /admin/<section> through it, so an
// entry that drifts lands a user on V2's catch-all -- a blank page, not an error.
func TestPortalV1SectionToV2(t *testing.T) {
	v1 := v1Sections(readSource(t, v1DashboardHTML))
	routes := v2Routes(readSource(t, v2AppRoutes))
	sections := v2DashboardSections(readSource(t, v2DashboardSection))

	// Each derivation checked before it is trusted: an empty set would make the comparisons
	// below pass over nothing.
	if !v1["overview"] || !v1["system"] || len(v1) < 15 {
		t.Fatalf("V1 section scan of %s is broken: found %d (%v)", v1DashboardHTML, len(v1), sortedKeys(v1))
	}
	if v1["content-macos"] {
		t.Fatalf("V1 section scan counted the install guide's tab-content-macos as a section")
	}
	if !routes["/admin/users"] || len(routes) < 15 {
		t.Fatalf("route scan of %s is broken: found %d (%v)", v2AppRoutes, len(routes), sortedKeys(routes))
	}
	if sections["tokens"] != "access-tokens" || len(sections) < 3 {
		t.Fatalf("DASHBOARD_SECTIONS scan of %s is broken: %v", v2DashboardSection, sections)
	}

	if problems := portalSectionProblems(PortalV1SectionToV2, v1, routes, sections); len(problems) > 0 {
		t.Errorf("PortalV1SectionToV2 has drifted from the portals:\n  %s", strings.Join(problems, "\n  "))
	}

	// CONTROL: the same check, given a table known to be wrong in each way it can be, must
	// say so. Without this a check that had stopped reading its inputs would look exactly
	// like one that passed.
	bad := map[string]string{}
	for k, v := range PortalV1SectionToV2 {
		bad[k] = v
	}
	delete(bad, "tokens")
	bad["users"] = "/admin/no-such-page"
	bad["tunnels"] = "/dashboard/no-such-section"
	bad["no-such-tab"] = "/dashboard"
	got := strings.Join(portalSectionProblems(bad, v1, routes, sections), "\n")
	for _, want := range []string{
		"V1 section tokens has no V2 path",
		"users -> /admin/no-such-page: no route",
		"tunnels -> /dashboard/no-such-section: no-such-section is not in DASHBOARD_SECTIONS",
		"maps no-such-tab, which is not a V1 section",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CONTROL: a deliberately wrong table did not report %q; got:\n%s", want, got)
		}
	}
}

// TestDashboardSectionsMatchThePage holds DASHBOARD_SECTIONS against the page it addresses:
// every anchor it names must exist, and it must cover every section the jump nav does, so a
// panel added to the dashboard is addressable by path as well as by fragment.
func TestDashboardSectionsMatchThePage(t *testing.T) {
	sections := v2DashboardSections(readSource(t, v2DashboardSection))
	if len(sections) < 3 {
		t.Fatalf("DASHBOARD_SECTIONS scan is broken: %v", sections)
	}
	nav := jumpNavAnchors(readSource(t, v2DashboardPage))
	if !nav["access-tokens"] || len(nav) < 3 {
		t.Fatalf("jump nav scan of %s is broken: %v", v2DashboardPage, sortedKeys(nav))
	}

	// Every id="..." literal anywhere in the V2 source: some anchors live in the panels the
	// dashboard renders (ReservationsPanel passes #registered-subdomains down as a prop).
	ids := map[string]bool{}
	idAttr := regexp.MustCompile(`\bid="([a-z0-9-]+)"`)
	err := filepath.WalkDir(v2SourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tsx") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range idAttr.FindAllStringSubmatch(string(b), -1) {
			ids[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", v2SourceDir, err)
	}
	if !ids["dashboard-overview"] {
		t.Fatalf("id scan of %s is broken: #dashboard-overview not found", v2SourceDir)
	}

	anchors := map[string]bool{}
	for seg, anchor := range sections {
		anchors[anchor] = true
		if !ids[anchor] {
			t.Errorf("DASHBOARD_SECTIONS[%q] = %q, but no element in ui/src carries id=%q: /dashboard/%s would land nowhere", seg, anchor, anchor, seg)
		}
	}
	for a := range nav {
		if !anchors[a] {
			t.Errorf("the jump nav links to #%s but DASHBOARD_SECTIONS has no path for it", a)
		}
	}
}
