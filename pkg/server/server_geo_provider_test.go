package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
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

func TestAConfiguredDatabaseWithNoDeclaredVendorStaysOff(t *testing.T) {
	path := aReadableDatabase(t)
	srv := geoServerWithVendor(t, path, "")

	if srv.geo != nil {
		t.Fatal("geographic distribution started without a declared vendor, so the panel would " +
			"credit whichever vendor the file claims to be")
	}
	if srv.geoDiagnosis.Reason != geoReasonProviderNotDeclared {
		t.Fatalf("reason %q, want %q -- telling an operator who configured a database that they "+
			"configured nothing sends them to check the path",
			srv.geoDiagnosis.Reason, geoReasonProviderNotDeclared)
	}
	if srv.geoDiagnosis.Path != path {
		t.Errorf("diagnosis path %q, want %q", srv.geoDiagnosis.Path, path)
	}
}

func TestAMisspelledVendorIsReportedSeparatelyFromAnUnsetOne(t *testing.T) {
	srv := geoServerWithVendor(t, aReadableDatabase(t), "maxmnid")

	if srv.geo != nil {
		t.Fatal("geographic distribution started with an unrecognised vendor")
	}
	if srv.geoDiagnosis.Reason != geoReasonProviderUnknown {
		t.Fatalf("reason %q, want %q -- a typo and an unset value need different remedies",
			srv.geoDiagnosis.Reason, geoReasonProviderUnknown)
	}
	// The operator has to see what they actually typed or they will read the message and look
	// straight past their own typo.
	if !strings.Contains(srv.geoDiagnosis.Detail, "maxmnid") {
		t.Errorf("the detail does not quote what was set: %q", srv.geoDiagnosis.Detail)
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
