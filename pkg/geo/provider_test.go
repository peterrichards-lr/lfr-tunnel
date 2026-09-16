package geo

import "testing"

// Which vendor supplied the file? (#1921)
//
// Not a cosmetic question: every supported vendor's licence obliges a visible credit and each
// obliges a DIFFERENT one, so getting this wrong prints one company's acknowledgment over
// another company's data -- a false provenance claim, with the real supplier's licence still
// unmet. It is derived from the mmdb's own `database_type` rather than configured, so the
// derivation is the whole guarantee.
func TestTheVendorIsDerivedFromTheDatabaseType(t *testing.T) {
	// Every value here is one a real file reports, or one a vendor documents. A fixture
	// naming a string no database emits would test the switch statement against itself.
	cases := []struct {
		databaseType string
		want         Provider
		why          string
	}{
		// Measured: dbip-city-lite-2026-09.mmdb reports exactly this. See
		// TestARealDatabaseIdentifiesItsVendor, which reads it out of a real file.
		{"DBIP-City-Lite", ProviderDBIP, "the DB-IP file the owner deployed to production"},
		{"DBIP-Country-Lite", ProviderDBIP, "the country edition of the same free download"},
		// MaxMind DB specification: "Names starting with GeoIP are reserved for MaxMind
		// databases", and GeoLite2 is what a free MaxMind download is called.
		{"GeoLite2-Country", ProviderMaxMind, "the free MaxMind download this feature shipped with"},
		{"GeoLite2-City", ProviderMaxMind, "the city edition of the same"},
		{"GeoIP2-Country", ProviderMaxMind, "the paid product"},
		// Unverified against a real file -- the IP2Location copy available here is the .BIN
		// edition, which this reader cannot open at all. Stated in provider.go too, so the
		// guess is visible rather than implied.
		{"IP2LOCATION-LITE-DB1", ProviderIP2Location, "IP2Location's MMDB edition (unverified naming)"},
	}
	for _, c := range cases {
		if got := ProviderFromDatabaseType(c.databaseType); got != c.want {
			t.Errorf("ProviderFromDatabaseType(%q) = %q, want %q -- %s", c.databaseType, got, c.want, c.why)
		}
	}
}

// TestAnUnrecognisedVendorIsNotRoundedToOne is the honesty requirement, and the reason this
// is not a two-way "DB-IP or MaxMind" flag.
//
// A default of any named vendor would be wrong in exactly the way that is hardest to notice:
// the panel would render a complete, plausible credit line naming a company that did not
// supply the data, and nothing on the page or in the log would contradict it.
func TestAnUnrecognisedVendorIsNotRoundedToOne(t *testing.T) {
	for _, dbType := range []string{
		"",                          // metadata absent or empty
		"Some-Other-Vendor-Country", // a vendor this build has never seen
		"Custom-Internal-Ranges",    // an mmdb somebody built themselves; mmdbwriter makes this easy
	} {
		if got := ProviderFromDatabaseType(dbType); got != ProviderUnknown {
			t.Errorf("ProviderFromDatabaseType(%q) = %q, want %q -- an unrecognised file must "+
				"not be credited to a vendor that did not supply it", dbType, got, ProviderUnknown)
		}
	}
}

// TestTheDerivationIgnoresCaseAndSurroundingSpace. Vendors are not consistent about case
// (DB-IP writes "DBIP-City-Lite", IP2Location shouts), and the value is read out of a file.
func TestTheDerivationIgnoresCaseAndSurroundingSpace(t *testing.T) {
	if got := ProviderFromDatabaseType("  dbip-country-lite  "); got != ProviderDBIP {
		t.Errorf("lowercase, padded DB-IP type resolved to %q, want %q", got, ProviderDBIP)
	}
	if got := ProviderFromDatabaseType("geolite2-country"); got != ProviderMaxMind {
		t.Errorf("lowercase MaxMind type resolved to %q, want %q", got, ProviderMaxMind)
	}
}

// TestANilResolverNamesNoVendor. Provider() is reached from the API handler, which runs
// whether or not a database opened, so the nil paths have to answer something -- and the
// something must not be a trademark.
func TestANilResolverNamesNoVendor(t *testing.T) {
	var r *mmdbResolver
	if got := r.Provider(); got != ProviderUnknown {
		t.Errorf("a nil resolver reported %q, want %q", got, ProviderUnknown)
	}
	var a *Aggregator
	if got := a.Provider(); got != ProviderUnknown {
		t.Errorf("a nil aggregator -- the no-database-configured state -- reported %q, want %q",
			got, ProviderUnknown)
	}
}
