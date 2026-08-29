// Package geo turns a client IP into a country code in memory, and aggregates the
// result into counts that carry no identity.
//
// The privacy design (#1152) is deliberate and load-bearing:
//
//   - An IP is resolved to an ISO 3166-1 alpha-2 country code and then discarded. The
//     two are never written together, and never logged together.
//   - Only cardinalities are persisted -- see Aggregator. The set of user IDs behind a
//     count lives in memory for the current period and is never returned by any method
//     on this package, so there is no shape of code here that can flush it to a store,
//     a log line, or a diagnostic endpoint.
//   - A lookup must never be able to block a user connecting. Every entry point is
//     nil-safe and non-fatal: with no database configured the feature reports itself
//     unavailable and registration proceeds untouched.
package geo

import (
	"errors"
	"fmt"
	"net/netip"
	"os"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// ErrUnavailable reports that no geo-IP database is configured or readable, which is a
// normal state rather than a failure: the deployment simply has no MaxMind file.
var ErrUnavailable = errors.New("geo: no geo-IP database available")

// Resolver maps an IP address to an ISO 3166-1 alpha-2 country code.
type Resolver interface {
	// Country returns the country code for ip. The second result is false when the
	// address is not in the database at all -- private ranges, CGNAT and unallocated
	// space all land here.
	Country(ip netip.Addr) (string, bool)
	Close() error
}

// mmdbResolver reads a MaxMind GeoLite2/GeoIP2 database.
//
// MaxMind rather than a lookup API because the whole premise of this feature is that the
// IP is resolved in memory and discarded; posting every user's IP to a third party to
// arrive at an anonymous aggregate would invert that.
type mmdbResolver struct {
	db *maxminddb.Reader
}

// OpenResolver opens the MaxMind database at path.
//
// An empty path, or a path that does not exist, returns ErrUnavailable rather than a hard
// error: not deploying a MaxMind file is a supported configuration.
func OpenResolver(path string) (Resolver, error) {
	if path == "" {
		return nil, ErrUnavailable
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrUnavailable
		}
		return nil, fmt.Errorf("geo: stat %s: %w", path, err)
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geo: open %s: %w", path, err)
	}
	return &mmdbResolver{db: db}, nil
}

// Country decodes only the country ISO code. Decoding the whole record would pull city,
// subdivision and lat/long into memory, which this feature has no use for and which are
// far more identifying than a country -- so the narrow path is the safe one as well as
// the cheap one.
func (r *mmdbResolver) Country(ip netip.Addr) (string, bool) {
	if r == nil || r.db == nil || !ip.IsValid() {
		return "", false
	}
	// A v4-in-v6 address resolves either way, but unmapping keeps the lookup on the v4
	// tree, where the database is denser.
	ip = ip.Unmap()
	res := r.db.Lookup(ip)
	if !res.Found() || res.Err() != nil {
		return "", false
	}
	var iso string
	if err := res.DecodePath(&iso, "country", "iso_code"); err != nil || iso == "" {
		return "", false
	}
	return iso, true
}

func (r *mmdbResolver) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}
