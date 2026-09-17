package geo

import (
	"net/netip"
	"os"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// Does a real database from a given vendor actually resolve? (#1921)
//
// Skipped unless LFT_GEO_TEST_DB names one, because no database ships with this repo and none
// ever will -- every vendor's licence forbids redistribution, which is the whole reason this
// issue exists. Run it against a file you downloaded:
//
//	LFT_GEO_TEST_DB=/path/to/provider.mmdb make test PKG=./pkg/geo/
//
// This is the only way to tell "we support this vendor" from "we believe we support this
// vendor": the formats differ, and so do the record schemas inside the same format.
func TestARealDatabaseResolvesKnownAddresses(t *testing.T) {
	path := os.Getenv("LFT_GEO_TEST_DB")
	if path == "" {
		t.Skip("set LFT_GEO_TEST_DB to a .mmdb file to exercise a real vendor database")
	}

	r, err := OpenResolver(path, ProviderMaxMind)
	if err != nil {
		t.Fatalf("OpenResolver(%s): %v", path, err)
	}
	// Handled rather than discarded: Resolver.Close is not on the errcheck exclusion list,
	// and a close that fails on a database this test just read successfully is worth knowing
	// about rather than swallowing.
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})

	// Public addresses with long-stable, well-known registrations. Asserting only that a
	// plausible ISO code comes back, not WHICH one: vendors disagree at the margins and a
	// test that pins a country would fail on a data refresh rather than on a defect.
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "2606:4700:4700::1111"} {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			t.Fatalf("parse %s: %v", ip, err)
		}
		code, ok := r.Country(addr)
		if !ok {
			t.Errorf("%s resolved to nothing -- the schema this vendor uses is probably not "+
				"the one Country() decodes", ip)
			continue
		}
		if len(code) != 2 {
			t.Errorf("%s gave %q, want a 2-letter ISO 3166-1 alpha-2 code", ip, code)
		}
		t.Logf("  %-22s -> %s", ip, code)
	}

	// A private address must resolve to nothing rather than to a guess: these are the
	// addresses a self-hosted deployment sees most, and inventing a country for them would
	// quietly corrupt the distribution.
	priv := netip.MustParseAddr("192.168.1.1")
	if code, ok := r.Country(priv); ok {
		t.Errorf("a private address resolved to %q; it must not appear in the distribution", code)
	}
}

// Does the vendor derivation work on a real file? (#1921)
//
// Everything else about attribution is a table of strings checked against another table of
// strings. This is the only test that reads `database_type` out of a database somebody
// actually downloaded, which is where the value comes from in production.
//
// Skipped without LFT_GEO_TEST_DB, same as above and for the same reason: no database ships
// with this repo and none ever can.
//
//	LFT_GEO_TEST_DB=/path/to/provider.mmdb make test PKG=./pkg/geo/
func TestARealDatabaseIdentifiesItsVendor(t *testing.T) {
	path := os.Getenv("LFT_GEO_TEST_DB")
	if path == "" {
		t.Skip("set LFT_GEO_TEST_DB to a .mmdb file to check the vendor is derived from a real file")
	}

	db, err := maxminddb.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	dbType := db.Metadata.DatabaseType
	provider := ProviderFromDatabaseType(dbType)
	t.Logf("  database_type=%q -> provider=%q", dbType, provider)

	// The whole metadata block, because `database_type` turned out NOT to be a reliable
	// discriminator: IP2Location's MMDB edition reports "GeoLite2-City", MaxMind's own string
	// (#1964). Anyone verifying a new vendor needs to see what else is there, and finding out
	// should not require editing this test.
	t.Logf("  description=%v", db.Metadata.Description)
	t.Logf("  ip_version=%d  languages=%v", db.Metadata.IPVersion, db.Metadata.Languages)
	t.Logf("  node_count=%d  record_size=%d  build_epoch=%d",
		db.Metadata.NodeCount, db.Metadata.RecordSize, db.Metadata.BuildEpoch)

	// Asserting only that SOMETHING was recognised, not which vendor: this runs against
	// whatever file the person running it has, and pinning a vendor would fail on a
	// different (perfectly valid) download rather than on a defect. ProviderUnknown is the
	// failure worth catching -- it means the panel would render no vendor's credit for a
	// file that certainly has one.
	if provider == ProviderUnknown {
		t.Errorf("a real database reported database_type=%q and was not recognised -- the panel "+
			"would show no attribution for data whose licence requires one", dbType)
	}

	// The DECLARED vendor must reach callers through the Resolver, which is the path the
	// server actually uses (#1964). Declared, not derived: since #1990 the operator names the
	// vendor and the file's own claim is advisory, because an IP2Location MMDB reports
	// MaxMind's database_type and deriving it credited the wrong vendor.
	//
	// This previously passed ProviderMaxMind here -- a mechanical edit when OpenResolver took
	// its second argument -- and then compared against the DERIVED value. That fails on any
	// file that is not MaxMind's, including the DB-IP country edition this repo's own setup
	// guide recommends (#2011). It tested nothing about wiring and everything about the
	// fixture's luck.
	//
	// Declaring what the file actually is, so the case works against whatever download the
	// person running it has.
	r, err := OpenResolver(path, provider)
	if err != nil {
		t.Fatalf("OpenResolver(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close resolver %s: %v", path, err)
		}
	})
	if got := r.Provider(); got != provider {
		t.Errorf("Resolver.Provider() = %q, but %q was declared -- the declared vendor is not "+
			"reaching callers, so the panel would credit someone else", got, provider)
	}

	// And the whole point of #1990: when the declaration DISAGREES with the file, the
	// declaration wins. Without this the case above passes on a resolver that ignores its
	// argument and derives internally -- which is precisely the behaviour being replaced.
	misdeclared := ProviderIP2Location
	if provider == ProviderIP2Location {
		misdeclared = ProviderDBIP
	}
	r2, err := OpenResolver(path, misdeclared)
	if err != nil {
		t.Fatalf("OpenResolver(%s, %q): %v", path, misdeclared, err)
	}
	t.Cleanup(func() {
		if err := r2.Close(); err != nil {
			t.Errorf("close resolver %s: %v", path, err)
		}
	})
	if got := r2.Provider(); got != misdeclared {
		t.Errorf("declared %q but Resolver.Provider() = %q -- the file's own claim is "+
			"overriding the operator's declaration, which is the defect #1964 fixed",
			misdeclared, got)
	}
}
