package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config/configtest"
)

// The files PortalV1SectionToV2 has to agree with. Read rather than restated, so a section
// added to either portal is checked without anyone remembering this test exists.
const (
	v1DashboardHTML    = "dashboard.html"
	v1DashboardJS      = "static/dashboard.js"
	v2AppRoutes        = "../../ui/src/App.tsx"
	v2DashboardSection = "../../ui/src/dashboardSections.ts"
	v2DashboardPage    = "../../ui/src/pages/Dashboard.tsx"
	v2SourceDir        = "../../ui/src"
)

// v2SectionRoute is the route that makes /dashboard/<section> a real address. Without it
// every dashboard section path in the table would fall through to no route at all.
const v2SectionRoute = "/dashboard/:section?"

// v1AdminSectionsOnOpenV2Pages lists the admin-only V1 sections deliberately mapped to a V2
// page every user can open, with the reason. An admin-only section may land on either kind
// of page, but landing on an open one has to be said here; an open V1 section landing on an
// admin-only V2 page is always wrong, because the redirect would bounce its non-admin users
// to /dashboard. Empty today: every admin-only V1 section maps to an admin-only V2 page.
var v1AdminSectionsOnOpenV2Pages = map[string]string{}

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// v1Sections returns V1's section names by the rule showTab itself uses (dashboard.js,
// #1215): every <div> that is a direct child of .main-content and whose id starts with
// "tab-". Structural rather than a pattern on the tag's text, so attribute order or an extra
// class cannot drop a section -- and the install guide's tab-content-* panels, which are
// nested deeper, are not sections.
func v1Sections(html string) map[string]bool {
	html = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(html, " ")
	tag := regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b([^>]*)>`)
	idAttr := regexp.MustCompile(`\bid="([^"]+)"`)
	classAttr := regexp.MustCompile(`\bclass="([^"]*)"`)
	void := map[string]bool{
		"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true,
		"source": true, "track": true, "area": true, "base": true, "col": true,
		"embed": true, "param": true, "wbr": true,
	}
	type open struct {
		tag  string
		main bool
	}
	var stack []open
	out := map[string]bool{}
	for _, m := range tag.FindAllStringSubmatch(html, -1) {
		closing, name, attrs := m[1] != "", strings.ToLower(m[2]), m[3]
		if closing {
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k].tag == name {
					stack = stack[:k]
					break
				}
			}
			continue
		}
		if name == "div" && len(stack) > 0 && stack[len(stack)-1].main {
			if a := idAttr.FindStringSubmatch(attrs); a != nil && strings.HasPrefix(a[1], "tab-") {
				out[strings.TrimPrefix(a[1], "tab-")] = true
			}
		}
		if void[name] || strings.HasSuffix(strings.TrimSpace(attrs), "/") {
			continue
		}
		isMain := false
		if c := classAttr.FindStringSubmatch(attrs); c != nil {
			for _, cls := range strings.Fields(c[1]) {
				isMain = isMain || cls == "main-content"
			}
		}
		stack = append(stack, open{name, isMain})
	}
	return out
}

// v1AdminOnlySections returns dashboard.js's ADMIN_ONLY_TABS: the sections showTab refuses
// to a non-admin.
func v1AdminOnlySections(js string) map[string]bool {
	out := map[string]bool{}
	m := regexp.MustCompile(`(?s)const ADMIN_ONLY_TABS = \[(.*?)\]`).FindStringSubmatch(js)
	if m == nil {
		return out
	}
	for _, q := range regexp.MustCompile(`'([a-z0-9-]+)'`).FindAllStringSubmatch(m[1], -1) {
		out[q[1]] = true
	}
	return out
}

// stripTSXComments blanks JSX `{/* */}`, block and whole-line `//` comments, so a route
// that has been commented out is not read as one that exists.
func stripTSXComments(tsx string) string {
	tsx = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(tsx, " ")
	return regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(tsx, "")
}

// v2Routes returns every `path="..."` declared in App.tsx, and which of them sit inside the
// <Route element={<AdminRoute />}> group. A route ending in an optional segment (`/x/:y?`)
// serves `/x` as well, so that is added too.
func v2Routes(tsx string) (routes, admin map[string]bool) {
	tsx = stripTSXComments(tsx)
	routes, admin = map[string]bool{}, map[string]bool{}
	adminStart, adminEnd := -1, -1
	if i := strings.Index(tsx, "<Route element={<AdminRoute />}>"); i >= 0 {
		if j := strings.Index(tsx[i:], "</Route>"); j >= 0 {
			adminStart, adminEnd = i, i+j
		}
	}
	optional := regexp.MustCompile(`/:[A-Za-z]+\?$`)
	for _, loc := range regexp.MustCompile(`\bpath="([^"]+)"`).FindAllStringSubmatchIndex(tsx, -1) {
		path := tsx[loc[2]:loc[3]]
		paths := []string{path}
		if o := optional.FindStringIndex(path); o != nil {
			paths = append(paths, path[:o[0]])
		}
		for _, p := range paths {
			routes[p] = true
			if loc[0] > adminStart && loc[0] < adminEnd {
				admin[p] = true
			}
		}
	}
	return routes, admin
}

// v2Basename returns the BrowserRouter basename declared in App.tsx.
func v2Basename(tsx string) string {
	m := regexp.MustCompile(`\bbasename="([^"]+)"`).FindStringSubmatch(stripTSXComments(tsx))
	if m == nil {
		return ""
	}
	return m[1]
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

// portalSurfaces is everything the table is checked against, read from both portals.
type portalSurfaces struct {
	v1, v1Admin         map[string]bool
	routes, adminRoutes map[string]bool
	sections            map[string]string
	adminOnOpen         map[string]string
}

// portalSectionProblems is the whole check, separated from the file reads so that the
// control below can feed it a table known to be wrong.
func portalSectionProblems(mapping map[string]string, ps portalSurfaces) []string {
	var problems []string
	for s := range ps.v1 {
		if _, ok := mapping[s]; !ok {
			problems = append(problems, "V1 section "+s+" has no V2 path in PortalV1SectionToV2")
		}
	}
	for s, path := range mapping {
		if !ps.v1[s] {
			problems = append(problems, "PortalV1SectionToV2 maps "+s+", which is not a V1 section (no #tab-"+s+" in dashboard.html)")
		}
		route := path
		if !ps.routes[path] {
			seg, ok := strings.CutPrefix(path, "/dashboard/")
			switch {
			case !ok:
				problems = append(problems, s+" -> "+path+": no route in App.tsx serves this path")
				continue
			case !ps.routes[v2SectionRoute]:
				problems = append(problems, s+" -> "+path+": App.tsx has no "+v2SectionRoute+" route")
				continue
			}
			if _, known := ps.sections[seg]; !known {
				problems = append(problems, s+" -> "+path+": "+seg+" is not in DASHBOARD_SECTIONS, so V2 would fall back to the top of the dashboard")
				continue
			}
			route = v2SectionRoute
		}
		// Who may see it has to survive the move as well as what it shows.
		switch adminV2 := ps.adminRoutes[route]; {
		case !ps.v1Admin[s] && adminV2:
			problems = append(problems, s+" -> "+path+": "+s+" is open to every user in V1, but "+path+" is admin-only in V2 (inside <AdminRoute />), so a non-admin would be bounced to /dashboard")
		case ps.v1Admin[s] && !adminV2 && ps.adminOnOpen[s] == "":
			problems = append(problems, s+" -> "+path+": "+s+" is admin-only in V1 (ADMIN_ONLY_TABS) but "+path+" is open to every user in V2; if that is intended, say why in v1AdminSectionsOnOpenV2Pages")
		}
	}
	sort.Strings(problems)
	return problems
}

// TestPortalV1SectionToV2 holds the V1 -> V2 table (#2527) against both portals. The
// redirects in #2528 send every /portal/<section> and /admin/<section> through it, so an
// entry that drifts lands a user on V2's catch-all -- a blank page, not an error.
func TestPortalV1SectionToV2(t *testing.T) {
	app := readSource(t, v2AppRoutes)
	routes, adminRoutes := v2Routes(app)
	ps := portalSurfaces{
		v1:          v1Sections(readSource(t, v1DashboardHTML)),
		v1Admin:     v1AdminOnlySections(readSource(t, v1DashboardJS)),
		routes:      routes,
		adminRoutes: adminRoutes,
		sections:    v2DashboardSections(readSource(t, v2DashboardSection)),
		adminOnOpen: v1AdminSectionsOnOpenV2Pages,
	}

	// Each derivation checked before it is trusted: an empty set would make the comparisons
	// below pass over nothing.
	if !ps.v1["overview"] || !ps.v1["system"] || len(ps.v1) < 15 {
		t.Fatalf("V1 section scan of %s is broken: found %d (%v)", v1DashboardHTML, len(ps.v1), sortedKeys(ps.v1))
	}
	for s := range ps.v1 {
		if strings.HasPrefix(s, "content-") || strings.HasPrefix(s, "btn-") {
			t.Fatalf("V1 section scan counted the install guide's tab-%s as a section", s)
		}
	}
	if !ps.v1Admin["users"] || ps.v1Admin["tokens"] || len(ps.v1Admin) < 10 {
		t.Fatalf("ADMIN_ONLY_TABS scan of %s is broken: %v", v1DashboardJS, sortedKeys(ps.v1Admin))
	}
	for s := range ps.v1Admin {
		if !ps.v1[s] {
			t.Errorf("ADMIN_ONLY_TABS names %s, which is not a V1 section", s)
		}
	}
	if !ps.routes["/admin/users"] || len(ps.routes) < 15 {
		t.Fatalf("route scan of %s is broken: found %d (%v)", v2AppRoutes, len(ps.routes), sortedKeys(ps.routes))
	}
	if !ps.adminRoutes["/admin/users"] || ps.adminRoutes["/dashboard"] || ps.adminRoutes["/account"] || len(ps.adminRoutes) < 10 {
		t.Fatalf("AdminRoute scan of %s is broken: %v", v2AppRoutes, sortedKeys(ps.adminRoutes))
	}
	if ps.sections["tokens"] != "access-tokens" || len(ps.sections) < 3 {
		t.Fatalf("DASHBOARD_SECTIONS scan of %s is broken: %v", v2DashboardSection, ps.sections)
	}

	// The table's paths are relative to the router's basename; if V2 moves (#2528 may mount
	// it at /portal), PortalV2Base has to move with it.
	if got := v2Basename(app); got != PortalV2Base {
		t.Errorf("App.tsx mounts V2 at basename %q but PortalV2Base is %q", got, PortalV2Base)
	}

	if problems := portalSectionProblems(PortalV1SectionToV2, ps); len(problems) > 0 {
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
	bad["account"] = "/admin/users"
	bad["backups"] = "/account"
	got := strings.Join(portalSectionProblems(bad, ps), "\n")
	for _, want := range []string{
		"V1 section tokens has no V2 path",
		"users -> /admin/no-such-page: no route",
		"tunnels -> /dashboard/no-such-section: no-such-section is not in DASHBOARD_SECTIONS",
		"maps no-such-tab, which is not a V1 section",
		"account -> /admin/users: account is open to every user in V1, but /admin/users is admin-only",
		"backups -> /account: backups is admin-only in V1 (ADMIN_ONLY_TABS) but /account is open",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CONTROL: a deliberately wrong table did not report %q; got:\n%s", want, got)
		}
	}
	// ... and an admin-only section on an open page is accepted once it is explained.
	explained := portalSurfaces{v1: ps.v1, v1Admin: ps.v1Admin, routes: ps.routes, adminRoutes: ps.adminRoutes, sections: ps.sections,
		adminOnOpen: map[string]string{"backups": "control"}}
	if got := strings.Join(portalSectionProblems(bad, explained), "\n"); strings.Contains(got, "backups -> ") {
		t.Errorf("CONTROL: an explained admin-only section on an open page was still reported:\n%s", got)
	}

	// CONTROL for the scanners themselves: a commented-out route is not a route, and a section
	// written with its attributes in another order is still a section.
	if r, _ := v2Routes(`{/* <Route path="/gone" element={<X />} /> */}` + "\n// <Route path=\"/gone2\" />\n" + `<Route path="/here" />`); r["/gone"] || r["/gone2"] || !r["/here"] {
		t.Errorf("CONTROL: route scan reads comments as routes, or misses a real one: %v", sortedKeys(r))
	}
	sample := `<div class="main-content"><div class="hidden x" id="tab-reordered"></div><div id="tab-plain"><div id="tab-content-nested"></div></div></div>`
	if got := v1Sections(sample); !got["reordered"] || !got["plain"] || got["content-nested"] {
		t.Errorf("CONTROL: V1 section scan of %q gave %v", sample, sortedKeys(got))
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
		if d.IsDir() {
			if path != v2SourceDir && (configtest.IsNonSourceDir(d.Name()) || configtest.IsNestedWorktreeRoot(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".tsx") {
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
