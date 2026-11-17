package server

import (
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// Audit Log export (#2464).
//
// The export used to ignore the request entirely and return at most 50 rows -- an empty
// AuditFilter fell into the repository's default limit -- while both portals told admins to "Use
// Export for the full history". It now exports exactly what the admin is looking at: the same
// search, the same sort, every matching row rather than only the loaded ones, optionally a
// selection of rows by their position, and optionally anonymised. All of it happens here, so an
// anonymised export never carries raw personal data to the browser.

// auditSearchFields are the fields the Audit Log search box matches, in both portal arms. V1
// used to match every field (including the id and the raw timestamp) and V2 these six; they are
// one definition now, because a row number in the export must mean the row the admin can see.
var auditSearchFields = []string{
	auditFieldActor, auditFieldAction, auditFieldTargetType, auditFieldTargetID, auditFieldIP, auditFieldDetails,
}

// The audit entry's JSON field names, which are what both portals sort and search by.
const (
	auditFieldActor      = "actor_id"
	auditFieldAction     = "action"
	auditFieldTargetType = "target_type"
	auditFieldTargetID   = "target_id"
	auditFieldIP         = "ip_address"
	auditFieldDetails    = "details"
	auditFieldCreatedAt  = "created_at"
)

// auditFieldValue returns a field as both portals render it into their sort and search: the JSON
// value. The timestamp is therefore compared as its RFC 3339 text, exactly as the views compare
// it, rather than chronologically -- the two differ only for entries within the same second.
func auditFieldValue(e *db.AuditEntry, field string) (string, bool) {
	switch field {
	case auditFieldActor:
		return e.ActorID, true
	case auditFieldAction:
		return e.Action, true
	case auditFieldTargetType:
		return e.TargetType, true
	case auditFieldTargetID:
		return e.TargetID, true
	case auditFieldIP:
		return e.IPAddress, true
	case auditFieldDetails:
		return e.Details, true
	case auditFieldCreatedAt:
		return e.CreatedAt.Format(time.RFC3339Nano), true
	}
	return "", false
}

// auditView is the search and sort the admin has applied to the Audit Log.
type auditView struct {
	query   string
	sortKey string
	desc    bool
}

func auditViewFromQuery(q url.Values) (auditView, error) {
	v := auditView{query: q.Get("q"), sortKey: q.Get("sort")}
	if v.sortKey != "" {
		if _, ok := auditFieldValue(&db.AuditEntry{}, v.sortKey); !ok {
			return v, fmt.Errorf("unknown sort column %q", v.sortKey)
		}
	}
	switch q.Get("dir") {
	case "", "asc":
	case "desc":
		v.desc = true
	default:
		return v, fmt.Errorf("unknown sort direction %q", q.Get("dir"))
	}
	return v, nil
}

// apply filters and sorts entries the way both portals do. The search is a case-insensitive
// substring match over auditSearchFields; a query of only whitespace is no search, as in V2. The
// sort compares lowercased values and is stable, so ties keep the server's own order (newest
// first) in either direction -- which is what a stable JavaScript sort does to the same input.
func (v auditView) apply(entries []*db.AuditEntry) []*db.AuditEntry {
	out := entries
	if strings.TrimSpace(v.query) != "" {
		needle := strings.ToLower(v.query)
		out = make([]*db.AuditEntry, 0, len(entries))
		for _, e := range entries {
			for _, f := range auditSearchFields {
				val, _ := auditFieldValue(e, f)
				if strings.Contains(strings.ToLower(val), needle) {
					out = append(out, e)
					break
				}
			}
		}
	}
	if v.sortKey != "" {
		sorted := make([]*db.AuditEntry, len(out))
		copy(sorted, out)
		sort.SliceStable(sorted, func(i, j int) bool {
			a, _ := auditFieldValue(sorted[i], v.sortKey)
			b, _ := auditFieldValue(sorted[j], v.sortKey)
			if v.desc {
				return strings.ToLower(a) > strings.ToLower(b)
			}
			return strings.ToLower(a) < strings.ToLower(b)
		})
		out = sorted
	}
	return out
}

// errRowSelection is a selection the admin typed that cannot be applied. Its message is shown to
// them as-is, under the field.
type errRowSelection struct{ msg string }

func (e errRowSelection) Error() string { return e.msg }

// parseRowSelection turns "4-9,12,18" into 0-based indexes into a view of total rows, in view
// order with duplicates and overlaps collapsed. An empty selection means every row and returns
// nil. Numbers are 1-based, as the admin counts them.
func parseRowSelection(spec string, total int) ([]int, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	selected := make(map[int]bool)
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return nil, errRowSelection{"empty entry: remove the extra comma"}
		}
		lo, hi := item, item
		if i := strings.Index(item, "-"); i >= 0 {
			lo, hi = strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+1:])
			if lo == "" {
				return nil, errRowSelection{fmt.Sprintf("%q: row numbers start at 1", item)}
			}
		}
		from, err := parseRowNumber(item, lo, total)
		if err != nil {
			return nil, err
		}
		to, err := parseRowNumber(item, hi, total)
		if err != nil {
			return nil, err
		}
		if from > to {
			return nil, errRowSelection{fmt.Sprintf("%q is reversed: write %d-%d", item, to, from)}
		}
		for n := from; n <= to; n++ {
			selected[n-1] = true
		}
	}
	out := make([]int, 0, len(selected))
	for i := range selected {
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}

func parseRowNumber(item, s string, total int) (int, error) {
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errRowSelection{fmt.Sprintf("%q is not a row number or range", item)}
		}
	}
	if s == "" {
		return 0, errRowSelection{fmt.Sprintf("%q is not a row number or range", item)}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > total {
		// Atoi fails here only on overflow, which is beyond any row count too.
		return 0, errRowSelection{fmt.Sprintf("%q is beyond the %d rows that match", item, total)}
	}
	if n < 1 {
		return 0, errRowSelection{fmt.Sprintf("%q: row numbers start at 1", item)}
	}
	return n, nil
}

// auditSubjectTargetTypes are the target types whose target_id names a tunnel host or label.
var auditSubjectTargetTypes = map[string]bool{resourceKindSubdomain: true, auditTargetLease: true, auditTargetTunnel: true}

// Audit target types written for a tunnel's lease and for the tunnel itself.
const (
	auditTargetLease  = "lease"
	auditTargetTunnel = "tunnel"
)

// What classify found a value to be. Emails are not among them: forEachAuditEmail finds those.
const (
	valueKindHost  = "host"  // a hostname under a tunnel base domain
	valueKindLabel = "label" // a bare subdomain label already known to this export
	valueKindIP    = "ip"
)

// auditCSVActor is the export's actor column heading.
const auditCSVActor = "Actor"

// auditExportError is the JSON both export endpoints answer with when they cannot. Total is how
// many rows matched, when that is known, so a portal can say what an out-of-range selection was
// measured against.
type auditExportError struct {
	Error string `json:"error"`
	Total int    `json:"total,omitempty"`
}

// isAuditTokenChar is what a hostname or an IPv4 or IPv6 address can be made of ('%' for an IPv6
// zone). Everything else -- spaces, parentheses, commas, '=', '/', '_', quotes -- separates
// values, so a value is only ever replaced whole and never as a substring of a longer word.
// Emails are found separately (forEachAuditEmail), because what an address may contain is far
// wider than this.
func isAuditTokenChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '-' || c == '%' || c == '@' || c == ':'
}

// isAuditEmailDelimiter ends an email address. Everything else around an '@' -- including
// apostrophes and any non-ASCII letter -- is taken as part of it, because the cost of the two
// mistakes is not symmetric: replacing a little too much loses a character of context, while
// stopping short leaves "jürgen.mü" of "jürgen.müller@..." in an anonymised file (review of #2465).
func isAuditEmailDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '<', '>', '(', ')', '[', ']', '{', '}', '"', ',', ';', '=', ':', '/', '\\', '`', '|':
		return true
	}
	return false
}

// forEachAuditEmail calls fn on every "local@domain" in s and splices in what fn returns. Any
// non-empty local part and domain qualify -- admin@localhost, an IDN, ubuntu@10.1.2.3 -- since a
// value with an '@' in an audit row is far likelier to identify someone than not.
func forEachAuditEmail(s string, fn func(string) string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		start := i
		for start > last && !isAuditEmailDelimiter(s[start-1]) {
			start--
		}
		end := i + 1
		for end < len(s) && !isAuditEmailDelimiter(s[end]) && s[end] != '@' {
			end++
		}
		// A JSON-escaped '<' ("<jane@...") leaves "u003c" glued to the local part.
		if start > 0 && s[start-1] == '\\' && end-start > 5 && strings.HasPrefix(strings.ToLower(s[start:]), "u00") {
			start += 5
		}
		// Quotes and sentence punctuation around an address are not part of it.
		for start < i && strings.IndexByte(`'.`, s[start]) >= 0 {
			start++
		}
		for end > i+1 && strings.IndexByte(`'.`, s[end-1]) >= 0 {
			end--
		}
		if start == i || end == i+1 {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(fn(s[start:end]))
		last = end
		i = end - 1
	}
	b.WriteString(s[last:])
	return b.String()
}

// auditAnonymiser replaces emails, tunnel subdomains and IP addresses in one export.
//
// Within an export the same original always gets the same replacement, so one actor's actions
// can still be followed across rows; emails and hostnames match case-insensitively. Each export
// builds a new anonymiser with fresh random values and keeps nothing, so two files cannot be
// linked through their replacements. No two originals share a replacement, and no replacement
// equals any original in the export.
type auditAnonymiser struct {
	rand  io.Reader
	bases []string // tunnel base domains, lowercase, longest first

	emails     map[string]string // lowercase email -> replacement
	subdomains map[string]string // lowercase label(s) before a base domain -> replacement
	ips        map[string]string // canonical address -> replacement

	used      map[string]bool // every replacement handed out
	originals map[string]bool // every original value, lowercase/canonical

	v4Pool  []string // unused documentation addresses, shuffled
	v4Spill int      // addresses issued from the fallback range
}

// documentationV4Ranges are RFC 5737's three /24s, reserved for documentation: 762 usable hosts.
var documentationV4Ranges = []string{"192.0.2.", "198.51.100.", "203.0.113."}

func newAuditAnonymiser(rnd io.Reader, baseDomains []string) *auditAnonymiser {
	a := &auditAnonymiser{
		rand:       rnd,
		emails:     map[string]string{},
		subdomains: map[string]string{},
		ips:        map[string]string{},
		used:       map[string]bool{},
		originals:  map[string]bool{},
	}
	seen := map[string]bool{}
	for _, d := range baseDomains {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if d != "" && !seen[d] {
			seen[d] = true
			a.bases = append(a.bases, d)
		}
	}
	a.sortBases()
	return a
}

func (a *auditAnonymiser) sortBases() {
	sort.SliceStable(a.bases, func(i, j int) bool { return len(a.bases[i]) > len(a.bases[j]) })
}

// anonymisedColumns are the CSV columns that can carry personal data. The id, action, target type
// and timestamp are system values and are left alone, so an enum can never be mangled by a
// subdomain that happens to share its name.
func anonymisedColumns(e *db.AuditEntry) []*string {
	return []*string{&e.ActorID, &e.TargetID, &e.Details, &e.IPAddress}
}

// prepare learns every original before anything is replaced, so a bare subdomain label in free
// text is recognised even when its structured row comes later, and so no replacement can collide
// with an original that has not been reached yet.
//
// rows should be the whole log the export was drawn from, not only the rows being exported: a
// label mentioned in an exported row's details but structured only in a row outside the selection
// would otherwise survive (review of #2465).
func (a *auditAnonymiser) prepare(rows []*db.AuditEntry) {
	// Structured tunnel targets teach the bare labels. They teach a base domain only when it lies
	// under a configured one (a regional edge's us.<domain>, on central): a vanity or custom domain
	// recorded under the same target type would otherwise teach "com", and every *.com host in the
	// export would be rewritten. With no configured domains at all, any base of two or more labels
	// is learned instead, so a bare test server is still covered.
	configured := append([]string(nil), a.bases...)
	underConfigured := func(base string) bool {
		if len(configured) == 0 {
			return strings.Contains(base, ".")
		}
		for _, c := range configured {
			if base == c || strings.HasSuffix(base, "."+c) {
				return true
			}
		}
		return false
	}
	for _, e := range rows {
		if !auditSubjectTargetTypes[e.TargetType] || e.TargetID == "" {
			continue
		}
		target := strings.Trim(strings.ToLower(e.TargetID), ".")
		i := strings.Index(target, ".")
		if i < 0 {
			a.subdomains[target] = ""
			continue
		}
		if base := target[i+1:]; underConfigured(base) {
			a.subdomains[target[:i]] = ""
			a.bases = append(a.bases, base)
			a.sortBases()
		}
	}
	for _, e := range rows {
		for _, col := range anonymisedColumns(e) {
			forEachAuditEmail(*col, func(email string) string {
				a.emails[strings.ToLower(email)] = ""
				return email
			})
			forEachAuditToken(*col, func(tok string) string {
				return a.eachValue(tok, func(v string) string {
					a.learn(v)
					return v
				})
			})
		}
	}
	for k := range a.emails {
		a.originals[k] = true
	}
	for k := range a.subdomains {
		a.originals[k] = true
	}
	for k := range a.ips {
		a.originals[k] = true
	}
}

func (a *auditAnonymiser) learn(tok string) {
	switch kind, key, _ := a.classify(tok); kind {
	case valueKindHost, valueKindLabel:
		a.subdomains[key] = ""
	case valueKindIP:
		a.ips[key] = ""
	}
}

// classify says what a token is and the key its replacement is stored under. For a hostname,
// rest is the base domain to keep; for an address with a port, rest is the port suffix.
func (a *auditAnonymiser) classify(tok string) (kind, key, rest string) {
	// Anything with an '@' has been through forEachAuditEmail already: it is a replacement now.
	if strings.Contains(tok, "@") {
		return "", "", ""
	}
	if ip := parseAuditIP(tok); ip != nil {
		return valueKindIP, ip.String(), ""
	}
	// An IPv6 zone ("fe80::1%eth0") names an interface, not a person: the address is replaced and
	// the zone kept. net.ParseIP refuses the zoned form whole.
	if i := strings.IndexByte(tok, '%'); i > 0 {
		if ip := parseAuditIP(tok[:i]); ip != nil && ip.To4() == nil {
			return valueKindIP, ip.String(), tok[i:]
		}
	}
	if host, port, err := net.SplitHostPort(tok); err == nil {
		if ip := parseAuditIP(host); ip != nil && ip.To4() != nil {
			return valueKindIP, ip.String(), ":" + port
		}
	}
	lower := strings.ToLower(tok)
	if label, base, ok := a.splitHost(lower); ok {
		return valueKindHost, label, base
	}
	if _, known := a.subdomains[lower]; known {
		return valueKindLabel, lower, ""
	}
	return "", "", ""
}

// parseAuditIP parses an address, refusing tokens like "::" that parse but are not one anybody
// wrote as an address.
func parseAuditIP(s string) net.IP {
	if len(s) < 3 {
		return nil
	}
	return net.ParseIP(s)
}

// splitHost splits "acme.<base>" into ("acme", "<base>") for the longest known base domain.
func (a *auditAnonymiser) splitHost(host string) (label, base string, ok bool) {
	for _, b := range a.bases {
		if strings.HasSuffix(host, "."+b) && len(host) > len(b)+1 {
			return host[:len(host)-len(b)-1], b, true
		}
	}
	return "", "", false
}

// eachValue calls fn on tok, or -- when tok is not a value itself but values joined by colons, as
// in "mailto:jane@example.org" or "acme.<base>:443" -- on each part. An address is classified
// before this split, so IPv6 is never taken apart.
func (a *auditAnonymiser) eachValue(tok string, fn func(string) string) string {
	if kind, _, _ := a.classify(tok); kind != "" || !strings.Contains(tok, ":") {
		return fn(tok)
	}
	parts := strings.Split(tok, ":")
	for i, p := range parts {
		if p != "" {
			parts[i] = fn(p)
		}
	}
	return strings.Join(parts, ":")
}

// rewrite returns s with every email, tunnel subdomain and IP address replaced. Emails go first,
// whole, so a hostname or address inside one ("ubuntu@10.1.2.3") is never left behind by a
// replacement of only its other half.
func (a *auditAnonymiser) rewrite(s string) (string, error) {
	var failed error
	s = forEachAuditEmail(s, func(email string) string {
		if failed != nil {
			return email
		}
		r, err := a.replacement(a.emails, strings.ToLower(email), a.newEmail)
		if err != nil {
			failed = err
			return email
		}
		return r
	})
	out := forEachAuditToken(s, func(tok string) string {
		return a.eachValue(tok, func(v string) string { return a.rewriteValue(v, &failed) })
	})
	return out, failed
}

// rewriteValue replaces one value, recording the first failure in failed and leaving everything
// after it untouched -- the caller discards the whole export on any failure.
func (a *auditAnonymiser) rewriteValue(tok string, failed *error) string {
	if *failed != nil {
		return tok
	}
	kind, key, rest := a.classify(tok)
	var repl string
	var err error
	switch kind {
	case valueKindHost:
		repl, err = a.replacement(a.subdomains, key, a.newSubdomain)
		repl += "." + rest
	case valueKindLabel:
		repl, err = a.replacement(a.subdomains, key, a.newSubdomain)
	case valueKindIP:
		repl, err = a.replacement(a.ips, key, func() (string, error) { return a.newIP(key) })
		repl += rest
	default:
		return tok
	}
	if err != nil {
		*failed = err
		return tok
	}
	return repl
}

func (a *auditAnonymiser) replacement(m map[string]string, key string, mint func() (string, error)) (string, error) {
	if r := m[key]; r != "" {
		return r, nil
	}
	for attempt := 0; attempt < 64; attempt++ {
		r, err := mint()
		if err != nil {
			return "", err
		}
		if !a.used[r] && !a.originals[strings.ToLower(r)] {
			a.used[r] = true
			m[key] = r
			return r, nil
		}
	}
	return "", errors.New("could not mint a unique replacement")
}

func (a *auditAnonymiser) randomString(n int, alphabet string) (string, error) {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(a.rand, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b), nil
}

func (a *auditAnonymiser) newEmail() (string, error) {
	s, err := a.randomString(8, "abcdefghijklmnopqrstuvwxyz")
	return s + "@example.com", err
}

func (a *auditAnonymiser) newSubdomain() (string, error) {
	s, err := a.randomString(6, "abcdefghijklmnopqrstuvwxyz0123456789")
	return "sub-" + s, err
}

func (a *auditAnonymiser) newIP(original string) (string, error) {
	if ip := net.ParseIP(original); ip != nil && ip.To4() == nil {
		// 2001:db8::/32 (RFC 3849) holds 2^96 addresses; uniqueness is checked by the caller.
		b := make([]byte, 12)
		if _, err := io.ReadFull(a.rand, b); err != nil {
			return "", err
		}
		v6 := append(net.ParseIP("2001:db8::").To16()[:4:4], b...)
		return net.IP(v6).String(), nil
	}
	if a.v4Pool == nil {
		if err := a.fillV4Pool(); err != nil {
			return "", err
		}
	}
	if len(a.v4Pool) > 0 {
		r := a.v4Pool[len(a.v4Pool)-1]
		a.v4Pool = a.v4Pool[:len(a.v4Pool)-1]
		return r, nil
	}
	// FALLBACK, once the 762 documentation addresses are spent. 240.0.0.0/4 is RFC 1112's
	// "reserved for future use" block: never assigned, never routed, so no reader can mistake one
	// of these for a real visitor -- which is the property the documentation ranges give, and the
	// reason a private range (10/8, which real gateways do log) would not do. Issued in sequence
	// rather than at random: 2^28 addresses are far more than any export, and sequence cannot
	// collide with itself.
	a.v4Spill++
	n := a.v4Spill
	return fmt.Sprintf("240.%d.%d.%d", (n>>16)&0xff, (n>>8)&0xff, n&0xff), nil
}

func (a *auditAnonymiser) fillV4Pool() error {
	pool := make([]string, 0, 762)
	for _, prefix := range documentationV4Ranges {
		for host := 1; host <= 254; host++ {
			pool = append(pool, prefix+strconv.Itoa(host))
		}
	}
	// Fisher-Yates, so which documentation address a visitor gets says nothing about order.
	for i := len(pool) - 1; i > 0; i-- {
		k, err := rand.Int(a.rand, big.NewInt(int64(i+1)))
		if err != nil {
			return err
		}
		j := int(k.Int64())
		pool[i], pool[j] = pool[j], pool[i]
	}
	a.v4Pool = pool
	return nil
}

// forEachAuditToken calls fn on every maximal run of token characters in s, trimmed of the
// punctuation that ends a sentence or a clause ("acme.example.com." or "1.2.3.4:"), and splices
// in what fn returns. Everything between tokens is kept byte for byte.
func forEachAuditToken(s string, fn func(string) string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if !isAuditTokenChar(s[i]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && isAuditTokenChar(s[j]) {
			j++
		}
		tok := s[i:j]
		start, end := 0, len(tok)
		for start < end && strings.ContainsRune(".-:", rune(tok[start])) && !strings.HasPrefix(tok[start:], "::") {
			start++
		}
		for end > start && strings.ContainsRune(".-:", rune(tok[end-1])) && !strings.HasSuffix(tok[:end], "::") {
			end--
		}
		b.WriteString(tok[:start])
		if start < end {
			b.WriteString(fn(tok[start:end]))
		}
		b.WriteString(tok[end:])
		i = j
	}
	return b.String()
}

// allAuditEntries reads the whole log in the repository's 200-row pages, newest first, without
// routine portal visits -- the same rows, in the same order, as the view loads. Deduplicated by
// id, because an entry written mid-read shifts every later page by one.
func (s *Server) allAuditEntries() ([]*db.AuditEntry, error) {
	const page = 200
	seen := map[int64]bool{}
	var all []*db.AuditEntry
	for offset := 0; ; offset += page {
		entries, err := s.db.ListAuditEntries(db.AuditFilter{Limit: page, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !seen[e.ID] {
				seen[e.ID] = true
				all = append(all, e)
			}
		}
		if len(entries) < page {
			return all, nil
		}
	}
}

// auditExport is what one export request resolves to.
type auditExport struct {
	rows  []*db.AuditEntry // the rows to export, in view order
	total int              // how many rows the view matched
	log   []*db.AuditEntry // the whole log as of the view, for the anonymiser to learn from
}

// auditExportRows applies as_of, the view and the selection.
//
// as_of is the newest entry id the admin's view had loaded. Without it the export re-read the live
// log, so every entry written since the page loaded -- a tunnel starting, someone signing in --
// shifted every newest-first position, and "4-9" exported rows the admin had never seen. With
// anonymisation on, nothing in the file would show it (review of #2465). Preview and export send
// the same as_of, so their counts agree as well.
func (s *Server) auditExportRows(q url.Values) (auditExport, int, error) {
	view, err := auditViewFromQuery(q)
	if err != nil {
		return auditExport{}, http.StatusBadRequest, err
	}
	var asOf int64
	if v := q.Get("as_of"); v != "" {
		if asOf, err = strconv.ParseInt(v, 10, 64); err != nil || asOf < 0 {
			return auditExport{}, http.StatusBadRequest, fmt.Errorf("as_of %q is not an entry id", v)
		}
	}
	entries, err := s.allAuditEntries()
	if err != nil {
		return auditExport{}, http.StatusInternalServerError, errors.New("failed to list audit entries")
	}
	if asOf > 0 {
		kept := entries[:0:0]
		for _, e := range entries {
			if e.ID <= asOf {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	matched := view.apply(entries)
	out := auditExport{rows: matched, total: len(matched), log: entries}
	sel, err := parseRowSelection(q.Get("rows"), len(matched))
	if err != nil {
		return out, http.StatusBadRequest, err
	}
	if sel != nil {
		out.rows = make([]*db.AuditEntry, 0, len(sel))
		for _, i := range sel {
			out.rows = append(out.rows, matched[i])
		}
	}
	return out, http.StatusOK, nil
}

// handleAdminAuditExportPreview says how many rows an export would contain, or why the selection
// cannot be applied, so both portals can show "Exporting N of M rows" or the error before any
// download starts.
func (s *Server) handleAdminAuditExportPreview(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		respondJSON(w, http.StatusNotImplemented, auditExportError{Error: "Database not configured"})
		return
	}
	exp, status, err := s.auditExportRows(r.URL.Query())
	if err != nil {
		respondJSON(w, status, auditExportError{Error: err.Error(), Total: exp.total})
		return
	}
	respondJSON(w, http.StatusOK, map[string]int{"selected": len(exp.rows), "total": exp.total})
}

// auditExportFilename is audit-log-YYYY-MM-DD.csv, by the server's UTC date.
func auditExportFilename(now time.Time, anonymised bool) string {
	name := "audit-log-" + now.UTC().Format("2006-01-02")
	if anonymised {
		name += "-anonymised"
	}
	return name + ".csv"
}

// writeAuditCSV writes rows as the export's CSV. With anonymise, every row is rewritten before
// any byte is written, so a failure cannot leave a half-anonymised file behind; the anonymiser
// learns from log (nil means rows), so values structured only outside the selection are known.
func (s *Server) writeAuditCSV(w io.Writer, rows, log []*db.AuditEntry, anonymise bool) error {
	if anonymise {
		var bases []string
		if s.cfg != nil {
			bases = append(append(bases, s.cfg.Domains...), s.cfg.TunnelDomains...)
		}
		anon := newAuditAnonymiser(rand.Reader, bases)
		copies := make([]*db.AuditEntry, len(rows))
		for i, e := range rows {
			c := *e
			copies[i] = &c
		}
		if log == nil {
			log = rows
		}
		anon.prepare(log)
		for _, e := range copies {
			for _, col := range anonymisedColumns(e) {
				out, err := anon.rewrite(*col)
				if err != nil {
					return err
				}
				*col = out
			}
		}
		rows = copies
	}

	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"ID", auditCSVActor, "Action", "TargetType", "TargetID", "Details", "IP", "Timestamp"}); err != nil {
		return err
	}
	for _, e := range rows {
		if err := writer.Write([]string{
			strconv.FormatInt(e.ID, 10),
			csvSafeCell(e.ActorID),
			csvSafeCell(e.Action),
			csvSafeCell(e.TargetType),
			csvSafeCell(e.TargetID),
			csvSafeCell(e.Details),
			csvSafeCell(e.IPAddress),
			e.CreatedAt.Format(time.RFC3339),
		}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// csvSafeCell stops a spreadsheet evaluating a cell as a formula. Audit rows carry text a
// stranger typed -- a refused registration records the address as given -- so "=HYPERLINK(...)"
// could otherwise run in the admin's spreadsheet. The leading apostrophe is OWASP's mitigation:
// spreadsheets show the text as typed and do not evaluate it.
func csvSafeCell(s string) string {
	if s != "" && strings.IndexByte("=+-@\t\r", s[0]) >= 0 {
		return "'" + s
	}
	return s
}
