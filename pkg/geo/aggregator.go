package geo

import (
	"crypto/sha256"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// OtherBucket collects the countries that did not reach the k-threshold. It is a
// reserved bucket name: no ISO 3166-1 alpha-2 code is five characters long, so it cannot
// collide with a real country.
const OtherBucket = "OTHER"

// DefaultThreshold is the k in k-anonymity for this feature. At the current user count a
// bucket of one, set beside the user list, identifies the person -- so a bucket has to
// hold at least this many distinct users before it is allowed to exist at all.
const DefaultThreshold = 5

// BucketCount is one row of the anonymised aggregate: a bucket, and how many distinct
// users were seen in it during a period. There is deliberately no third field.
type BucketCount struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

// Store persists the aggregate. The signature is the guard rail: it can only ever be
// handed a bucket and a cardinality, so no implementation of it -- present or future --
// is able to write a user against a location.
type Store interface {
	UpsertLocationStats(period string, counts []BucketCount) error
}

// userKey identifies a user within a period without being the user.
//
// Deduplication needs equality, not the identity itself, and db.User.ID is an email
// address -- so the set holds a digest instead. This is defence in depth against a heap
// dump or a stray formatter, not an anonymity claim in its own right: a digest of a known
// user list is still reversible. The anonymity comes from the set being transient and
// never persisted.
type userKey [sha256.Size]byte

// Aggregator counts distinct users per country per period.
//
// Distinct users, not sessions: session counting measures reconnections, so one user
// having a bad morning would outweigh a dozen stable ones. The client authenticates with
// a PAT, so a stable user identity is available at exactly the point of geolocation --
// which is what makes the dedupe exact, and also what makes the anonymity depend entirely
// on choosing not to persist the pair.
//
// A nil *Aggregator is valid and does nothing. That is how "no MaxMind database
// configured" is represented, so no call site needs a branch for it.
type Aggregator struct {
	resolver  Resolver
	store     Store
	threshold int
	now       func() time.Time

	mu     sync.Mutex
	period string
	// seen maps a country code to the set of users observed there this period. It is the
	// only place a user and a location coexist, it never leaves this struct, and it is
	// discarded at every period rollover. Nothing on this type returns it, so there is no
	// shape of code here that can reach it from a debug endpoint or a diagnostic dump.
	seen map[string]map[userKey]struct{}
}

// Options tunes an Aggregator. The zero value asks for the defaults.
type Options struct {
	// Threshold overrides DefaultThreshold. Values below 2 are ignored -- a threshold of
	// 1 or 0 would disable the anonymity this whole package exists to provide.
	Threshold int
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// New returns an Aggregator writing to store.
//
// A nil resolver or store yields a nil *Aggregator, i.e. a working no-op, because the
// feature being unavailable must never become an error a caller has to handle on the
// registration path.
func New(resolver Resolver, store Store, opts Options) *Aggregator {
	if resolver == nil || store == nil {
		return nil
	}
	threshold := opts.Threshold
	if threshold < 2 {
		threshold = DefaultThreshold
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Aggregator{
		resolver:  resolver,
		store:     store,
		threshold: threshold,
		now:       now,
		period:    PeriodKey(now()),
		seen:      make(map[string]map[userKey]struct{}),
	}
}

// PeriodKey is the ISO week containing t, as "2026-W35".
//
// Weekly rather than hourly: an hourly count of a rare country is effectively a timestamp
// for one individual, which is the opposite of what this feature is for.
func PeriodKey(t time.Time) string {
	year, week := t.UTC().ISOWeek()
	return pad(year, 4) + "-W" + pad(week, 2)
}

func pad(v, width int) string {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte('0' + v%10)
		v /= 10
	}
	return string(out)
}

// Observe records that userID was seen from ip.
//
// The IP is resolved here and goes no further: only the country code is retained, against
// a digest of a user ID that is itself discarded at rollover. Failure of any kind is
// silent -- the caller is on the registration path, and a geo lookup must never be able
// to stop a user connecting.
func (a *Aggregator) Observe(userID string, ip netip.Addr) {
	if a == nil || userID == "" || !ip.IsValid() {
		return
	}
	country, ok := a.resolver.Country(ip)
	if !ok {
		// Private ranges, CGNAT and addresses missing from the database. Counting them
		// under a catch-all bucket would quietly turn "we could not tell" into a
		// geographic claim, so they are dropped instead.
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.rotateLocked()
	users := a.seen[country]
	if users == nil {
		users = make(map[userKey]struct{})
		a.seen[country] = users
	}
	users[sha256.Sum256([]byte(userID))] = struct{}{}
}

// Flush rolls the period over if the clock has moved past it, then writes the current
// period's cardinalities with the k-threshold applied.
//
// It is safe and cheap to call on a schedule with no traffic in between. Calling it more
// often than the period length is the point: the period is a week, and a panel that
// stayed empty for six days would be useless, so the running total is written out
// repeatedly and the period boundary is what resets it.
func (a *Aggregator) Flush() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	a.rotateLocked()
	period, counts := a.period, a.snapshotLocked()
	a.mu.Unlock()
	if len(counts) == 0 {
		return nil
	}
	return a.store.UpsertLocationStats(period, counts)
}

// Close flushes a final time and releases the database handle.
func (a *Aggregator) Close() error {
	if a == nil {
		return nil
	}
	err := a.Flush()
	if cerr := a.resolver.Close(); err == nil {
		err = cerr
	}
	return err
}

// rotateLocked writes out and discards the previous period once the clock has moved past
// it. Callers must hold a.mu.
func (a *Aggregator) rotateLocked() {
	current := PeriodKey(a.now())
	if current == a.period {
		return
	}
	previous, counts := a.period, a.snapshotLocked()
	a.period = current
	// The sets are dropped here, not merely emptied -- the whole anonymity claim rests on
	// the users behind a count not outliving the count.
	a.seen = make(map[string]map[userKey]struct{})
	if len(counts) > 0 {
		// Not surfaced to the caller -- this runs on the registration path, and a failed
		// analytics write must never fail a connection -- but not swallowed either. The
		// period has already rolled and the in-memory sets are gone by the time this runs,
		// so a silent failure is the one case where a whole week of counts disappears with
		// nothing anywhere saying why.
		if err := a.store.UpsertLocationStats(previous, counts); err != nil {
			slog.Warn("[Geo] Failed to write location stats; this period's counts are lost",
				"period", previous, "buckets", len(counts), "error", err)
		}
	}
}

// snapshotLocked turns the in-memory sets into k-thresholded counts. Callers must hold
// a.mu.
//
// The threshold is applied here, on the way to storage, rather than at render time. A
// count that only the renderer suppresses still exists in the database to be read by
// anyone holding the file, which is not anonymity.
func (a *Aggregator) snapshotLocked() []BucketCount {
	counts := make([]BucketCount, 0, len(a.seen))
	other := 0
	for country, users := range a.seen {
		if len(users) < a.threshold {
			// Summed, so a user appearing in two sub-threshold countries counts twice
			// here. Deduplicating across them would mean holding the union of their user
			// digests, which is exactly the linkage this design refuses to build.
			other += len(users)
			continue
		}
		counts = append(counts, BucketCount{Bucket: country, Count: len(users)})
	}
	if other >= a.threshold {
		counts = append(counts, BucketCount{Bucket: OtherBucket, Count: other})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Bucket < counts[j].Bucket
	})
	return counts
}
