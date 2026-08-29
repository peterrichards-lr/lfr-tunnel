package geo

import (
	"net/netip"
	"testing"
	"time"
)

// fakeResolver maps an address to whatever the test says it maps to. Addresses absent
// from the map resolve to nothing, which is how a private range or an IP missing from a
// real MaxMind database behaves.
type fakeResolver struct {
	countries map[string]string
	closed    bool
}

func (f *fakeResolver) Country(ip netip.Addr) (string, bool) {
	c, ok := f.countries[ip.String()]
	return c, ok
}

func (f *fakeResolver) Close() error {
	f.closed = true
	return nil
}

// recordingStore captures every write so a test can assert on what would have reached
// the database, rather than on what a renderer would have shown.
type recordingStore struct {
	writes []storeWrite
	err    error
}

type storeWrite struct {
	period string
	counts []BucketCount
}

func (r *recordingStore) UpsertLocationStats(period string, counts []BucketCount) error {
	if r.err != nil {
		return r.err
	}
	cp := make([]BucketCount, len(counts))
	copy(cp, counts)
	r.writes = append(r.writes, storeWrite{period: period, counts: cp})
	return nil
}

func (r *recordingStore) last() storeWrite {
	if len(r.writes) == 0 {
		return storeWrite{}
	}
	return r.writes[len(r.writes)-1]
}

// countOf returns the count stored for bucket in w, and whether it was stored at all.
func countOf(w storeWrite, bucket string) (int, bool) {
	for _, c := range w.counts {
		if c.Bucket == bucket {
			return c.Count, true
		}
	}
	return 0, false
}

func addr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad test address %q: %v", s, err)
	}
	return a
}

// observeN registers n distinct users from ip, named with prefix.
func observeN(a *Aggregator, prefix, ip string, n int, t *testing.T) {
	t.Helper()
	for i := 0; i < n; i++ {
		a.Observe(prefix+"-user-"+string(rune('a'+i)), addr(t, ip))
	}
}

func newTestAggregator(countries map[string]string, store Store, now func() time.Time) *Aggregator {
	return New(&fakeResolver{countries: countries}, store, Options{Now: now})
}

// TestThresholdKeepsSmallBucketsOutOfStorage is the core privacy assertion: a bucket
// below k must never reach the store at all. Asserted against the writes, not against
// any rendered output -- a count the renderer merely hides still exists to be read.
func TestThresholdKeepsSmallBucketsOutOfStorage(t *testing.T) {
	store := &recordingStore{}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{
		"10.0.0.1": "GB",
		"10.0.0.2": "PT",
	}, store, func() time.Time { return fixed })

	// GB clears the default threshold of 5; PT does not.
	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	observeN(agg, "pt", "10.0.0.2", DefaultThreshold-1, t)

	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("expected exactly one write, got %d", len(store.writes))
	}
	w := store.last()

	if got, ok := countOf(w, "GB"); !ok || got != DefaultThreshold {
		t.Errorf("GB: got (%d, %v), want (%d, true)", got, ok, DefaultThreshold)
	}
	if _, ok := countOf(w, "PT"); ok {
		t.Errorf("PT reached storage with %d users, below the threshold of %d", DefaultThreshold-1, DefaultThreshold)
	}
	// Four users is below the threshold, so even the Other bucket must stay unwritten --
	// otherwise the remainder itself becomes a count of one country.
	if _, ok := countOf(w, OtherBucket); ok {
		t.Errorf("%s reached storage holding a sub-threshold remainder", OtherBucket)
	}
}

// TestSubThresholdBucketsFoldIntoOther checks the remainder is preserved once enough of
// it accumulates, so the total is not silently understated.
func TestSubThresholdBucketsFoldIntoOther(t *testing.T) {
	store := &recordingStore{}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{
		"10.0.0.2": "PT",
		"10.0.0.3": "IE",
		"10.0.0.4": "NL",
	}, store, func() time.Time { return fixed })

	observeN(agg, "pt", "10.0.0.2", 2, t)
	observeN(agg, "ie", "10.0.0.3", 2, t)
	observeN(agg, "nl", "10.0.0.4", 2, t)

	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	w := store.last()
	for _, country := range []string{"PT", "IE", "NL"} {
		if _, ok := countOf(w, country); ok {
			t.Errorf("%s reached storage below the threshold", country)
		}
	}
	if got, ok := countOf(w, OtherBucket); !ok || got != 6 {
		t.Errorf("%s: got (%d, %v), want (6, true)", OtherBucket, got, ok)
	}
}

// TestDistinctUsersNotSessions is the difference between measuring where users are and
// measuring who reconnected most.
func TestDistinctUsersNotSessions(t *testing.T) {
	store := &recordingStore{}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{"10.0.0.1": "GB"}, store, func() time.Time { return fixed })

	// Five distinct users, and one of them reconnecting twenty times.
	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	for i := 0; i < 20; i++ {
		agg.Observe("gb-user-a", addr(t, "10.0.0.1"))
	}

	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, _ := countOf(store.last(), "GB"); got != DefaultThreshold {
		t.Errorf("GB: got %d, want %d -- reconnections were counted as users", got, DefaultThreshold)
	}
}

// TestSameUserFromTwoCountriesCountsInBoth documents the churn the design anticipates: a
// laptop moves between home, office, tethering and VPN, so one person legitimately
// appears in more than one bucket.
func TestSameUserFromTwoCountriesCountsInBoth(t *testing.T) {
	store := &recordingStore{}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{
		"10.0.0.1": "GB",
		"10.0.0.9": "ES",
	}, store, func() time.Time { return fixed })

	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	observeN(agg, "es", "10.0.0.9", DefaultThreshold, t)
	// One of the GB users also appears from Spain.
	agg.Observe("gb-user-a", addr(t, "10.0.0.9"))

	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	w := store.last()
	if got, _ := countOf(w, "GB"); got != DefaultThreshold {
		t.Errorf("GB: got %d, want %d", got, DefaultThreshold)
	}
	if got, _ := countOf(w, "ES"); got != DefaultThreshold+1 {
		t.Errorf("ES: got %d, want %d", got, DefaultThreshold+1)
	}
}

// TestUnresolvedAddressesAreDropped covers private ranges and addresses missing from the
// database. Bucketing them would turn "we could not tell" into a geographic claim.
func TestUnresolvedAddressesAreDropped(t *testing.T) {
	store := &recordingStore{}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{}, store, func() time.Time { return fixed })

	observeN(agg, "unknown", "192.168.1.1", 20, t)

	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(store.writes) != 0 {
		t.Errorf("expected no writes for unresolvable addresses, got %d", len(store.writes))
	}
}

// TestRolloverWritesCardinalityAndDropsTheSet is the other half of the anonymity claim:
// once a period ends its count is kept and the users behind it are not.
func TestRolloverWritesCardinalityAndDropsTheSet(t *testing.T) {
	store := &recordingStore{}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) // ISO week 2026-W35
	agg := newTestAggregator(map[string]string{"10.0.0.1": "GB"}, store, func() time.Time { return now })

	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	firstPeriod := PeriodKey(now)

	// Move the clock a week on. The next observation must roll the period over.
	now = now.AddDate(0, 0, 7)
	secondPeriod := PeriodKey(now)
	if firstPeriod == secondPeriod {
		t.Fatalf("test setup: both timestamps landed in %s", firstPeriod)
	}

	// The very same five users reappear, plus one more.
	observeN(agg, "gb", "10.0.0.1", DefaultThreshold+1, t)
	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var wroteFirst, wroteSecond bool
	for _, w := range store.writes {
		switch w.period {
		case firstPeriod:
			wroteFirst = true
			if got, _ := countOf(w, "GB"); got != DefaultThreshold {
				t.Errorf("%s GB: got %d, want %d", firstPeriod, got, DefaultThreshold)
			}
		case secondPeriod:
			wroteSecond = true
			// The new period counts from scratch. This assertion alone cannot tell a
			// rebuilt set from a carried-over one -- the same five users reappear either
			// way -- which is what TestRolloverDiscardsPreviousPeriodUsers is for.
			if got, _ := countOf(w, "GB"); got != DefaultThreshold+1 {
				t.Errorf("%s GB: got %d, want %d", secondPeriod, got, DefaultThreshold+1)
			}
		default:
			t.Errorf("unexpected write for period %q", w.period)
		}
	}
	if !wroteFirst {
		t.Errorf("the closed period %s was never written", firstPeriod)
	}
	if !wroteSecond {
		t.Errorf("the current period %s was never written", secondPeriod)
	}
}

// TestRolloverDiscardsPreviousPeriodUsers proves the set is rebuilt rather than reused:
// after a rollover, a period in which only one user appears must fall below the
// threshold, even though that user was one of five in the period before.
func TestRolloverDiscardsPreviousPeriodUsers(t *testing.T) {
	store := &recordingStore{}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{"10.0.0.1": "GB"}, store, func() time.Time { return now })

	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	now = now.AddDate(0, 0, 7)
	second := PeriodKey(now)

	agg.Observe("gb-user-a", addr(t, "10.0.0.1"))
	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for _, w := range store.writes {
		if w.period != second {
			continue
		}
		if _, ok := countOf(w, "GB"); ok {
			t.Errorf("%s: GB was stored from a single user -- the previous period's set survived the rollover", second)
		}
	}
}

// TestFlushOnQuietServerRollsThePeriodOver covers the case with no traffic at all: the
// scheduled Flush is the only thing that can notice the boundary.
func TestFlushOnQuietServerRollsThePeriodOver(t *testing.T) {
	store := &recordingStore{}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := newTestAggregator(map[string]string{"10.0.0.1": "GB"}, store, func() time.Time { return now })

	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	first := PeriodKey(now)

	now = now.AddDate(0, 0, 7)
	if err := agg.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(store.writes) == 0 {
		t.Fatalf("the closed period was never written")
	}
	if store.writes[0].period != first {
		t.Errorf("first write was for %q, want the closed period %q", store.writes[0].period, first)
	}
	// Nothing was observed in the new period, so there is nothing more to write.
	if len(store.writes) != 1 {
		t.Errorf("expected one write, got %d", len(store.writes))
	}
}

// TestNilAggregatorIsANoOp is the graceful-absence contract every call site relies on.
func TestNilAggregatorIsANoOp(t *testing.T) {
	var agg *Aggregator
	agg.Observe("someone", addr(t, "10.0.0.1"))
	if err := agg.Flush(); err != nil {
		t.Errorf("Flush on nil aggregator: %v", err)
	}
	if err := agg.Close(); err != nil {
		t.Errorf("Close on nil aggregator: %v", err)
	}
}

// TestNewWithoutResolverOrStoreIsNil keeps "unavailable" representable as nil rather than
// as an error the registration path would have to handle.
func TestNewWithoutResolverOrStoreIsNil(t *testing.T) {
	if got := New(nil, &recordingStore{}, Options{}); got != nil {
		t.Errorf("New with no resolver: got %v, want nil", got)
	}
	if got := New(&fakeResolver{}, nil, Options{}); got != nil {
		t.Errorf("New with no store: got %v, want nil", got)
	}
}

// TestThresholdCannotBeDisabled guards the one option that could quietly undo the whole
// design.
func TestThresholdCannotBeDisabled(t *testing.T) {
	for _, k := range []int{0, 1, -3} {
		agg := New(&fakeResolver{}, &recordingStore{}, Options{Threshold: k})
		if agg.threshold != DefaultThreshold {
			t.Errorf("Threshold %d: got %d, want the default %d", k, agg.threshold, DefaultThreshold)
		}
	}
	agg := New(&fakeResolver{}, &recordingStore{}, Options{Threshold: 9})
	if agg.threshold != 9 {
		t.Errorf("Threshold 9: got %d, want 9", agg.threshold)
	}
}

// TestCloseFlushesAndReleasesTheResolver covers shutdown.
func TestCloseFlushesAndReleasesTheResolver(t *testing.T) {
	store := &recordingStore{}
	res := &fakeResolver{countries: map[string]string{"10.0.0.1": "GB"}}
	fixed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	agg := New(res, store, Options{Now: func() time.Time { return fixed }})

	observeN(agg, "gb", "10.0.0.1", DefaultThreshold, t)
	if err := agg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(store.writes) != 1 {
		t.Errorf("expected a final flush, got %d writes", len(store.writes))
	}
	if !res.closed {
		t.Errorf("the resolver was not closed")
	}
}

func TestPeriodKeyIsTheISOWeek(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC), "2026-W35"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-W01"},
		// Same ISO week, four days apart -- the point of a coarse period.
		{time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), "2026-W35"},
		{time.Date(2026, 8, 30, 23, 59, 0, 0, time.UTC), "2026-W35"},
	}
	for _, c := range cases {
		if got := PeriodKey(c.in); got != c.want {
			t.Errorf("PeriodKey(%s): got %q, want %q", c.in.Format(time.RFC3339), got, c.want)
		}
	}
}
