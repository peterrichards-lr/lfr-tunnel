package server

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestParseRowSelection(t *testing.T) {
	valid := []struct {
		spec  string
		total int
		want  []int // 0-based
	}{
		{"", 10, nil},
		{"   ", 10, nil},
		{"4-9", 20, []int{3, 4, 5, 6, 7, 8}},
		{"1,4,18", 20, []int{0, 3, 17}},
		{"4-9,12,18", 20, []int{3, 4, 5, 6, 7, 8, 11, 17}},
		{"4-9,6", 20, []int{3, 4, 5, 6, 7, 8}}, // overlap collapses
		{"3,3,3", 20, []int{2}},                // duplicates collapse
		{"18,1,4", 20, []int{0, 3, 17}},        // view order, not typed order
		{" 2 - 3 , 5 ", 20, []int{1, 2, 4}},    // spaces tolerated
		{"7-7", 7, []int{6}},                   // a one-row range, at the last row
		{"1-3,2-5", 5, []int{0, 1, 2, 3, 4}},   // overlapping ranges
		{"5-5,1-1", 5, []int{0, 4}},            // ...in any order
		{"1-1000000", 1000000, nil /* checked below */},
	}
	for _, tc := range valid {
		got, err := parseRowSelection(tc.spec, tc.total)
		if err != nil {
			t.Errorf("parseRowSelection(%q, %d) refused: %v", tc.spec, tc.total, err)
			continue
		}
		if tc.spec == "1-1000000" {
			if len(got) != 1000000 || got[0] != 0 || got[len(got)-1] != 999999 {
				t.Errorf("a whole-view range gave %d rows", len(got))
			}
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseRowSelection(%q, %d) = %v, want %v", tc.spec, tc.total, got, tc.want)
		}
	}

	// Each refusal asserts the message that names its cause, so a case cannot pass by tripping
	// a different check.
	invalid := []struct {
		spec, because string
	}{
		{"9-4", "reversed"},
		{"0", "start at 1"},
		{"0-3", "start at 1"},
		{"-3", "start at 1"},
		{"21", "beyond the 20 rows"},
		{"4-21", "beyond the 20 rows"},
		{"99999999999999999999", "beyond the 20 rows"},
		{"abc", "not a row number"},
		{"4-x", "not a row number"},
		{"4.5", "not a row number"},
		{"+4", "not a row number"},
		{"1-2-3", "not a row number"},
		{"4-", "not a row number"},
		{"4,,5", "empty entry"},
		{"4,", "empty entry"},
	}
	for _, tc := range invalid {
		_, err := parseRowSelection(tc.spec, 20)
		if err == nil {
			t.Errorf("parseRowSelection(%q) accepted it; want a refusal (%s)", tc.spec, tc.because)
			continue
		}
		if !strings.Contains(err.Error(), tc.because) {
			t.Errorf("parseRowSelection(%q) = %q; want it to say %q", tc.spec, err, tc.because)
		}
	}

	if _, err := parseRowSelection("1", 0); err == nil || !strings.Contains(err.Error(), "beyond the 0 rows") {
		t.Errorf("selecting from an empty view: got %v", err)
	}
}

// auditFixture is a log in the repository's own order: newest first.
func auditFixture() []*db.AuditEntry {
	base := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id int64, actor, action, target, ip string) *db.AuditEntry {
		return &db.AuditEntry{ID: id, ActorID: actor, Action: action, TargetType: "subdomain",
			TargetID: target, IPAddress: ip, CreatedAt: base.Add(-time.Duration(id) * time.Minute)}
	}
	return []*db.AuditEntry{
		mk(1, "zed@corp.test", "reservation.created", "delta", "10.0.0.1"),
		mk(2, "amy@corp.test", "mfa.enabled", "alpha", "10.0.0.2"),
		mk(3, "Bob@corp.test", "reservation.deleted", "charlie", "10.0.0.3"),
		mk(4, "bob@corp.test", "reservation.created", "bravo", "10.0.0.4"),
		mk(5, "amy@corp.test", "reservation.promoted", "echo", "10.0.0.5"),
		mk(6, "cat@corp.test", "user.updated", "foxtrot", "10.0.0.6"),
	}
}

func ids(rows []*db.AuditEntry) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestAuditViewFiltersAndSortsAsThePortalsDo(t *testing.T) {
	entries := auditFixture()

	// Case-insensitive substring over the search fields.
	got := auditView{query: "RESERVATION"}.apply(entries)
	if want := []int64{1, 3, 4, 5}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("search: got %v, want %v", ids(got), want)
	}
	// Whitespace alone is no search.
	if got := (auditView{query: "  "}).apply(entries); len(got) != len(entries) {
		t.Errorf("whitespace query filtered to %d rows", len(got))
	}
	// Sort lowercases, and is stable: "Bob@" and "bob@" tie, so they keep the server's newest-first
	// order (3 before 4) in BOTH directions -- what a stable JavaScript sort does.
	asc := auditView{sortKey: "actor_id"}.apply(entries)
	if want := []int64{2, 5, 3, 4, 6, 1}; !reflect.DeepEqual(ids(asc), want) {
		t.Errorf("sort asc: got %v, want %v", ids(asc), want)
	}
	desc := auditView{sortKey: "actor_id", desc: true}.apply(entries)
	if want := []int64{1, 6, 3, 4, 2, 5}; !reflect.DeepEqual(ids(desc), want) {
		t.Errorf("sort desc: got %v, want %v", ids(desc), want)
	}
	// The view must not reorder the caller's slice.
	if want := []int64{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(ids(entries), want) {
		t.Errorf("apply mutated its input: %v", ids(entries))
	}
}

func TestAuditViewFromQueryRefusesUnknownSort(t *testing.T) {
	for _, q := range []string{"sort=password", "sort=actor_id&dir=sideways"} {
		v, err := url.ParseQuery(q)
		if err != nil {
			t.Fatalf("parsing %q: %v", q, err)
		}
		if _, err := auditViewFromQuery(v); err == nil {
			t.Errorf("%s was accepted", q)
		}
	}
}

// The filter, then the sort, then the selection -- by position in that view, returned in view
// order. Driven through the real router and database, so it also proves both routes are reached.
func TestAuditExportAppliesFilterSortAndSelection(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	token := newAdminSession(t, srv, "export-admin@example.com")

	for _, e := range auditFixture() {
		e.ID = 0
		e.Details = "fixture-2464"
		if err := srv.db.WriteAuditEntry(e); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	// View: fixture rows matching "reservation", sorted by target ascending:
	//   1 bravo, 2 charlie, 3 delta, 4 echo
	// Selection "4,1-2,2" -> rows 1, 2, 4 in view order: bravo, charlie, echo.
	q := url.Values{"q": {"reservation"}, "sort": {"target_id"}, "dir": {"asc"}, "rows": {"4,1-2,2"}}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, adminRequest(http.MethodGet, "/api/admin/audit/export/preview?"+q.Encode(), nil, token))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var preview struct{ Selected, Total int }
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatalf("preview body: %v", err)
	}
	if preview.Selected != 3 || preview.Total != 4 {
		t.Errorf("preview = %+v, want 3 of 4", preview)
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, adminRequest(http.MethodGet, "/api/admin/audit/export?"+q.Encode(), nil, token))
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	var targets []string
	for _, r := range records[1:] {
		targets = append(targets, r[4])
	}
	if want := []string{"bravo", "charlie", "echo"}; !reflect.DeepEqual(targets, want) {
		t.Errorf("exported targets %v, want %v", targets, want)
	}

	// A selection beyond the view is refused with the reason, by both endpoints.
	bad := url.Values{"q": {"reservation"}, "rows": {"5"}}
	for _, path := range []string{"/api/admin/audit/export/preview", "/api/admin/audit/export"} {
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, adminRequest(http.MethodGet, path+"?"+bad.Encode(), nil, token))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "beyond the 4 rows") {
			t.Errorf("%s with rows=5: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// #2464: the export used to return at most 50 rows -- an empty filter fell into the repository's
// default limit -- while the portals said "Use Export for the full history".
func TestAuditExportIsNotCappedAtOnePage(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	const n = 230 // past both the old 50 and one 200-row repository page
	for i := 0; i < n; i++ {
		if err := srv.db.WriteAuditEntry(&db.AuditEntry{ActorID: "a@corp.test", Action: "bulk.test",
			Details: "cap-2464-" + strconv.Itoa(i)}); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	w := httptest.NewRecorder()
	srv.handleAdminAuditExport(w, httptest.NewRequest(http.MethodGet, "/api/admin/audit/export?q=cap-2464-", nil), "admin")
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if got := len(records) - 1; got != n {
		t.Errorf("exported %d rows, want all %d", got, n)
	}
}

func TestAuditExportFilename(t *testing.T) {
	day := time.Date(2026, 11, 17, 23, 30, 0, 0, time.FixedZone("x", -5*3600)) // 04:30 UTC on the 18th
	if got := auditExportFilename(day, false); got != "audit-log-2026-11-18.csv" {
		t.Errorf("got %q", got)
	}
	if got := auditExportFilename(day, true); got != "audit-log-2026-11-18-anonymised.csv" {
		t.Errorf("got %q", got)
	}
}

// anonFixture carries every kind of value, in structured columns and in free text, in mixed
// case. The originals deliberately contain hyphens or are longer than any random part of a
// replacement, so "does an original survive" cannot be answered yes by coincidence.
func anonFixture() []*db.AuditEntry {
	return []*db.AuditEntry{
		{ID: 1, ActorID: "Jane.Doe@acme-corp.test", Action: "reservation.created", TargetType: "subdomain",
			TargetID: "shop-alpha.tunnels.test", IPAddress: "203.0.113.77",
			Details: "Reserved shop-alpha for jane.doe@acme-corp.test from 203.0.113.77; see https://SHOP-ALPHA.tunnels.test/web/guest."},
		{ID: 2, ActorID: "jane.doe@acme-corp.test", Action: "reservation.access_control_updated", TargetType: "subdomain",
			TargetID: "shop-alpha.eu.tunnels-edge.test", IPAddress: "198.51.100.23, 10.20.30.40",
			Details: "Access controls updated: IPs=10.20.30.40,2001:0db8:85a3::8a2e:0370:7334 host shop-alpha.tunnels.test:443"},
		{ID: 3, ActorID: "max-power@other-co.test", Action: "user.updated", TargetType: "user",
			TargetID: "max-power@other-co.test", IPAddress: "2a02:c7f:1234::99",
			Details: "Guest invitation created for Max (mailto:max-power@other-co.test), not shop-alphabet or xshop-alpha"},
		{ID: 4, ActorID: "system", Action: "lease.expired", TargetType: "lease",
			TargetID: "zz-legacy-host", IPAddress: "",
			Details: "Lease zz-legacy-host expired; IPv6 [2a02:c7f:1234::99]:8443"},
	}
}

var anonBases = []string{"tunnels.test"}

func anonymise(t *testing.T, rows []*db.AuditEntry) []*db.AuditEntry {
	t.Helper()
	a := newAuditAnonymiser(rand.Reader, anonBases)
	copies := make([]*db.AuditEntry, len(rows))
	for i, e := range rows {
		c := *e
		copies[i] = &c
	}
	a.prepare(copies)
	for _, e := range copies {
		for _, col := range anonymisedColumns(e) {
			out, err := a.rewrite(*col)
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			*col = out
		}
	}
	return copies
}

var mustCompile = regexp.MustCompile

func TestAnonymiseIsConsistentWithinAnExport(t *testing.T) {
	out := anonymise(t, anonFixture())

	// The same email, in two cases and in three places (actor, actor, free text), is one value.
	email := out[0].ActorID
	if !mustCompile(`^[a-z]{8}@example\.com$`).MatchString(email) {
		t.Fatalf("actor email replaced with %q", email)
	}
	if out[1].ActorID != email {
		t.Errorf("same email, different case: %q vs %q", email, out[1].ActorID)
	}
	if !strings.Contains(out[0].Details, " for "+email+" ") {
		t.Errorf("email in free text not replaced with the same value: %q", out[0].Details)
	}
	// A different email is a different value.
	if out[2].ActorID == email {
		t.Errorf("two emails share a replacement")
	}
	if !strings.Contains(out[2].Details, "mailto:"+out[2].ActorID+")") {
		t.Errorf("email after mailto: not replaced consistently: %q", out[2].Details)
	}

	// The subdomain: bare in free text, inside hostnames under two base domains (one configured,
	// one only learned from a structured target), in a URL in another case, and with a port.
	sub := strings.SplitN(out[0].TargetID, ".", 2)[0]
	if !mustCompile(`^sub-[a-z0-9]{6}$`).MatchString(sub) || !strings.HasSuffix(out[0].TargetID, ".tunnels.test") {
		t.Fatalf("hostname replaced with %q; want sub-xxxxxx.tunnels.test", out[0].TargetID)
	}
	for _, want := range []string{"Reserved " + sub + " for", "https://" + sub + ".tunnels.test/web/guest"} {
		if !strings.Contains(out[0].Details, want) {
			t.Errorf("details lack %q: %q", want, out[0].Details)
		}
	}
	if out[1].TargetID != sub+".eu.tunnels-edge.test" {
		t.Errorf("regional host: got %q, want %q", out[1].TargetID, sub+".eu.tunnels-edge.test")
	}
	if !strings.Contains(out[1].Details, "host "+sub+".tunnels.test:443") {
		t.Errorf("host:port not replaced consistently: %q", out[1].Details)
	}
	// Whole values only: longer words containing the label are untouched.
	if !strings.Contains(out[2].Details, "not shop-alphabet or xshop-alpha") {
		t.Errorf("a substring of a longer word was replaced: %q", out[2].Details)
	}
	// A different subdomain gets a different value.
	other := out[3].TargetID
	if !mustCompile(`^sub-[a-z0-9]{6}$`).MatchString(other) || other == sub {
		t.Errorf("second subdomain: %q (first %q)", other, sub)
	}
	if !strings.Contains(out[3].Details, "Lease "+other+" expired") {
		t.Errorf("bare lease label not replaced consistently: %q", out[3].Details)
	}

	// IPs: the same address in a column, a list column and free text is one value.
	ip1 := out[0].IPAddress
	if !strings.Contains(out[0].Details, " from "+ip1+";") {
		t.Errorf("IP in free text: %q (column %q)", out[0].Details, ip1)
	}
	parts := strings.Split(out[1].IPAddress, ", ")
	if len(parts) != 2 || !strings.Contains(out[1].Details, "IPs="+parts[1]+",") {
		t.Errorf("IP list: column %q, details %q", out[1].IPAddress, out[1].Details)
	}
	v6 := out[2].IPAddress
	if !strings.Contains(out[3].Details, "["+v6+"]:8443") {
		t.Errorf("IPv6 in brackets with a port: column %q, details %q", v6, out[3].Details)
	}
	// Every replacement address sits in a documentation range.
	for _, s := range []string{ip1, parts[0], parts[1], v6} {
		ip := net.ParseIP(s)
		if ip == nil || !inDocumentationRange(ip) {
			t.Errorf("replacement %q is not a documentation address", s)
		}
	}
	// "system" and the action and type columns are not personal data and are left alone.
	if out[3].ActorID != "system" || out[0].Action != "reservation.created" {
		t.Errorf("system values changed: %q %q", out[3].ActorID, out[0].Action)
	}
}

func inDocumentationRange(ip net.IP) bool {
	for _, cidr := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err) // a constant above; unreachable unless it is mistyped
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// No original email, subdomain or IP may appear anywhere in an anonymised export -- read from the
// CSV bytes the endpoint would send, case-insensitively, not from the anonymiser's own fields.
func TestAnonymisedExportCarriesNoOriginals(t *testing.T) {
	srv := &Server{}
	srv.cfg = nil
	var b strings.Builder
	rows := anonFixture()
	// writeAuditCSV with no config learns the base domains from structured targets only, so this
	// also covers a gateway whose config lists none of them.
	if err := srv.writeAuditCSV(&b, rows, true); err != nil {
		t.Fatalf("writeAuditCSV: %v", err)
	}
	out := strings.ToLower(b.String())

	originals := []string{
		"jane.doe@acme-corp.test", "max-power@other-co.test",
		"shop-alpha.tunnels.test", "shop-alpha.eu", "zz-legacy-host",
		"203.0.113.77", "198.51.100.23", "10.20.30.40",
		"2001:0db8:85a3::8a2e:0370:7334", "2001:db8:85a3::8a2e:370:7334", "2a02:c7f:1234::99",
	}
	for _, o := range originals {
		if strings.Contains(out, o) {
			t.Errorf("original %q survived anonymisation:\n%s", o, b.String())
		}
	}
	// The bare label is only allowed inside the two longer words that merely contain it.
	if c := strings.Count(out, "shop-alpha"); c != 2 {
		t.Errorf("shop-alpha appears %d times; want only inside shop-alphabet and xshop-alpha:\n%s", c, b.String())
	}
	// And the input rows are not modified: anonymising must not leak back into the caller.
	if rows[0].ActorID != "Jane.Doe@acme-corp.test" {
		t.Errorf("writeAuditCSV modified its input")
	}
}

func TestAnonymiseUsesFreshValuesForEachExport(t *testing.T) {
	a, b := anonymise(t, anonFixture()), anonymise(t, anonFixture())
	if a[0].ActorID == b[0].ActorID {
		t.Errorf("two exports gave the same email replacement %q", a[0].ActorID)
	}
	if a[0].TargetID == b[0].TargetID {
		t.Errorf("two exports gave the same subdomain replacement %q", a[0].TargetID)
	}
}

// Distinct originals never share a replacement, including past the 762 documentation IPv4
// addresses, where the fallback range takes over.
func TestAnonymiseNeverMergesDistinctOriginals(t *testing.T) {
	var rows []*db.AuditEntry
	const n = 900
	for i := 0; i < n; i++ {
		rows = append(rows, &db.AuditEntry{
			ID:        int64(i),
			ActorID:   "user-" + strconv.Itoa(i) + "@corp.test",
			TargetID:  "host-" + strconv.Itoa(i) + ".tunnels.test",
			IPAddress: "10." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250) + ".9",
		})
	}
	out := anonymise(t, rows)
	seen := map[string]bool{}
	docs, spill := 0, 0
	for _, e := range out {
		for _, v := range []string{e.ActorID, e.TargetID, e.IPAddress} {
			if seen[v] {
				t.Fatalf("replacement %q issued twice", v)
			}
			seen[v] = true
		}
		ip := net.ParseIP(e.IPAddress)
		switch {
		case ip != nil && inDocumentationRange(ip):
			docs++
		case strings.HasPrefix(e.IPAddress, "240."):
			spill++
		default:
			t.Errorf("address %q is neither documentation nor the fallback range", e.IPAddress)
		}
	}
	if docs != 762 || spill != n-762 {
		t.Errorf("documentation=%d fallback=%d; want 762 and %d", docs, spill, n-762)
	}
}
