package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Portal V1's sections all ship hidden and are revealed one at a time by showTab(), which
// also kicks off that section's data load. Several cards inside a section additionally ship
// `display: none` and are revealed only by the loader that fills them -- so a loader wired
// to the wrong branch does not merely fetch late, it makes its markup *unreachable* on the
// route the user actually takes. The symptom is invisible from either file on its own: the
// markup is present and correct, the loader is present and correct, and only the pairing is
// wrong.
//
// #1785 reported one instance (the Server Configuration card, loaded from the Maintenance
// branch). Enumerating the shape rather than the symbol found three:
//
//	loadServerConfig           maintenance  -> #card-server-config, #config-tree-container  (#tab-system)
//	loadMaintenanceStatus      maintenance  -> #test-integration-target[-container]         (#tab-system)
//	loadDomains                reservations -> #acc-preferred-domain                        (#tab-account)
//
// So this asserts the property instead of those three: every function that runs *only*
// because some tab was shown may only touch element ids belonging to a tab it is reached
// from. A function with any other caller -- one that also runs at sign-in, say -- is out of
// scope, because it is not tab-conditional and its reachability is not showTab's to get
// wrong.
func TestShowTabLoadersStayInTheirTab(t *testing.T) {
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("read dashboard.html: %v", err)
	}
	js, err := os.ReadFile(filepath.Join("static", "dashboard.js"))
	if err != nil {
		t.Fatalf("read dashboard.js: %v", err)
	}

	// Comments are blanked before anything is parsed. Without that the checker reads a
	// function name mentioned in prose as a call, so deleting a call and leaving the comment
	// that describes it keeps the gate green -- which is precisely the state a regression
	// would arrive in. Measured: with comments left in, reverting the account branch's
	// loadDomains() call and keeping the sentence above it hid the violation entirely.
	source := blankJSComments(string(js))

	idTab := elementTabOwners(string(html))
	// The derivation has to be checked, not assumed: an id map that came out empty would
	// make every comparison below vacuously true and report a clean pass over nothing.
	if got := idTab["card-server-config"]; got != "system" {
		t.Fatalf("id->tab derivation is wrong: #card-server-config maps to %q, want \"system\"", got)
	}
	if len(idTab) < 100 {
		t.Fatalf("id->tab derivation found only %d ids; the HTML scan is broken", len(idTab))
	}

	fns := jsFunctionBodies(source)
	if _, ok := fns["showTab"]; !ok {
		t.Fatalf("showTab() not found in dashboard.js; the JS scan is broken")
	}
	if len(fns) < 150 {
		t.Fatalf("only %d top-level functions found in dashboard.js; the JS scan is broken", len(fns))
	}

	callers := jsCallers(source, fns)
	branches := showTabBranches(fns)
	if len(branches) < 10 {
		t.Fatalf("only %d showTab branches parsed; the branch scan is broken", len(branches))
	}

	// Every function each branch is solely responsible for running: the branch's own calls,
	// plus any function whose only other callers are already in that set. A helper called
	// from somewhere else as well is not private to the branch and is left out.
	reached := map[string]map[string]bool{}
	for tab, seeds := range branches {
		for fn := range branchClosure(seeds, fns, callers) {
			if reached[fn] == nil {
				reached[fn] = map[string]bool{}
			}
			reached[fn][tab] = true
		}
	}

	idRefs := regexp.MustCompile(`getElementById\(\s*'([^']+)'|querySelector(?:All)?\(\s*'#([A-Za-z0-9_-]+)`)

	var problems []string
	for fn, tabs := range reached {
		// Out of scope if anything outside the tab-conditional world calls it too.
		outside := false
		for c := range callers[fn] {
			if c == fn || c == "showTab" {
				continue
			}
			if _, ok := reached[c]; !ok {
				outside = true
				break
			}
		}
		if outside {
			continue
		}
		for _, m := range idRefs.FindAllStringSubmatch(fns[fn], -1) {
			id := m[1]
			if id == "" {
				id = m[2]
			}
			owner, ok := idTab[id]
			if !ok || owner == "" || tabs[owner] {
				continue
			}
			problems = append(problems, "  "+fn+"() runs only for tab(s) "+strings.Join(sortedKeys(tabs), ",")+
				" but writes #"+id+", which lives in #tab-"+owner)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("showTab() branches load data into other tabs' markup (#1785).\n%s\n\n"+
			"Each of these is only reachable by visiting an unrelated section first. Call the "+
			"loader from the branch whose markup it fills; if one loader serves two tabs, call "+
			"it from both.", strings.Join(problems, "\n"))
	}
}

// elementTabOwners maps every element id in dashboard.html to the name of the #tab-* section
// enclosing it (empty for ids outside any section -- modals, the sidebar, the shell).
func elementTabOwners(html string) map[string]string {
	html = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(html, " ")
	tag := regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b([^>]*)>`)
	idAttr := regexp.MustCompile(`\bid="([^"]+)"`)
	void := map[string]bool{
		"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true,
		"source": true, "track": true, "area": true, "base": true, "col": true,
		"embed": true, "param": true, "wbr": true,
	}

	type open struct{ tag, id string }
	var stack []open
	owner := func() string {
		for _, e := range stack {
			if strings.HasPrefix(e.id, "tab-") {
				return strings.TrimPrefix(e.id, "tab-")
			}
		}
		return ""
	}

	out := map[string]string{}
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
		id := ""
		if a := idAttr.FindStringSubmatch(attrs); a != nil {
			id = a[1]
			out[id] = owner()
		}
		if void[name] || strings.HasSuffix(strings.TrimSpace(attrs), "/") {
			continue
		}
		stack = append(stack, open{name, id})
	}
	return out
}

// jsFunctionBodies returns each top-level `function name(...) { ... }` body, braces included.
func jsFunctionBodies(js string) map[string]string {
	decl := regexp.MustCompile(`(?m)^(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`)
	out := map[string]string{}
	for _, loc := range decl.FindAllStringSubmatchIndex(js, -1) {
		name := js[loc[2]:loc[3]]
		start := strings.Index(js[loc[1]-1:], "{")
		if start < 0 {
			continue
		}
		start += loc[1] - 1
		depth := 0
		for i := start; i < len(js); i++ {
			switch js[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					out[name] = js[start : i+1]
					i = len(js)
				}
			}
		}
	}
	return out
}

// jsCallers maps each known function to the set of functions that call it ("" for a call at
// file scope). Declaration sites are not calls.
func jsCallers(js string, fns map[string]string) map[string]map[string]bool {
	type span struct {
		lo, hi int
		name   string
	}
	var spans []span
	for name, body := range fns {
		lo := strings.Index(js, body)
		spans = append(spans, span{lo, lo + len(body), name})
	}
	enclosing := func(pos int) string {
		for _, s := range spans {
			if pos >= s.lo && pos < s.hi {
				return s.name
			}
		}
		return ""
	}

	callSite := regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*\(`)
	declPrefix := regexp.MustCompile(`function\s+$`)
	out := map[string]map[string]bool{}
	for _, loc := range callSite.FindAllStringSubmatchIndex(js, -1) {
		name := js[loc[2]:loc[3]]
		if _, known := fns[name]; !known {
			continue
		}
		from := loc[2] - 40
		if from < 0 {
			from = 0
		}
		if declPrefix.MatchString(js[from:loc[2]]) {
			continue
		}
		if out[name] == nil {
			out[name] = map[string]bool{}
		}
		out[name][enclosing(loc[2])] = true
	}
	return out
}

// showTabBranches maps each `tabName === 'x'` branch to the functions it calls directly.
func showTabBranches(fns map[string]string) map[string]map[string]bool {
	body := fns["showTab"]
	head := regexp.MustCompile(`tabName === '([a-z-]+)'\)\s*(\{?)`)
	call := regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*\(`)

	out := map[string]map[string]bool{}
	for _, loc := range head.FindAllStringSubmatchIndex(body, -1) {
		tab := body[loc[2]:loc[3]]
		rest := body[loc[1]:]
		end := strings.Index(rest, "\n")
		if body[loc[4]:loc[5]] == "{" {
			end = strings.Index(rest, "\n  }")
		}
		if end < 0 {
			continue
		}
		if out[tab] == nil {
			out[tab] = map[string]bool{}
		}
		for _, m := range call.FindAllStringSubmatch(rest[:end], -1) {
			if _, known := fns[m[1]]; known {
				out[tab][m[1]] = true
			}
		}
	}
	return out
}

// branchClosure grows a branch's seed calls by any function whose every other caller is
// already inside the set -- i.e. helpers private to that branch's loaders.
func branchClosure(seeds map[string]bool, fns map[string]string, callers map[string]map[string]bool) map[string]bool {
	set := map[string]bool{}
	for s := range seeds {
		set[s] = true
	}
	call := regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*\(`)
	for changed := true; changed; {
		changed = false
		for fn := range set {
			for _, m := range call.FindAllStringSubmatch(fns[fn], -1) {
				c := m[1]
				if _, known := fns[c]; !known || set[c] {
					continue
				}
				private := true
				for caller := range callers[c] {
					if caller != c && caller != "showTab" && !set[caller] {
						private = false
						break
					}
				}
				if private {
					set[c] = true
					changed = true
				}
			}
		}
	}
	return set
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// blankJSComments replaces the contents of every // and /* */ comment with spaces, leaving
// offsets, string literals and regex literals untouched. String and regex state has to be
// tracked or a "//" inside a URL literal would swallow the rest of its line: dashboard.js
// has 11 of those and 12 regex literals, one of which is /\/+$/.
func blankJSComments(js string) string {
	out := []byte(js)
	const (
		code = iota
		line
		block
		sq
		dq
		tmpl
		rex
	)
	state := code
	prevSig := byte(0) // last significant code character, for the regex-vs-division call
	for i := 0; i < len(js); i++ {
		c := js[i]
		switch state {
		case code:
			switch {
			case c == '/' && i+1 < len(js) && js[i+1] == '/':
				state, out[i], out[i+1] = line, ' ', ' '
				i++
			case c == '/' && i+1 < len(js) && js[i+1] == '*':
				state, out[i], out[i+1] = block, ' ', ' '
				i++
			case c == '\'':
				state = sq
			case c == '"':
				state = dq
			case c == '`':
				state = tmpl
			case c == '/' && regexCanStartAfter(prevSig):
				state = rex
			}
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prevSig = c
			}
		case line:
			if c == '\n' {
				state = code
			} else {
				out[i] = ' '
			}
		case block:
			if c == '*' && i+1 < len(js) && js[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				state = code
			} else if c != '\n' {
				out[i] = ' '
			}
		case sq, dq, tmpl, rex:
			if c == '\\' {
				i++
				continue
			}
			if (state == sq && c == '\'') || (state == dq && c == '"') ||
				(state == tmpl && c == '`') || (state == rex && c == '/') {
				state = code
				prevSig = c
			}
		}
	}
	return string(out)
}

// A '/' opens a regex literal unless the previous significant character could end an
// expression, in which case it is division.
func regexCanStartAfter(prev byte) bool {
	switch {
	case prev == 0:
		return true
	case prev >= 'a' && prev <= 'z', prev >= 'A' && prev <= 'Z', prev >= '0' && prev <= '9':
		return false
	case prev == ')' || prev == ']' || prev == '_' || prev == '$':
		return false
	}
	return true
}
