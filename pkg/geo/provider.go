package geo

import (
	"errors"
	"fmt"
	"strings"
)

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
	// ProviderIPinfo is an IPinfo Lite database (#2008).
	//
	// The only supported vendor whose record does NOT nest the code under `country`: it puts a
	// top-level `country_code`, and a top-level `country` holding the country's NAME. Measured
	// against ipinfo_lite.mmdb -- `country_code = "US"`, `country = "United States"` -- which is
	// why countryPaths carries both shapes and why the name field is not one of them.
	ProviderIPinfo Provider = "ipinfo"
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
	// Measured: "ipinfo bundle_location_lite.mmdb". Unlike IP2Location -- whose MMDB reports
	// MaxMind's own "GeoLite2-City" and is indistinguishable from it -- IPinfo names itself, so
	// a mismatch warning against a declared vendor is meaningful here.
	case strings.Contains(t, "ipinfo"):
		return ProviderIPinfo
	case strings.HasPrefix(t, "geolite"), strings.HasPrefix(t, "geoip"):
		return ProviderMaxMind
	default:
		return ProviderUnknown
	}
}

// selectableProviders is the vocabulary an operator may declare, in the order a portal offers
// it (#1995).
//
// ONE list, because there were about to be several. The dropdown in System Settings, the
// validation the settings endpoint applies to what it is sent, and ParseProvider's own switch
// are three views of the same question -- "which vendors does this build support?" -- and the
// failure mode of letting them drift is the one this whole area exists to prevent: an option
// offered but refused on save, or accepted on save and never credited. #1882 is the same
// lesson one surface over, where the portal's copy of the alert list left three alerts with no
// control at all.
//
// ProviderUnknown is deliberately absent. It is a RESULT ("we could not tell"), never a
// choice: offering it would let an operator declare that they do not know who published their
// data, which is exactly the state the panel refuses to render rows in.
var selectableProviders = []Provider{ProviderMaxMind, ProviderDBIP, ProviderIP2Location, ProviderIPinfo}

// SelectableProviders returns the vendors an operator may declare, in portal order.
//
// A copy, so a caller that sorts or appends to the result cannot change what the next caller
// is offered.
func SelectableProviders() []Provider {
	out := make([]Provider, len(selectableProviders))
	copy(out, selectableProviders)
	return out
}

// ParseProvider turns a declared config value into a Provider, rejecting anything it does not
// recognise (#1964).
//
// Strict on purpose. The alternative -- accepting an unknown string and rendering no credit --
// would let a typo disable attribution silently, which is the failure this whole change exists
// to remove. An operator who names a vendor wrongly is told so, at startup for a YAML value
// and at the point of saving for a portal one.
//
// Resolved against selectableProviders rather than a switch of its own (#1995): a vendor this
// accepts but the dropdown does not offer is unreachable from the portal, and a vendor the
// dropdown offers but this rejects is an option that 400s on save. Both are silent until
// somebody tries it.
func ParseProvider(declared string) (Provider, error) {
	normalised := Provider(strings.ToLower(strings.TrimSpace(declared)))
	if normalised == "" {
		return ProviderUnknown, ErrProviderNotDeclared
	}
	for _, p := range selectableProviders {
		if normalised == p {
			return p, nil
		}
	}
	names := make([]string, 0, len(selectableProviders))
	for _, p := range selectableProviders {
		names = append(names, string(p))
	}
	return ProviderUnknown, fmt.Errorf("%w: %q is not one of %s",
		ErrProviderUnknown, declared, strings.Join(names, ", "))
}

// ErrProviderNotDeclared is an unset country_db_provider: the feature stays off, and this is
// an operator situation to report rather than a fault.
var ErrProviderNotDeclared = errors.New("geo: country_db_provider is not set")

// ErrProviderUnknown is a declared vendor this build does not know.
var ErrProviderUnknown = errors.New("geo: unknown country_db_provider")

// AttributionLink is the anchor a vendor's credit must carry: where it points and what it says.
//
// ONE table, served to the portals rather than copied into them (#2044). The href and text used
// to live in ui/src/components/GeoAttribution.tsx and pkg/server/static/dashboard.js, which is
// two copies of a licence obligation in two languages -- and the credit now has to appear on the
// login screen too, which would have made three. A vendor added to one and not the others would
// render no anchor at all in the places that were missed, and an anchor is the whole of what
// DB-IP and IPinfo require.
//
// The TEXT is deliberately not translated and does not live in the locale bundles. It is quoted
// from what each vendor publishes -- DB-IP's own snippet, IP2Location's LICENSE_LITE.TXT, the
// wording IPinfo requires -- and a translator is not free to improve a licensor's words. The
// sentence AROUND it is translated; the bundles hold that, with {0} marking where this goes.
func AttributionLink(p Provider) (href, text string, ok bool) {
	switch p {
	case ProviderMaxMind:
		return "https://www.maxmind.com", "maxmind.com", true
	case ProviderDBIP:
		// DB-IP publish this exact anchor: "You may do it by pasting the HTML code snippet
		// below into your code: <a href='https://db-ip.com'>IP Geolocation by DB-IP</a>".
		return "https://db-ip.com", "IP Geolocation by DB-IP", true
	case ProviderIP2Location:
		// LICENSE_LITE.TXT prescribes the sentence word for word, with the link on the phrase
		// "IP geolocation".
		return "https://lite.ip2location.com", "IP geolocation", true
	case ProviderIPinfo:
		return "https://ipinfo.io", "IPinfo", true
	default:
		// ProviderUnknown has no anchor ON PURPOSE. There is nobody to link to, and naming a
		// vendor anyway would be a false provenance claim AND would leave the real supplier's
		// licence unmet. The panel says it could not identify the vendor instead.
		return "", "", false
	}
}
