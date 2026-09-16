package geo

import (
	"net/netip"
	"os"
	"testing"
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

	r, err := OpenResolver(path)
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
