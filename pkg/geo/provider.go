package geo

import "strings"

// Provider names the vendor that published an open database (#1921).
//
// It exists for one reason: **every** supported vendor's licence obliges a visible credit,
// and each obliges a DIFFERENT one, so the panel cannot carry a single hardcoded sentence.
// Verified 2026-09-16, one licence at a time rather than by analogy:
//
//   - DB-IP Lite -- Creative Commons Attribution 4.0 International. Their download page:
//     "In the case of a web application, you must include a link back to DB-IP.com on pages
//     that display or use results from the database."
//   - IP2Location LITE -- LICENSE_LITE.TXT, shipped inside the download and read directly,
//     prescribes the acknowledgment word for word: "[Your site name or product name] uses
//     the IP2Location LITE database for <a href="https://lite.ip2location.com">IP
//     geolocation</a>."
//   - MaxMind GeoLite2 -- the odd one out, but NOT in the direction this repo assumed. The
//     GeoLite End User Licence Agreement (effective 2026-02-12) §3 reads: "to the extent the
//     Services contain any copyrightable elements those copyrightable elements are governed
//     by the Creative Commons License. You must provide attribution of your use to MaxMind
//     (an example of attribution: 'This product includes GeoLite Data created by MaxMind,
//     available from https://www.maxmind.com.')". #1921's own comment and
//     docs/server/setup_guide.md §8.11.4 both said GeoLite2 needed no credit line; reading
//     the EULA rather than repeating that is what found otherwise.
//
// So "which vendor" is a question the panel has to be able to answer before it renders a
// single row -- which is why it is derived from the file itself (see ProviderFromDatabaseType)
// rather than added as a config key an operator can set wrongly and never be told about.
type Provider string

const (
	// ProviderMaxMind is a MaxMind GeoLite2 or GeoIP2 database.
	ProviderMaxMind Provider = "maxmind"
	// ProviderDBIP is a DB-IP database, Lite or commercial.
	ProviderDBIP Provider = "dbip"
	// ProviderIP2Location is an IP2Location MMDB-edition database.
	ProviderIP2Location Provider = "ip2location"
	// ProviderUnknown is a readable mmdb whose metadata names a vendor this build has not
	// been taught.
	//
	// It is a first-class result, not a failure and not a default. Falling back to any
	// named vendor's text here would print a credit for data that vendor did not supply --
	// which is a false statement about provenance AND leaves the actual supplier's licence
	// unmet. The panel says plainly that it could not identify the vendor instead.
	ProviderUnknown Provider = "unknown"
)

// ProviderFromDatabaseType derives the vendor from the mmdb metadata's `database_type`
// field, which every MaxMind-format database carries.
//
// Derived rather than configured, deliberately. A `geo_provider:` key would be a second
// thing to keep in step with the file on disk, and the failure mode of getting it wrong is
// silent: the panel would render one vendor's credit over another vendor's data, with
// nothing anywhere to contradict it. The file already knows what it is.
//
// Measured values, not guessed:
//
//	DBIP-City-Lite     dbip-city-lite-2026-09.mmdb (see TestARealDatabaseIdentifiesItsVendor)
//	GeoLite2-Country   MaxMind's own naming; "Names starting with GeoIP are reserved for
//	                   MaxMind databases" per the MaxMind DB specification
//
// IP2Location's MMDB `database_type` has NOT been verified against a real file -- the copy
// available here is the `.BIN` edition, which this reader cannot open at all. The substring
// match below is the best available guess; if it is wrong the result is ProviderUnknown,
// which is honest, rather than a wrong vendor's credit.
func ProviderFromDatabaseType(databaseType string) Provider {
	t := strings.ToLower(strings.TrimSpace(databaseType))
	switch {
	case t == "":
		return ProviderUnknown
	case strings.HasPrefix(t, "dbip"):
		return ProviderDBIP
	case strings.Contains(t, "ip2location"):
		return ProviderIP2Location
	case strings.HasPrefix(t, "geolite"), strings.HasPrefix(t, "geoip"):
		return ProviderMaxMind
	default:
		return ProviderUnknown
	}
}
