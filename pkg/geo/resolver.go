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
	"path/filepath"
	"strings"

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
		// IP2Location's default download is a .BIN in their own proprietary format, not an
		// mmdb, and maxminddb reports that as an opaque parse error. Named explicitly
		// because the fix is a different DOWNLOAD, not a different path or permission, and
		// nothing else would tell the operator that (#1921).
		if strings.EqualFold(filepath.Ext(path), ".bin") {
			return nil, fmt.Errorf("geo: %s is a .BIN file, which is IP2Location's proprietary "+
				"format and not MaxMind's .mmdb -- download the MMDB edition of the same "+
				"database instead: %w", path, err)
		}
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
	// Vendors disagree about where the country code lives inside the same mmdb format, so
	// the paths are tried in turn (#1921). Verified against real databases:
	//
	//   country.iso_code   MaxMind GeoLite2/GeoIP2, and DB-IP (which mirrors the schema)
	//   country_code       IP2Location's MMDB editions
	//
	// Only the country is ever decoded, whichever vendor supplied the file. Decoding the
	// whole record would pull city, subdivision and lat/long into memory -- DB-IP City Lite
	// and IP2Location DB11 both carry all three -- and those are far more identifying than a
	// country. The narrow path is the safe one as well as the cheap one.
	for _, path := range countryPaths {
		var iso string
		if err := res.DecodePath(&iso, path...); err == nil && iso != "" {
			return iso, true
		}
	}
	return "", false
}

// countryPaths are the record locations known to hold an ISO 3166-1 alpha-2 country code,
// most common first.
//
// Ordered rather than probed in parallel because the first hit wins and MaxMind/DB-IP are the
// overwhelmingly common case; a file that answers on neither path is a vendor this build has
// not been taught, which SupportedSchemas() exists to report.
var countryPaths = [][]any{
	{"country", "iso_code"},
	{"country_code"},
}

func (r *mmdbResolver) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}
