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
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// ErrUnavailable reports that no geo-IP database is configured or readable, which is a
// normal state rather than a failure: the deployment simply has no database file. No vendor
// permits one to ship with the server, so absence is the default (#1921).
var ErrUnavailable = errors.New("geo: no geo-IP database available")

// ErrNotFound reports that a path WAS configured and nothing exists at it (#1938).
//
// It wraps ErrUnavailable deliberately: every caller that treats "no database" as a normal,
// non-fatal state keeps matching, so the absent-is-a-supported-configuration property this
// package is built on is untouched. What it adds is the one bit OpenResolver used to throw
// away -- whether the operator configured a path at all -- which is why a mistyped path and
// an unset one rendered the same sentence in the admin panel and only the journal could
// tell them apart.
var ErrNotFound = fmt.Errorf("geo: configured database file does not exist: %w", ErrUnavailable)

// Resolver maps an IP address to an ISO 3166-1 alpha-2 country code.
type Resolver interface {
	// Country returns the country code for ip. The second result is false when the
	// address is not in the database at all -- private ranges, CGNAT and unallocated
	// space all land here.
	Country(ip netip.Addr) (string, bool)
	// Provider names the vendor whose data this resolver is serving, so the panel can
	// render the credit that vendor's licence requires (#1921).
	//
	// On the interface rather than on the concrete type: every supported vendor obliges a
	// visible attribution, so a Resolver that cannot say whose data it is serving is a
	// Resolver whose data cannot lawfully be displayed. A future implementation has to
	// answer this, and the compiler is the only thing that reliably asks.
	Provider() Provider
	Close() error
}

// mmdbResolver reads any database in MaxMind's .mmdb FORMAT -- MaxMind's own GeoLite2 and
// GeoIP2, DB-IP's Lite files, and IP2Location's MMDB editions all qualify (#1921). The
// format is one thing; the record schema inside it is another, which is what countryPaths
// below is for.
//
// A local file rather than a lookup API because the whole premise of this feature is that
// the IP is resolved in memory and discarded; posting every user's IP to a third party to
// arrive at an anonymous aggregate would invert that.
type mmdbResolver struct {
	db *maxminddb.Reader
	// provider is resolved once at open time from the file's own metadata. Read-only
	// afterwards, so Provider() needs no lock alongside the concurrent Lookup path.
	provider Provider
}

// OpenResolver opens the .mmdb country database at path, whichever vendor published it.
//
// An empty path, or a path that does not exist, returns an error matching ErrUnavailable
// rather than a hard one: not deploying a database file is a supported configuration.
//
// The two are not the same error, though (#1938). An unset path is bare ErrUnavailable; a
// configured path with no file at it also matches ErrNotFound and names the path, because
// the second is an operator mistake and the first is the default. Callers that only care
// whether the feature is on keep testing ErrUnavailable and see no change.
// The vendor is DECLARED, not derived (#1964). ParseProvider rejects an empty or unknown
// value, and the caller keeps the feature off rather than guessing -- see CountryDBProvider
// in pkg/config for why deriving it cannot work.
func OpenResolver(path string, declared Provider) (Resolver, error) {
	if path == "" {
		return nil, ErrUnavailable
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
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
	// The declared vendor wins. The derived one is kept only to notice a disagreement,
	// which is the cheapest way to catch a typo'd declaration -- and it cannot be an error,
	// because the one case that provoked this change is a file that legitimately derives to
	// the wrong vendor (IP2Location's MMDB reports MaxMind's own database_type).
	if derived := ProviderFromDatabaseType(db.Metadata.DatabaseType); derived != declared {
		slog.Warn("[Geo] The declared vendor does not match the database's own metadata. "+
			"The declared value is used for attribution. If it is wrong, the panel will "+
			"credit the wrong vendor and that vendor's licence will be unmet.",
			"declared", declared, "database_type", db.Metadata.DatabaseType, "derived", derived)
	}
	return &mmdbResolver{db: db, provider: declared}, nil
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

// Provider reports the vendor derived from the database's own metadata.
//
// A nil receiver or a closed database yields ProviderUnknown rather than a named vendor:
// "we do not know" must never round to somebody's trademark.
func (r *mmdbResolver) Provider() Provider {
	if r == nil || r.provider == "" {
		return ProviderUnknown
	}
	return r.provider
}

func (r *mmdbResolver) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}
