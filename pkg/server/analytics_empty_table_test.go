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

	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(markup, -1) {
		ids[m[1]] = true
	}

	// The panels in scope are derived, never listed: every table body whose panel also has a
	// state headline, matched by the shared id prefix the markup already uses.
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

	// An empty or shrunken derivation would make every assertion below vacuously true and
	// report a clean pass over nothing -- the failure mode this file exists to catch.
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
