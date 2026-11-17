package server

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Findings from the adversarial review of #2465, one test each.

func anonymisedCSV(t *testing.T, bases []string, rows, log []*db.AuditEntry) string {
	t.Helper()
	srv := &Server{cfg: &config.ServerConfig{Domains: bases}}
	var b strings.Builder
	if err := srv.writeAuditCSV(&b, rows, log, true); err != nil {
		t.Fatalf("writeAuditCSV: %v", err)
	}
	return b.String()
}

// Emails were found by matching a token against a strict pattern, so anything outside it leaked:
// part of the address when it held an apostrophe or a non-ASCII letter, and all of it when it had
// no TLD, an IDN or an underscore in the domain.
func TestAnonymiseCatchesEveryEmailShape(t *testing.T) {
	rows := []*db.AuditEntry{{ID: 1, Details: strings.Join([]string{
		"Refused jane.o'neil@liferay.com.",
		"Refused jürgen.müller@liferay.com",
		"admin@localhost rejected",
		"jane@my_host.com and jane@exämple.com",
		"ssh ubuntu@10.1.2.3 failed",
		`{"to":"\u003cjohn.doe@corp.test\u003e"}`,
		"(mailto:max@corp.test)",
	}, " | ")}}
	out := anonymisedCSV(t, []string{"tunnels.test"}, rows, nil)

	for _, original := range []string{"jane.o", "o'neil", "jürgen", "müller", "admin@localhost", "my_host",
		"exämple", "ubuntu", "10.1.2.3", "john.doe", "max@corp"} {
		if strings.Contains(out, original) {
			t.Errorf("%q survived anonymisation:\n%s", original, out)
		}
	}
	// The JSON escape around the address is kept intact, not turned into "\r" + an address.
	if !regexp.MustCompile(`\\u003c[a-z]{8}@example\.com\\u003e`).MatchString(out) {
		t.Errorf("JSON-escaped address not replaced cleanly:\n%s", out)
	}
	if !strings.Contains(out, "(mailto:") || !strings.Contains(out, "rejected") {
		t.Errorf("text around the addresses was damaged:\n%s", out)
	}
}

func TestAnonymiseCatchesZonedAndJoinedIPs(t *testing.T) {
	rows := []*db.AuditEntry{{ID: 1,
		IPAddress: "fe80::1ff:fe23:4567:890a%eth0",
		Details:   "link fe80::1ff:fe23:4567:890a%eth0 up; client_10.7.7.7 seen"}}
	out := anonymisedCSV(t, []string{"tunnels.test"}, rows, nil)
	for _, original := range []string{"fe80::1ff", "10.7.7.7"} {
		if strings.Contains(out, original) {
			t.Errorf("%q survived anonymisation:\n%s", original, out)
		}
	}
	if strings.Count(out, "%eth0") != 2 {
		t.Errorf("the zone should be kept on both addresses:\n%s", out)
	}
}

// A vanity or custom domain recorded as a subdomain target taught "com" as a tunnel base, after
// which every *.com host in the export was rewritten.
func TestAnonymiseLearnsBasesOnlyUnderConfiguredDomains(t *testing.T) {
	rows := []*db.AuditEntry{
		{ID: 1, TargetType: resourceKindSubdomain, TargetID: "solaramoto.com"},
		{ID: 2, TargetType: resourceKindSubdomain, TargetID: "shop-x.eu.tunnels.test",
			Details: "relay smtp.office365.com; host shop-x.eu.tunnels.test"},
	}
	out := anonymisedCSV(t, []string{"tunnels.test"}, rows, nil)
	if !strings.Contains(out, "smtp.office365.com") {
		t.Errorf("an unrelated .com host was rewritten:\n%s", out)
	}
	// The regional base under the configured domain is still learned and kept.
	if strings.Contains(out, "shop-x") || !regexp.MustCompile(`sub-[a-z0-9]{6}\.eu\.tunnels\.test`).MatchString(out) {
		t.Errorf("a host under a regional base was not anonymised:\n%s", out)
	}
}

// Labels were learned only from the exported rows, so a selection could export a row naming a
// tunnel whose structured row was not selected, and the name survived.
func TestAnonymiseLearnsLabelsFromOutsideTheSelection(t *testing.T) {
	mention := &db.AuditEntry{ID: 2, Details: "tunnel peter-se-demo kicked"}
	structured := &db.AuditEntry{ID: 1, TargetType: resourceKindSubdomain, TargetID: "peter-se-demo"}
	out := anonymisedCSV(t, []string{"tunnels.test"}, []*db.AuditEntry{mention},
		[]*db.AuditEntry{mention, structured})
	if strings.Contains(out, "peter-se-demo") {
		t.Errorf("a label structured outside the selection survived:\n%s", out)
	}
}

// "No replacement equals any original" had no test: a forced collision proves it.
func TestAnonymiseNeverReusesAnOriginalAsAReplacement(t *testing.T) {
	// rand.Int(r, 26) reads one byte per letter here, so eight zero bytes mint "aaaaaaaa".
	rnd := io.MultiReader(bytes.NewReader(make([]byte, 8)), rand.Reader)
	a := newAuditAnonymiser(rnd, nil)
	rows := []*db.AuditEntry{{ActorID: "bob@corp.test"}, {ActorID: "aaaaaaaa@example.com"}}
	a.prepare(rows)
	got, err := a.rewrite(rows[0].ActorID)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got == "aaaaaaaa@example.com" {
		t.Errorf("bob@corp.test was replaced with another original address, %q", got)
	}
}

// Values a stranger typed must not run as a formula in the admin's spreadsheet.
func TestAuditCSVCellsCannotRunAsFormulas(t *testing.T) {
	rows := []*db.AuditEntry{{ID: 1, ActorID: `=HYPERLINK("http://x","y")`, TargetID: "+1", Details: "-2", IPAddress: "@x", Action: "plain"}}
	var b strings.Builder
	if err := (&Server{}).writeAuditCSV(&b, rows, nil, false); err != nil {
		t.Fatalf("writeAuditCSV: %v", err)
	}
	rec, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	row := rec[1]
	for col, want := range map[int]string{1: `'=HYPERLINK("http://x","y")`, 4: "'+1", 5: "'-2", 6: "'@x", 2: "plain"} {
		if row[col] != want {
			t.Errorf("column %d = %q, want %q", col, row[col], want)
		}
	}
}

// The export re-read the live log, so an entry written after the admin's view loaded shifted
// every position and "row 1" exported a row they had never seen. as_of pins it.
func TestAuditExportPinsTheViewWithAsOf(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	var asOf int64
	for i := 0; i < 3; i++ {
		e := &db.AuditEntry{ActorID: "a@corp.test", Action: "asof.test", Details: "asof-2464 #" + strconv.Itoa(i)}
		if err := srv.db.WriteAuditEntry(e); err != nil {
			t.Fatalf("seed: %v", err)
		}
		asOf = e.ID
	}
	// Written after the admin's view loaded.
	if err := srv.db.WriteAuditEntry(&db.AuditEntry{ActorID: "a@corp.test", Action: "asof.test", Details: "asof-2464 late"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	q := url.Values{"q": {"asof-2464"}, "rows": {"1"}, "as_of": {strconv.FormatInt(asOf, 10)}}
	w := httptest.NewRecorder()
	srv.handleAdminAuditExportPreview(w, httptest.NewRequest(http.MethodGet, "/x?"+q.Encode(), nil), "admin")
	var preview struct{ Selected, Total int }
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatalf("preview: %v (%s)", err, w.Body.String())
	}
	if preview.Total != 3 {
		t.Errorf("preview total %d, want the 3 rows the view had", preview.Total)
	}

	w = httptest.NewRecorder()
	srv.handleAdminAuditExport(w, httptest.NewRequest(http.MethodGet, "/x?"+q.Encode(), nil), "admin")
	rec, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(rec) != 2 || rec[1][5] != "asof-2464 #2" {
		t.Errorf("row 1 of the pinned view should be the newest entry the view had, got %v", rec[1:])
	}

	w = httptest.NewRecorder()
	srv.handleAdminAuditExport(w, httptest.NewRequest(http.MethodGet, "/x?as_of=yesterday", nil), "admin")
	if w.Code != http.StatusBadRequest {
		t.Errorf("a malformed as_of answered %d", w.Code)
	}
}
