package geo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1921: the panel depended on one vendor because the decode path named one vendor's schema.
// These cover the parts that need no vendor file; provider_compat_test.go exercises a real
// database when LFT_GEO_TEST_DB points at one.

func TestBothKnownCountrySchemasAreTried(t *testing.T) {
	// The vocabulary itself. MaxMind and DB-IP put the code at country.iso_code;
	// IP2Location's MMDB editions put it at country_code. A build that knows only one is a
	// build tied to one vendor's licence, which is the defect.
	want := map[string]bool{"country.iso_code": true, "country_code": true}
	got := map[string]bool{}
	for _, p := range countryPaths {
		parts := make([]string, 0, len(p))
		for _, seg := range p {
			s, ok := seg.(string)
			if !ok {
				t.Fatalf("countryPaths must hold string segments, got %T", seg)
			}
			parts = append(parts, s)
		}
		got[strings.Join(parts, ".")] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("no decode path for %q -- databases using that schema resolve to nothing", w)
		}
	}
	if len(countryPaths) == 0 {
		t.Fatal("no country paths declared at all -- every lookup would fail silently")
	}
	// Order matters for cost, not correctness: the common vendors must be tried first.
	if first, _ := countryPaths[0][0].(string); first != "country" {
		t.Errorf("the MaxMind/DB-IP schema should be tried first, got %q", first)
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

	_, err := OpenResolver(bin)
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
	_, err := OpenResolver(bad)
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
	if _, err := OpenResolver(""); err != ErrUnavailable {
		t.Errorf("an empty path must report ErrUnavailable, got %v", err)
	}
	if _, err := OpenResolver(filepath.Join(t.TempDir(), "nope.mmdb")); err != ErrUnavailable {
		t.Errorf("a missing file must report ErrUnavailable, got %v", err)
	}
}
