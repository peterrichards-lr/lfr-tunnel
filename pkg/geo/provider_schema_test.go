package geo

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"lfr-tunnel/pkg/geo/geotest"
)

// #1921: the panel depended on one vendor because the decode path named one vendor's schema.
// These cover the parts that need no vendor file; provider_compat_test.go exercises a real
// database when LFT_GEO_TEST_DB points at one.

// FIRING. Country() must actually decode the schema, against a real .mmdb file, on every
// commit (#1993).
//
// What stood here before was TestBothKnownCountrySchemasAreTried: it asserted that
// countryPaths held two particular strings, which is a table checked against a second copy
// of itself. It passed whatever those strings meant, and it did in fact pass for the whole
// life of a path that named a schema no database anyone has measured actually uses. Nothing
// it could assert would have noticed, because it never decoded anything.
func TestCountryDecodesTheNestedSchemaFromARealMMDBFile(t *testing.T) {
	path := geotest.WriteMMDB(t, map[string]any{
		"country": map[string]any{"iso_code": "SE"},
	})

	r, err := OpenResolver(path, ProviderMaxMind)
	if err != nil {
		t.Fatalf("OpenResolver(fixture): %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})

	code, ok := r.Country(netip.MustParseAddr("8.8.8.8"))
	if !ok {
		t.Fatalf("an address the fixture holds a record for resolved to nothing -- " +
			"Country() no longer decodes country.iso_code, which is the schema every vendor " +
			"that can be declared publishes")
	}
	if code != "SE" {
		t.Errorf("Country() = %q, want %q -- the record was found but decoded wrongly", code, "SE")
	}

	// The other half of the same file: an address it holds no record for must resolve to
	// nothing rather than to a guess. That is the Found()-is-false branch of Country(), and
	// it is the branch every private and unrouted address in a self-hosted deployment takes.
	if code, ok := r.Country(netip.MustParseAddr("192.168.1.1")); ok {
		t.Errorf("an address with no record resolved to %q; it must not enter the distribution", code)
	}
}

// BOUNDING. A record carrying ONLY a top-level `country_code` resolves to nothing, on
// purpose (#1993).
//
// countryPaths used to carry `country_code` as well, documented as "verified against real
// databases -- IP2Location's MMDB editions". It was not verified and it is not what
// IP2Location's MMDB edition does: a real IP2LOCATION-LITE-DB11.MMDB decodes through
// `country.iso_code`, reports database_type "GeoLite2-City" and MaxMind's eight languages,
// and is a deliberate drop-in clone. `country_code` is the column name in IP2Location's CSV
// and BIN editions, which is the likeliest origin of the claim.
//
// So this pins a decision rather than a defect. If a vendor whose MMDB really does use that
// key is ever supported -- IPinfo Lite documents exactly this schema, ParseProvider cannot
// name it today, and #2008 tracks it -- this test goes red and is the place to record the
// measurement that put the path back.
func TestATopLevelCountryCodeIsNotDecoded(t *testing.T) {
	path := geotest.WriteMMDB(t, map[string]any{"country_code": "SE"})

	// CONTROL, first: the fixture must be a readable database whose record really does carry
	// the key. Without this, "resolved to nothing" is satisfied by a fixture this test
	// failed to build -- the assertion would hold over an empty file forever.
	db, err := maxminddb.Open(path)
	if err != nil {
		t.Fatalf("CONTROL: the fixture is not a readable database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	res := db.Lookup(netip.MustParseAddr("8.8.8.8"))
	if !res.Found() {
		t.Fatalf("CONTROL: the fixture holds no record for the address under test")
	}
	var raw string
	if err := res.DecodePath(&raw, "country_code"); err != nil || raw != "SE" {
		t.Fatalf("CONTROL: the fixture record does not carry country_code=SE (got %q, %v) -- "+
			"the assertion below would pass against a file with no such key at all", raw, err)
	}

	r, err := OpenResolver(path, ProviderMaxMind)
	if err != nil {
		t.Fatalf("OpenResolver(fixture): %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close resolver: %v", err)
		}
	})
	if code, ok := r.Country(netip.MustParseAddr("8.8.8.8")); ok {
		t.Errorf("a top-level country_code decoded to %q. That schema is back in countryPaths; "+
			"it belongs there only once a real file from a vendor ParseProvider accepts has "+
			"been measured using it, and the measurement belongs in this comment", code)
	}
}

func TestABinFileIsDiagnosedRatherThanReportedAsCorrupt(t *testing.T) {
	// IP2Location's default download is .BIN, their own format. maxminddb reports it as an
	// opaque parse error, and the fix is a different DOWNLOAD -- not a path, not a
	// permission. Nothing else would tell the operator that.
	dir := t.TempDir()
	bin := filepath.Join(dir, "IP2LOCATION-LITE-DB11.BIN")
	if err := os.WriteFile(bin, []byte("not an mmdb"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	_, err := OpenResolver(bin, ProviderMaxMind)
	if err == nil {
		t.Fatal("a .BIN must not open as an mmdb")
	}
	if !strings.Contains(err.Error(), "MMDB edition") {
		t.Errorf("the error must point at the MMDB edition of the same database; got: %v", err)
	}
}

func TestAnUnreadableNonBinFileKeepsThePlainError(t *testing.T) {
	// BOUNDING: the .BIN wording must not be attached to every failure, or a genuinely
	// corrupt MaxMind file would send someone downloading a format they already have.
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.mmdb")
	if err := os.WriteFile(bad, []byte("not an mmdb"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := OpenResolver(bad, ProviderMaxMind)
	if err == nil {
		t.Fatal("a corrupt mmdb must not open")
	}
	if strings.Contains(err.Error(), "MMDB edition") {
		t.Errorf("a non-.BIN failure must not suggest downloading the MMDB edition; got: %v", err)
	}
}

func TestAnAbsentDatabaseIsStillASupportedConfiguration(t *testing.T) {
	// PREMISE for the whole feature: shipping no database is the default and must stay a
	// clean, non-error state whatever providers are supported.
	if _, err := OpenResolver("", ProviderMaxMind); err != ErrUnavailable {
		t.Errorf("an empty path must report ErrUnavailable, got %v", err)
	}
	// A configured-but-missing file stays inside that same non-error state -- errors.Is
	// rather than == because it now also carries WHICH path was missing (#1938). The
	// equality above is kept for the unset case on purpose: an unset path has nothing to
	// add, and bare ErrUnavailable is exactly "the operator configured nothing".
	if _, err := OpenResolver(filepath.Join(t.TempDir(), "nope.mmdb"), ProviderMaxMind); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a missing file must still match ErrUnavailable, got %v", err)
	}
}
