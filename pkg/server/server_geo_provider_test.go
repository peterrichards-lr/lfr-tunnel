package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/geo"
	"lfr-tunnel/pkg/geo/geotest"
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

// aReadableDatabase is a .mmdb this build genuinely opens: a downloaded one when
// LFT_GEO_TEST_DB names it, and a synthetic one otherwise (#2019).
//
// It has to be READABLE, which is not the same as real. The vendor gate runs AFTER the file
// checks -- deliberately, so an unset or mistyped path keeps its own more specific reason
// (#1938) instead of being told to set a second key that will not help. That is why the comment
// here used to say the file had to be downloaded: a FAKE file never reaches the gate, because it
// fails as unreadable first, and a test built on one would assert against a state the gateway
// cannot reach.
//
// geotest.WriteMMDB is not a fake. It assembles a real MaxMind DB 2.0 file that maxminddb.Open
// -- the same reader geo.OpenResolver uses in production -- genuinely opens, so the file checks
// pass and control reaches the vendor gate exactly as a download does. What it cannot prove is
// that a REAL vendor's record schema decodes; that stays gated on LFT_GEO_TEST_DB in
// pkg/geo/provider_compat_test.go, where a synthetic file would only check the fixture against a
// copy of itself (#1993). Nothing below reads the record: the vendor is DECLARED rather than
// derived (#1964), so whose data is inside is not what any of these three assert.
//
// Without this, all three skipped in CI -- the CONTROL included, and it is the one that stops
// the other two passing on a gate that refuses everything. The gate protecting three vendors'
// licence obligations therefore had no evidence behind it on any commit.
//
// The env var stays an OVERRIDE, not a leftover: a run with a download configured is
// byte-for-byte the run it was before this changed.
//
//	LFT_GEO_TEST_DB=/path/to/country.mmdb make test PKG=./pkg/server/
func aReadableDatabase(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("LFT_GEO_TEST_DB"); path != "" {
		return path
	}
	return geotest.WriteMMDB(t, syntheticCountryRecord())
}

// countSomeRegistrations drives enough registrations through the real path to clear the
// k-anonymity threshold, and flushes them into the store.
//
// Without it, "the panel served no rows" is satisfied by a period in which nothing was ever
// counted -- which is every one of these tests, since none of them register anybody. The
// licence protection is that rows which DO exist are withheld until a vendor is named, and an
// empty panel over an empty store is not evidence of it. TestADeclaredVendorGetsPastTheVendorGate
// makes the same calls and requires rows to come back, so the withholding above is shown to be
// a decision rather than the only thing this code path can produce.
//
// Through observeGeoLocation and the aggregator in force rather than by writing rows directly:
// the question is what the running server counts, and a hand-inserted row would answer it about
// the store instead.
func countSomeRegistrations(t *testing.T, srv *Server) {
	t.Helper()
	for i := 0; i <= geo.DefaultThreshold; i++ {
		srv.observeGeoLocation(fmt.Sprintf("vendor-gate-user-%d@example.com", i), "8.8.8.8")
	}
	srv.withGeoAggregator(func(a *geo.Aggregator) {
		if a == nil {
			t.Fatal("PREMISE: no aggregator is in force, so nothing was counted and every " +
				"assertion about withheld rows below would hold over an empty store")
		}
		if err := a.Flush(); err != nil {
			t.Fatalf("flushing the aggregator in force: %v", err)
		}
	})
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

	// And through the handler, which is where the licence protection is actually applied
	// (#1995). effectiveGeoProvider returning the sentinel is the DECISION; withholding the
	// credit and the rows is the CONSEQUENCE, and only this reaches the branch that maps one
	// to the other. Asserted by value, because "available:false" is shared with every other
	// way the feature can be off -- an absent file included, which is the confusion #1938
	// was filed for.
	countSomeRegistrations(t, srv)
	panel := locationsFor(t, srv)
	if panel.Available {
		t.Error("the panel published an open database with no vendor named, so the rows would " +
			"carry no credit under them")
	}
	if panel.Reason != geoReasonProviderNotDeclared {
		t.Errorf("the panel reports reason %q, want %q -- telling an operator who configured a "+
			"database that they configured nothing sends them to check the path",
			panel.Reason, geoReasonProviderNotDeclared)
	}
	if panel.Provider != "" {
		t.Errorf("the panel credits %q with no vendor declared", panel.Provider)
	}
	if panel.ConfiguredPath != path {
		t.Errorf("the panel names path %q, want %q -- the operator cannot see which file was "+
			"found", panel.ConfiguredPath, path)
	}
	if len(panel.Buckets) != 0 {
		t.Errorf("the panel served %d rows with no credit under them", len(panel.Buckets))
	}
}

// misspeltVendor is `maxmind` with two letters transposed: a value nothing accepts, and one an
// operator can stare straight past in their own YAML.
const misspeltVendor = "maxmnid"

// A typo and an unset value still need different remedies, and still get different answers --
// now from the vendor in force rather than from the startup diagnosis (see above).
func TestAMisspelledVendorIsReportedSeparatelyFromAnUnsetOne(t *testing.T) {
	srv := geoServerWithVendor(t, aReadableDatabase(t), misspeltVendor)

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
	if !strings.Contains(err.Error(), misspeltVendor) {
		t.Errorf("the error does not quote what was set: %v", err)
	}

	// The same distinction, at the panel. A typo and an unset value produce the same
	// `available:false`, so the reason is the only thing that can tell them apart -- and the
	// detail is the only thing that can quote the typo back.
	panel := locationsFor(t, srv)
	if panel.Available {
		t.Error("the panel published rows under a vendor this build does not know")
	}
	if panel.Reason != geoReasonProviderUnknown {
		t.Errorf("the panel reports reason %q, want %q -- a typo and an unset value send the "+
			"operator to different places", panel.Reason, geoReasonProviderUnknown)
	}
	if !strings.Contains(panel.Detail, misspeltVendor) {
		t.Errorf("the panel detail does not quote what was set: %q", panel.Detail)
	}
}

// CONTROL. Without this, both cases above would pass on a gate that refuses everything --
// which would disable the feature for every correctly configured deployment.
//
// It asks the gate the two cases above ask, and it has to: until #2019 it read
// srv.geoDiagnosis.Reason, and #1995 had moved the gate off that field hours earlier. Since
// then newGeoAggregator writes geoDiagnosis{Path: path} for ANY readable file, whatever vendor
// is declared -- so `Reason == ""` held for the unset and the mistyped vendor too, and the
// `CONTROL:` branch below had become unreachable. A control satisfied by something other than
// the thing it controls is not a control; it is the shape §5c names, in the one test whose
// whole job is to stop the other two passing vacuously.
func TestADeclaredVendorGetsPastTheVendorGate(t *testing.T) {
	const declared = string(geo.ProviderMaxMind)
	srv := geoServerWithVendor(t, aReadableDatabase(t), declared)

	if srv.geo == nil {
		t.Fatal("a readable database with a declared vendor did not enable geographic distribution")
	}
	// The same call the two cases above assert the sentinel from. A gate that refused
	// everything would return one here too, and this is the assertion that says so.
	provider, _, err := srv.effectiveGeoProvider()
	if err != nil {
		t.Fatalf("CONTROL: a correctly declared vendor was refused by the vendor gate (%v); the "+
			"cases above prove nothing -- both are satisfied by a gate that admits no vendor at all",
			err)
	}
	if string(provider) != declared {
		t.Errorf("the vendor in force is %q, want %q", provider, declared)
	}
	// And the panel publishes under that credit. This is the half the two cases above assert
	// the absence of, so without it "no rows, no credit" is never once shown to be a decision
	// rather than the only thing this code path can do.
	countSomeRegistrations(t, srv)
	panel := locationsFor(t, srv)
	if !panel.Available {
		t.Fatalf("CONTROL: a correctly declared vendor left the panel off (reason %q, path %q)",
			panel.Reason, panel.ConfiguredPath)
	}
	if panel.Provider != declared {
		t.Errorf("the panel credits %q, want %q", panel.Provider, declared)
	}
	if panel.Reason != "" {
		t.Errorf("a correctly configured database reported reason %q", panel.Reason)
	}
	// The other half of the control, and the one that makes "no rows" above mean something:
	// the SAME registrations, through the SAME calls, do reach the panel once a vendor is
	// named. Without this, the withheld-rows assertion is equally satisfied by a build that
	// serves no rows to anyone.
	if len(panel.Buckets) == 0 {
		t.Error("CONTROL: a correctly declared vendor served no rows either, so the withheld " +
			"rows in the cases above are not evidence of the licence gate")
	}
}
