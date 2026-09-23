package client

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// listeningLine finds what the served page claims the Inspector is listening on.
//
// It captures the port rather than testing for the presence of a string, because the two failures
// this has to tell apart both "fail to find localhost:56231": the page naming the WRONG port, and
// the page naming no port at all. Only the captured value distinguishes them, and only the first
// is the defect in #2190.
var listeningLine = regexp.MustCompile(`Listening on localhost:([0-9]+|\{0\}|__LFT_INSPECTOR_PORT__)`)

// TestInspectorPageNamesThePortItBound is the reported defect (#2190). The Inspector falls forward
// when its port is taken -- StartInspector increments and retries -- and it logs the port it ended
// up on, but the page had "localhost:4040" baked into all six translations of client_listening and
// substituted nothing. Two clients running, one Inspector on 4040 and one on 4041, both pages
// claiming 4040.
//
// Driven through StartInspector over a real socket and asserted on the response body, not on the
// embedded template: the substitution happens as the page is served, so a test that read
// dashboard.html from disk would be asserting about the input to the fix rather than its output.
//
// The port asserted is the one StartInspector RETURNED, not the one requested. That is the whole
// point of the bug -- if this test pinned the requested port it would go green on a build where
// the page ignored the fall-forward entirely.
func TestInspectorPageNamesThePortItBound(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	const requested = 56231
	port := startInspectorForTest(t, engine, requested)

	_, body := getInspector(t, port, "/")

	m := listeningLine.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("the Inspector page does not say what it is listening on at all -- no %q match in "+
			"%d bytes of served HTML. Something other than #2190 is wrong; this test cannot report "+
			"on the port until the line is back.", listeningLine, len(body))
	}
	if m[1] != strconv.Itoa(port) {
		t.Fatalf("the Inspector bound port %d (StartInspector returned it, and inspector.go logs it) "+
			"but the page it serves says \"Listening on localhost:%s\". The page is what someone "+
			"diagnosing a client reads, so it has to name the same port as the log (#2190).",
			port, m[1])
	}

	// The defect's literal, asserted as absent: a build that stamps the real port into the fallback
	// but leaves 4040 in a translation bundle would satisfy the check above and still show 4040 to
	// anyone whose browser is not in English.
	if strings.Contains(string(body), "localhost:4040") && port != 4040 {
		t.Errorf("the page bound port %d but still carries the literal \"localhost:4040\" somewhere "+
			"-- a locale bundle or a second copy of the header was missed (#2190)", port)
	}
}

// TestInspectorBundleStatesNoHostPortAsFact is the durable half: it fails on ANY member of the
// class #2190 was one instance of -- a translated string in the Inspector's own bundle that states
// a host:port as a literal fact.
//
// A string like that can only ever be right by coincidence. Every host:port this client deals with
// is chosen at run time: the Inspector's own port falls forward on a clash, the destination port
// and target host are settings. There is no correct literal to write, which is why the rule is
// "none", not "these six are wrong".
//
// Deliberately NOT a check for the number 4040: that is the instance. It matches the shape --
// something host-like, a colon, a port-like number -- which is the mistake rule 2 of §5b asks for.
// Illustrative values in help text ("e.g. 8080 for Liferay", "10.0.0.0/24") do not match, and
// should not: they are examples of what a user might type, not claims about what this client did.
//
// Re-run the same enumeration by hand with:
//
//	grep -nE '"[a-z0-9_]+": "[^"]*[A-Za-z0-9.-]+:[0-9]{2,5}' pkg/client/dashboard.html
func TestInspectorBundleStatesNoHostPortAsFact(t *testing.T) {
	page, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("reading dashboard.html: %v", err)
	}

	// A bundle entry: `"some_key": "some value",` inside clientTranslations.
	entry := regexp.MustCompile(`"([a-z0-9_]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	hostPort := regexp.MustCompile(`[A-Za-z0-9.\-]+:[0-9]{2,5}`)

	matches := entry.FindAllStringSubmatch(string(page), -1)
	if len(matches) == 0 {
		t.Fatal("no translation entries found in dashboard.html -- this check would be passing " +
			"over nothing, which is the failure mode it exists to avoid")
	}

	var offenders []string
	for _, m := range matches {
		if found := hostPort.FindString(m[2]); found != "" {
			offenders = append(offenders, fmt.Sprintf("%s = %q (states %q)", m[1], m[2], found))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("%d translated string(s) in the Inspector bundle state a host:port as a literal:\n  %s\n\n"+
			"Every host:port this client uses is decided at run time, so a literal here is wrong as "+
			"soon as anything moves -- #2190 was exactly this, \"Listening on localhost:4040\" shown "+
			"by an Inspector on 4041. Use a {0} token and give the element a data-i18n-arg0 the "+
			"server stamps the real value into.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestRenderDashboardHTMLLeavesNoToken guards the substitution itself. The token is invisible in a
// screenshot review -- an unsubstituted __LFT_INSPECTOR_PORT__ renders as text and looks like a
// typo rather than a broken mechanism -- so assert the served page has none left.
func TestRenderDashboardHTMLLeavesNoToken(t *testing.T) {
	if !strings.Contains(string(DashboardHTML), dashboardPortToken) {
		t.Fatalf("the embedded dashboard contains no %s, so RenderDashboardHTML has nothing to "+
			"substitute and every assertion about the port below is vacuous", dashboardPortToken)
	}
	const port = "4041"
	rendered := string(RenderDashboardHTML(4041))
	if strings.Contains(rendered, dashboardPortToken) {
		t.Errorf("RenderDashboardHTML left an unsubstituted %s in the page", dashboardPortToken)
	}
	// Counted as a delta against the template so an unrelated "4041" appearing in the page one day
	// cannot quietly turn this into an off-by-one about somebody else's string.
	tokens := strings.Count(string(DashboardHTML), dashboardPortToken)
	added := strings.Count(rendered, port) - strings.Count(string(DashboardHTML), port)
	if added != tokens {
		t.Errorf("RenderDashboardHTML substituted %d occurrence(s) of the port, want %d -- every "+
			"token has to be replaced, not the first", added, tokens)
	}
}
