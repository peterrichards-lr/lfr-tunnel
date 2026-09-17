package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/geo"
)

// A database with no declared vendor keeps the feature OFF (#1964).
//
// Not a cosmetic gate. Every supported vendor's licence obliges a DIFFERENT visible credit, and
// an IP2Location MMDB is indistinguishable from MaxMind's -- measured against a real file:
// database_type "GeoLite2-City", description "GeoLite2City database", MaxMind's eight languages
// and MaxMind's nested country.iso_code record schema. Deriving the vendor therefore printed
// MaxMind's credit over IP2Location's data, leaving IP2Location's own required acknowledgment
// unshown. Refusing to run is the only outcome that is neither a guess nor a licence breach.
//
// Driven through NewServer rather than newGeoAggregator directly, like geoServerWithPath: what
// the panel reports has to be what that configuration actually produces in production.

// geoServerWithVendor builds a server with both the path and the declared vendor set.
func geoServerWithVendor(t *testing.T, path, provider string) *Server {
	t.Helper()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(t.TempDir(), "geo_vendor.db")
	cfg.DisableBackupScheduler = true
	cfg.CountryDBPath = path
	cfg.CountryDBProvider = provider

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer refused to start with country_db_path=%q provider=%q: %v", path, provider, err)
	}
	t.Cleanup(func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite TempDir cleanup races
	})
	return srv
}

// aReadableDatabase is a real .mmdb, or the test skips.
//
// It has to be real. The vendor gate runs AFTER the file checks -- deliberately, so an unset or
// mistyped path keeps its own more specific reason (#1938) instead of being told to set a second
// key that will not help. A fake file therefore never reaches the gate: it fails as unreadable
// first, and a test built on one would assert against a state the gateway cannot reach.
//
// No database ships with this repo and none can (every vendor forbids redistribution), so these
// skip without one. The vendor DECISION itself is covered unconditionally by ParseProvider's
// tests in pkg/geo; what needs a real file is the wiring and the precedence.
//
//	LFT_GEO_TEST_DB=/path/to/country.mmdb make test PKG=./pkg/server/
func aReadableDatabase(t *testing.T) string {
	t.Helper()
	path := os.Getenv("LFT_GEO_TEST_DB")
	if path == "" {
		t.Skip("set LFT_GEO_TEST_DB to a real .mmdb to exercise the vendor gate against one")
	}
	return path
}

// Where this gate lives moved in #1995, and the protection did not.
//
// It used to refuse to OPEN the file, so this asserted srv.geo == nil. The vendor is now
// settable from System Settings without a restart, so refusing to open a perfectly readable
// file over a display value was the wrong lever: the database opens, the aggregator counts,
// and the PANEL withholds both the credit and the rows until somebody names the publisher.
// The licence protection is unchanged -- what changed is where it is applied.
func TestAConfiguredDatabaseWithNoDeclaredVendorStaysOff(t *testing.T) {
	path := aReadableDatabase(t)
	srv := geoServerWithVendor(t, path, "")

	if srv.geo == nil {
		t.Fatal("a readable database was not opened because no vendor was declared -- since " +
			"#1995 the vendor decides attribution, not whether the file can be read")
	}
	_, _, err := srv.effectiveGeoProvider()
	if !errors.Is(err, geo.ErrProviderNotDeclared) {
		t.Fatalf("the vendor in force resolved with %v, want ErrProviderNotDeclared -- the panel "+
			"would publish rows with no credit under them", err)
	}
	available, _, diagnosis := srv.geoPanelState()
	if !available {
		t.Error("the panel reports no open database, so it cannot tell the operator the file was found")
	}
	if diagnosis.Path != path {
		t.Errorf("diagnosis path %q, want %q", diagnosis.Path, path)
	}
}

// A typo and an unset value still need different remedies, and still get different answers --
// now from the vendor in force rather than from the startup diagnosis (see above).
func TestAMisspelledVendorIsReportedSeparatelyFromAnUnsetOne(t *testing.T) {
	srv := geoServerWithVendor(t, aReadableDatabase(t), "maxmnid")

	if srv.geo == nil {
		t.Fatal("a readable database was not opened because its declared vendor was misspelled")
	}
	_, _, err := srv.effectiveGeoProvider()
	if !errors.Is(err, geo.ErrProviderUnknown) {
		t.Fatalf("the vendor in force resolved with %v, want ErrProviderUnknown -- a typo and an "+
			"unset value send the operator to different places", err)
	}
	// The operator has to see what they actually typed or they will read the message and look
	// straight past their own typo.
	if !strings.Contains(err.Error(), "maxmnid") {
		t.Errorf("the error does not quote what was set: %v", err)
	}
}

// CONTROL. Without this, both cases above would pass on a gate that refuses everything --
// which would disable the feature for every correctly configured deployment.
func TestADeclaredVendorGetsPastTheVendorGate(t *testing.T) {
	srv := geoServerWithVendor(t, aReadableDatabase(t), "maxmind")

	got := srv.geoDiagnosis.Reason
	if got == geoReasonProviderNotDeclared || got == geoReasonProviderUnknown {
		t.Fatalf("CONTROL: a correctly declared vendor was refused by the vendor gate (reason %q); "+
			"the cases above prove nothing", got)
	}
	// A real database with a correctly declared vendor is simply ON: no diagnosis at all.
	if got != "" {
		t.Errorf("a correctly configured database reported reason %q", got)
	}
	if srv.geo == nil {
		t.Error("a real database with a declared vendor did not enable geographic distribution")
	}
}
