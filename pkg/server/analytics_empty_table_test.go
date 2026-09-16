package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Portal V1's analytics panels each carry a state headline -- a paragraph that says which of
// several empty-looking states the panel is actually in. "No geo-IP database is configured, so
// geographic distribution is off" is not the same statement as "no country has enough users to
// show", and neither is "there are no rows".
//
// The headline is the only thing that can tell those apart, and renderTable() actively
// contradicts it: an empty table renders a search box, the column headers and the words "No
// results found." -- we looked, and there was nobody. For the geo panel nothing had been looked
// at at all (#1920). V2 renders the message INSTEAD of the table in every one of these panels
// (ui/src/pages/AdminAnalytics.tsx), so V1 doing otherwise is a parity defect under #1866, not a
// style difference.
//
// This asserts the property rather than the three panels that had it wrong, because the defect
// arrives with the next panel somebody adds: a new `<prefix>-headline` beside a
// `<prefix>-table-body` is picked up here automatically and has to hide its table the same way.
//
// The wrapper element is load-bearing and is checked for too. renderTable() injects the search
// input and the pagination row as *siblings* of `.table-container`, so hiding the container
// alone leaves a search control hovering over a table that is not there.
func TestStateHeadlinePanelsHideTheirEmptyTable(t *testing.T) {
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("read dashboard.html: %v", err)
	}
	js, err := os.ReadFile(filepath.Join("static", "dashboard.js"))
	if err != nil {
		t.Fatalf("read dashboard.js: %v", err)
	}
	source := string(js)
	markup := string(html)

	panels, ids := stateHeadlinePanels(t, markup)

	for _, prefix := range panels {
		body := prefix + "-table-body"
		wrap := prefix + "-table-wrap"

		if !ids[wrap] {
			t.Errorf("#%s has a state headline but no #%s wrapper in dashboard.html: without one, hiding the table leaves its search box and pagination behind (#1920)", body, wrap)
			continue
		}

		// The wrapper ships hidden, so a failed fetch -- which reaches no branch of the
		// loader at all -- shows no table either. An empty table is not a report of no users.
		wrapTag := regexp.MustCompile(`<div id="` + regexp.QuoteMeta(wrap) + `"[^>]*>`).FindString(markup)
		if wrapTag == "" {
			t.Errorf("#%s is not a <div> in dashboard.html; this check cannot see how it ships", wrap)
		} else if !strings.Contains(strings.ReplaceAll(wrapTag, " ", ""), "display:none") {
			t.Errorf("#%s does not ship with display:none (%s): the table would be visible until the loader answered, and stay visible if it never did (#1920)", wrap, wrapTag)
		}

		// The rendering call must be the one that hides on zero rows. `renderTable(` and
		// `renderTableOrHide(` are distinct strings -- the second does not contain the
		// first, because of the `(` -- so this matches only a direct call, which is the
		// pre-#1920 defect exactly.
		bare := regexp.MustCompile(`renderTable\(\s*'` + regexp.QuoteMeta(body) + `'`)
		if loc := bare.FindStringIndex(source); loc != nil {
			t.Errorf("dashboard.js calls renderTable('%s', ...) directly at byte %d; a state-headline panel must use renderTableOrHide('%s', '%s', ...) so an empty result renders no table, no column headers, no search box and no \"No results found.\" (#1920)", body, loc[0], body, wrap)
		}

		// Whitespace-insensitive: prettier decides how this call site wraps, and a
		// reformat must not read as a regression.
		wired := regexp.MustCompile(`renderTableOrHide\(\s*'` + regexp.QuoteMeta(body) + `'\s*,\s*'` + regexp.QuoteMeta(wrap) + `'`)
		if !wired.MatchString(source) {
			t.Errorf("dashboard.js does not render #%s through renderTableOrHide('%s', '%s', ...) (#1920)", body, body, wrap)
		}
	}
}

// stateHeadlinePanels derives the analytics panels that carry a state headline, and returns them
// alongside every id in the markup. Shared by the two guards in this file so they can never
// disagree about which panels are in scope.
//
// The panels are derived, never listed: every table body whose panel also has a state headline,
// matched by the shared id prefix the markup already uses. An empty or shrunken derivation would
// make every assertion built on it vacuously true and report a clean pass over nothing -- the
// failure mode this file exists to catch -- so the three known panels are asserted to survive it.
func stateHeadlinePanels(t *testing.T, markup string) ([]string, map[string]bool) {
	t.Helper()

	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(markup, -1) {
		ids[m[1]] = true
	}

	var panels []string
	for id := range ids {
		prefix, ok := strings.CutSuffix(id, "-headline")
		if !ok {
			continue
		}
		if ids[prefix+"-table-body"] {
			panels = append(panels, prefix)
		}
	}
	sort.Strings(panels)

	for _, want := range []string{"geo-distribution", "node-placement", "region-latency"} {
		found := false
		for _, got := range panels {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("panel derivation is broken: %q has a headline and a table body in dashboard.html but was not derived; got %v", want, panels)
		}
	}

	return panels, ids
}

// stripElement removes the single element carrying `id`, opening tag through closing tag, from
// `fragment`. Used to except one deliberately-uncontained paragraph from the containment rule
// above without excepting the panel it lives in.
//
// Deliberately narrow: it matches one non-nesting element by id and removes nothing else, so an
// exemption cannot quietly grow to cover a sibling added next to it.
func stripElement(fragment, id string) string {
	open := regexp.MustCompile(`<([a-zA-Z]+)[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>`)
	loc := open.FindStringSubmatchIndex(fragment)
	if loc == nil {
		return fragment
	}
	tag := fragment[loc[2]:loc[3]]
	closing := "</" + tag + ">"
	end := strings.Index(fragment[loc[1]:], closing)
	if end < 0 {
		return fragment
	}
	return fragment[:loc[0]] + fragment[loc[1]+end+len(closing):]
}

// matchingDivEnd returns the offset of the `</div>` that closes the `<div>` opening at `start`.
//
// HTML comments MUST already be stripped from `markup`: this file's markup is heavily commented,
// and that prose talks about `<div>`s, which would throw the depth count off.
func matchingDivEnd(markup string, start int) (int, bool) {
	depth := 0
	for _, loc := range regexp.MustCompile(`<div\b|</div>`).FindAllStringIndex(markup[start:], -1) {
		at := start + loc[0]
		if strings.HasPrefix(markup[at:], "</div>") {
			depth--
			if depth == 0 {
				return at, true
			}
			continue
		}
		depth++
	}
	return 0, false
}

// The paragraphs beneath a panel's table belong to the table, and have to disappear with it
// (#1931).
//
// V1 rendered the Node Placement caveat -- "Not assessable is not a failure: the client caches
// its region choice for 24h ..." -- unconditionally. It is a footnote to the "Not assessable"
// column, so in the empty state it explained a column that was not on screen, and after #1920
// hid the table it annotated nothing at all. V2 renders every paragraph of that panel inside its
// non-empty branch (ui/src/pages/AdminAnalytics.tsx), so per #1866 V1 differing is the defect.
//
// The fix is containment rather than a second toggle: the paragraphs live INSIDE
// `#<prefix>-table-wrap`, so whatever hides the table hides them and no later edit can move one
// without the other. That is what this asserts -- nothing renders after the wrapper closes --
// and it asserts it for every state-headline panel, not only the one that was reported.
//
// Containment costs nothing in reachability for the two data-driven paragraphs that moved in
// with it: GetNodePlacement (pkg/db/node_placement.go) creates a node row for every session and
// only ever names an unknown node it has already counted, so "no rows" and "no sessions" are the
// same state -- which is exactly the condition V2 branches on.
func TestStateHeadlinePanelAnnotationsLiveWithTheirTable(t *testing.T) {
	raw, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("read dashboard.html: %v", err)
	}
	markup := regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(string(raw), "")

	panels, _ := stateHeadlinePanels(t, markup)

	for _, prefix := range panels {
		wrap := prefix + "-table-wrap"

		wrapStart := strings.Index(markup, `<div id="`+wrap+`"`)
		if wrapStart < 0 {
			// Absence is TestStateHeadlinePanelsHideTheirEmptyTable's finding, not this
			// one; reporting it twice would make one fix look like two.
			continue
		}
		wrapEnd, ok := matchingDivEnd(markup, wrapStart)
		if !ok {
			t.Errorf("#%s never closes in dashboard.html; its contents cannot be bounded", wrap)
			continue
		}

		headline := strings.Index(markup, `id="`+prefix+`-headline"`)
		if headline < 0 {
			t.Errorf("#%s-headline was derived but cannot be found in dashboard.html", prefix)
			continue
		}
		panelStart := strings.LastIndex(markup[:headline], "<div")
		if panelStart < 0 {
			t.Errorf("the %s headline is not inside a <div>; this check cannot see where its panel ends", prefix)
			continue
		}
		panelEnd, ok := matchingDivEnd(markup, panelStart)
		if !ok || panelEnd < wrapEnd {
			t.Errorf("the %s panel does not enclose #%s; this check cannot bound it", prefix, wrap)
			continue
		}

		trailing := strings.TrimSpace(markup[wrapEnd+len("</div>") : panelEnd])
		// One bounded exception, and it is not an annotation of the table (#1921). The geo
		// panel's attribution is the credit every geo-IP vendor's licence requires wherever
		// their data is displayed OR USED -- DB-IP's CC BY 4.0 wording is exactly that pair
		// -- so it has to survive the below-threshold state, where rows exist and are
		// suppressed. Containing it in the wrapper would hide the credit in precisely the
		// state where data was used and nothing was shown.
		//
		// It is exempt from containment, not from state: it is driven by `available`
		// instead, and that is asserted below rather than assumed, so this cannot become a
		// hole a later unconditional paragraph slips through.
		trailing = strings.TrimSpace(stripElement(trailing, prefix+"-attribution"))
		if trailing != "" {
			if len(trailing) > 160 {
				trailing = trailing[:160] + "..."
			}
			t.Errorf("the %s panel renders this after #%s closes, so it survives into the empty state where the table and its columns are gone (#1931):\n\t%s\nMove it inside #%s, so the one toggle that hides the table hides it too.", prefix, wrap, trailing, wrap)
		}
	}

	// The geo attribution's exemption above is only sound if something else switches it off
	// when no database is open. Nothing on the server renders it -- dashboard.js does -- so
	// this is where that half is held (#1921).
	js, err := os.ReadFile(filepath.Join("static", "dashboard.js"))
	if err != nil {
		t.Fatalf("read dashboard.js: %v", err)
	}
	source := string(js)
	if !strings.Contains(source, `getElementById('geo-distribution-attribution')`) {
		t.Errorf("dashboard.js never reaches #geo-distribution-attribution, so the paragraph exempted " +
			"from table containment above is rendered by nothing and cleared by nothing (#1921)")
	}
	// The clearing branch. Without it the credit line would persist from a previous render
	// into the "no database configured" state -- a licence acknowledgment under a panel that
	// is switched off, naming a vendor whose data is not in use.
	if !regexp.MustCompile(`if\s*\(!available\)\s*\{\s*el\.textContent\s*=\s*''`).MatchString(source) {
		t.Errorf("dashboard.js does not clear the geo attribution when the feature is unavailable; " +
			"the credit would survive into the switched-off state (#1921)")
	}
	// Parity (#1866): V2 must key the same paragraph off the same condition. A credit
	// rendered in one arm of a live A/B test and not the other is the defect, not a style
	// difference -- and V2's own panel already branches on bucket count for everything else,
	// which is exactly the mistake this guards.
	v2, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "pages", "AdminAnalytics.tsx"))
	if err != nil {
		t.Fatalf("read AdminAnalytics.tsx: %v", err)
	}
	if !strings.Contains(string(v2), "locations?.available && (") {
		t.Errorf("Portal V2 does not render the geo attribution on `locations?.available`, so the " +
			"two arms disagree about when a required credit line appears (#1866, #1921)")
	}

	// The caveat must still RENDER -- just not early. Dropping it would be the worse defect:
	// it exists so the counts cannot be read as a score (placementCaveats,
	// pkg/db/node_placement.go), and V2 renders it beneath the table it annotates.
	caveat := strings.Index(markup, `data-i18n="node_placement_caveat_unverifiable"`)
	npStart := strings.Index(markup, `<div id="node-placement-table-wrap"`)
	npEnd, ok := matchingDivEnd(markup, npStart)
	switch {
	case caveat < 0:
		t.Errorf(`dashboard.html no longer renders data-i18n="node_placement_caveat_unverifiable"; V2 renders it (ui/src/pages/AdminAnalytics.tsx) and it is what stops the counts being read as a score (#1931)`)
	case npStart < 0 || !ok:
		t.Errorf("#node-placement-table-wrap is missing or never closes, so the caveat's position cannot be checked")
	case caveat < npStart || caveat > npEnd:
		t.Errorf("the node placement caveat is outside #node-placement-table-wrap, so it renders in the empty state where the 'Not assessable' column it explains is not on screen (#1931)")
	}
}
