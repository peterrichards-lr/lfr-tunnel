package geo

import (
	"errors"
	"strings"
	"testing"
)

// The vendor is declared, not derived (#1964).
//
// Derivation was measured against a real IP2Location LITE MMDB and does not work: the file
// reports database_type="GeoLite2-City", description="GeoLite2City database", MaxMind's eight
// languages and MaxMind's nested country.iso_code record schema. It is a deliberate drop-in
// clone, so ProviderFromDatabaseType resolved it to maxmind and the panel rendered MaxMind's
// credit over IP2Location's data -- a false statement about provenance, with IP2Location's own
// required acknowledgment unshown.
//
// These cases pin the replacement. The ones that must FAIL are the point: an empty or unknown
// declaration has to keep the feature off rather than fall back to a guess.

func TestEachSupportedVendorCanBeDeclared(t *testing.T) {
	// CONTROL. Without this, every rejection case below would pass on a parser that rejects
	// everything -- which would disable the feature for all three vendors.
	for _, tc := range []struct {
		in   string
		want Provider
	}{
		{"maxmind", ProviderMaxMind},
		{"dbip", ProviderDBIP},
		{"ip2location", ProviderIP2Location},
		// Case and surrounding space are an operator's config file, not a protocol.
		{"  MaxMind  ", ProviderMaxMind},
		{"IP2Location", ProviderIP2Location},
	} {
		got, err := ParseProvider(tc.in)
		if err != nil {
			t.Errorf("CONTROL: %q was rejected (%v); the rejection cases below prove nothing", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

// FIRING. An unset value must not mean "work it out" -- that is the defect.
func TestAnUndeclaredVendorIsRefusedRatherThanGuessed(t *testing.T) {
	got, err := ParseProvider("")
	if !errors.Is(err, ErrProviderNotDeclared) {
		t.Fatalf("an unset country_db_provider returned (%q, %v); it must report ErrProviderNotDeclared", got, err)
	}
	if got != ProviderUnknown {
		t.Errorf("an unset value resolved to %q; it must not name a vendor", got)
	}
}

// FIRING. A typo must be refused loudly, not silently accepted and left to render nothing.
func TestAMisspelledVendorIsRefusedAndQuotedBack(t *testing.T) {
	got, err := ParseProvider("maxmnid")
	if !errors.Is(err, ErrProviderUnknown) {
		t.Fatalf("a misspelled vendor returned (%q, %v); it must report ErrProviderUnknown", got, err)
	}
	if got != ProviderUnknown {
		t.Errorf("a misspelled vendor resolved to %q", got)
	}
	// The operator has to see what they actually typed, or they will read the message and
	// look straight past their own typo.
	if msg := err.Error(); !strings.Contains(msg, "maxmnid") {
		t.Errorf("the error does not quote the value that was set: %q", msg)
	}
}

// The value that started this: a file that DERIVES to the wrong vendor must still be
// attributed to the declared one. This is the whole point of the change.
func TestADeclaredVendorWinsOverWhatTheFileClaims(t *testing.T) {
	// IP2Location's real MMDB metadata, measured.
	const ip2locationRealMetadata = "GeoLite2-City"

	if derived := ProviderFromDatabaseType(ip2locationRealMetadata); derived != ProviderMaxMind {
		t.Fatalf("PREMISE: %q no longer derives to maxmind (got %q) -- if the file stopped "+
			"impersonating MaxMind this test is measuring something else",
			ip2locationRealMetadata, derived)
	}

	declared, err := ParseProvider("ip2location")
	if err != nil {
		t.Fatalf("declaring ip2location failed: %v", err)
	}
	if declared == ProviderFromDatabaseType(ip2locationRealMetadata) {
		t.Fatal("the declared vendor equals the derived one, so this case cannot detect the defect")
	}
	if declared != ProviderIP2Location {
		t.Errorf("declared vendor is %q, want %q -- an IP2Location deployment would credit the "+
			"wrong vendor and leave its own licence unmet", declared, ProviderIP2Location)
	}
}
