package server

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/geo"
	"lfr-tunnel/pkg/geo/geotest"
)

// The SIGHUP reload swap, proved on every commit (#2014).
//
// Everything in geo_reload_test.go that needs a database open skips unless LFT_GEO_TEST_DB
// names a file no vendor licence permits redistributing. Measured on the tree before this file
// existed, by deleting `s.geo = candidate` from applyGeoDatabase:
//
//	mutant, no env vars -- what CI does   all 9 Reload tests PASS
//	mutant, two real vendor databases     3 fail
//	unmutated, the same two databases     all pass
//
// So the central assertion of #1998 -- that a reload actually swaps the open database and the
// panel's attribution follows it -- had no evidence behind it in the one place that runs on
// every commit. A guard that reports green on a build where the thing it guards has been
// removed is worse than no guard: it is read as coverage.
//
// #1993 removed the reason for the gate in exactly this case by assembling a real .mmdb in
// memory, and #2014 moved that encoder to pkg/geo/geotest so a test outside pkg/geo can call
// it. What a synthetic file can and cannot prove is set out in that package's doc comment; the
// short version is that the vendor is DECLARED rather than derived (#1964), so it is config
// rather than file content, and two fixtures carrying the same record declared as two different
// vendors exercise the swap and the attribution with neither vendor's data present.
//
// These tests are ADDITIVE. The LFT_GEO_TEST_DB cases stay exactly as they are: a synthetic
// file proves the wiring, and only a downloaded one proves a real vendor's schema decodes,
// which is what pkg/geo's TestARealDatabaseResolvesKnownAddresses exists to answer.

// syntheticCountryRecord is the one record both fixtures carry.
//
// The SAME record in both, on purpose. It is what makes the swap observable only through the
// wiring: if the two files differed in content, a difference seen after a reload could come
// from the data rather than from the database actually having been replaced.
func syntheticCountryRecord() map[string]any {
	return map[string]any{"country": map[string]any{"iso_code": "SE"}}
}

// twoSyntheticDatabasesFromDifferentVendors is twoRealDatabasesFromDifferentVendors' premise,
// built rather than downloaded, so it holds in CI.
//
// Different PATHS because "the open file moved" is only observable between distinct paths, and
// different declared VENDORS because the declaration is what the panel credits (#1964/#1990).
// Both are asserted rather than assumed: geotest.WriteMMDB takes a fresh t.TempDir() per call,
// and a change to that would silently turn every test below into one that proves nothing.
func twoSyntheticDatabasesFromDifferentVendors(t *testing.T) (first, second geoSource) {
	t.Helper()

	first = geoSource{
		Path:     geotest.WriteMMDB(t, syntheticCountryRecord()),
		Provider: string(geo.ProviderDBIP),
	}
	second = geoSource{
		Path:     geotest.WriteMMDB(t, syntheticCountryRecord()),
		Provider: string(geo.ProviderIP2Location),
	}

	if first.Path == second.Path {
		t.Fatalf("PREMISE: both fixtures were written to %s, so no reload below can move the "+
			"open file and every assertion about the swap would hold over a reload that did "+
			"nothing", first.Path)
	}
	if first.Provider == second.Provider {
		t.Fatalf("PREMISE: both fixtures declare vendor %q, so the panel would credit the same "+
			"name before and after and could not tell a working reload from one that did nothing",
			first.Provider)
	}
	return first, second
}

// syntheticServer starts a server serving src, and fails if it did not actually come up
// serving it.
//
// The premise check is not decoration. Every assertion in this file is of the form "this
// changed" or "this did not change", and all of them are satisfied by a server that never had a
// database open at all -- which is precisely the state the env-gated tests were silently in.
func syntheticServer(t *testing.T, src geoSource) *Server {
	t.Helper()

	srv := geoServerWithVendor(t, src.Path, src.Provider)
	before := locationsFor(t, srv)
	if !before.Available {
		t.Fatalf("PREMISE: the synthetic database at %s did not come up serving (reason %q, "+
			"path %q) -- the fixture is not a database this build can open, so nothing below "+
			"measures a reload", src.Path, before.Reason, before.ConfiguredPath)
	}
	if before.Provider != src.Provider {
		t.Fatalf("PREMISE: the panel credits %q before any reload, want %q",
			before.Provider, src.Provider)
	}
	return srv
}

// resolvesAddresses drives the real registration path until the k-threshold clears and reports
// whether the database in force produced a bucket.
//
// Through observeGeoLocation and the real handler rather than by calling Country directly: the
// question is whether the database the SERVER has in force resolves, and a direct call would
// answer it about a resolver this test opened itself.
func resolvesAddresses(t *testing.T, srv *Server) bool {
	t.Helper()

	const threshold = geo.DefaultThreshold
	for i := 0; i <= threshold; i++ {
		srv.observeGeoLocation(fmt.Sprintf("synthetic-user-%d@example.com", i), "8.8.8.8")
	}
	srv.withGeoAggregator(func(a *geo.Aggregator) {
		if err := a.Flush(); err != nil {
			t.Fatalf("flushing the aggregator in force: %v", err)
		}
	})
	return len(locationsFor(t, srv).Buckets) > 0
}

// TestReloadSwapsTheOpenDatabaseWithoutAVendorFile is the control #1998 asked for and #2014
// made runnable: the same assertions as TestReloadSwapsTheDatabaseAndTheAttributionFollowsIt,
// against two databases this test builds.
//
// It is the one that kills the mutation the issue measured. With `s.geo = candidate` deleted,
// the aggregator assertion below goes red and says so in those words -- the swap did not
// happen. Not "a test failed": the other assertions here (the declared vendor, the path in
// force) are written by applyGeoDatabase on lines the mutation leaves alone, so they would
// still pass, and the failure names the one thing that broke.
func TestReloadSwapsTheOpenDatabaseWithoutAVendorFile(t *testing.T) {
	first, second := twoSyntheticDatabasesFromDifferentVendors(t)

	srv := syntheticServer(t, first)
	beforeAggregator := openAggregator(t, srv)

	cfgPath := writeConfig(t, geoConfigYAML(second))
	if err := srv.ReloadGeoDatabase(cfgPath); err != nil {
		t.Fatalf("reloading onto %s: %v", second.Path, err)
	}

	after := locationsFor(t, srv)
	if !after.Available {
		t.Fatalf("the reload turned the panel off (reason %q, path %q)", after.Reason, after.ConfiguredPath)
	}
	if after.Provider != second.Provider {
		t.Errorf("the panel credits %q after reloading onto %s, want %q -- the attribution did "+
			"not follow the file that is actually open", after.Provider, second.Path, second.Provider)
	}
	if after.Provider == first.Provider {
		t.Errorf("the credit is %q before and after: a reload that did nothing at all would look "+
			"identical to this", after.Provider)
	}
	if got := geoSourceOf(t, srv); got.Path != second.Path {
		t.Errorf("the open path is %q, want %q -- the vendor declaration moved without the file, "+
			"which credits the new vendor for the old vendor's data", got.Path, second.Path)
	}
	// THE assertion, and the one the mutation kills. Pointer identity is the only thing that
	// can tell "a new database was opened" from "the recorded config changed and the handle did
	// not" -- and every other field here is written by a line the mutation leaves intact.
	if openAggregator(t, srv) == beforeAggregator {
		t.Error("the same aggregator is in force after the reload, so no new database was " +
			"opened: the config was adopted and the open file was not replaced")
	}
	// And the newly opened file genuinely answers lookups. Without this the test would pass on
	// a reload that recorded the new path and left a dead resolver behind it -- "the fields
	// changed" is satisfied by either half of the swap on its own.
	if !resolvesAddresses(t, srv) {
		t.Error("the database opened by the reload resolved nothing: registrations from a " +
			"well-known public address produced no bucket")
	}
}

// TestReloadWithABadPathKeepsTheSyntheticDatabaseServing.
//
// A reload that breaks a working feature because of a typo is worse than no reload at all --
// the precedent ReloadEdgeNodes set with "keeping the edge nodes already in force". The real
// -database twin of this has never run in CI, so "the running database survives a bad path" was
// asserted only on a developer's laptop with two downloads on it.
func TestReloadWithABadPathKeepsTheSyntheticDatabaseServing(t *testing.T) {
	running, _ := twoSyntheticDatabasesFromDifferentVendors(t)

	srv := syntheticServer(t, running)
	serving := openAggregator(t, srv)

	typo := filepath.Join(t.TempDir(), "geoip", "dbip-country-lite-2026-O9.mmdb")
	cfgPath := writeConfig(t, geoConfigYAML(geoSource{Path: typo, Provider: running.Provider}))

	err := srv.ReloadGeoDatabase(cfgPath)
	if err == nil {
		t.Fatal("a path with no file at it must be reported as a failed reload, not accepted")
	}
	// The cause, not merely "an error": every other reload failure would also be non-nil here.
	if !strings.Contains(err.Error(), "keeping the geo-IP database already in force") {
		t.Errorf("the error must say the running database was kept, got: %v", err)
	}
	if !strings.Contains(err.Error(), string(geoReasonPathNotFound)) {
		t.Errorf("the error must name the reason so the operator knows which key to fix, got: %v", err)
	}

	after := locationsFor(t, srv)
	if !after.Available {
		t.Fatalf("a mistyped path turned a working panel off (reason %q)", after.Reason)
	}
	if after.Provider != running.Provider {
		t.Errorf("the panel credits %q after a failed reload, want %q", after.Provider, running.Provider)
	}
	if openAggregator(t, srv) != serving {
		t.Error("a failed reload replaced the open database")
	}
	if got := geoSourceOf(t, srv); got != running {
		t.Errorf("a failed reload adopted the config it rejected: %+v", got)
	}
}

// TestReloadWithAnIdenticalConfigDoesNotChurnTheSyntheticFile.
//
// SIGHUP is the edge-token withdrawal signal too (#1309), so this path runs every time an
// operator revokes a credential -- and a reopen is not free: closing the aggregator flushes and
// then DISCARDS the current period's in-memory user sets, so an unrelated edit would cost the
// week's distinct-user accuracy. Pointer identity is the only assertion that can tell "left
// alone" from "closed and reopened to the same path".
func TestReloadWithAnIdenticalConfigDoesNotChurnTheSyntheticFile(t *testing.T) {
	running, other := twoSyntheticDatabasesFromDifferentVendors(t)

	srv := syntheticServer(t, running)
	serving := openAggregator(t, srv)

	same := writeConfig(t, geoConfigYAML(running))
	if err := srv.ReloadGeoDatabase(same); err != nil {
		t.Fatalf("an unchanged config must be a successful no-op, got: %v", err)
	}
	if openAggregator(t, srv) != serving {
		t.Error("an unchanged config reopened the database, losing this period's in-memory user sets")
	}

	// CONTROL. Without it the assertion above is satisfied by a reload that never reopens
	// anything -- including the broken one this whole issue exists to replace.
	//
	// A different FILE is the change used here, rather than a redeclared vendor on the same
	// file. Whether relabelling alone should reopen is a live question about attribution
	// (#1995), and a control has to turn on something that is not in dispute: a reload onto a
	// different path must open a different database under any answer to that.
	changed := writeConfig(t, geoConfigYAML(other))
	if err := srv.ReloadGeoDatabase(changed); err != nil {
		t.Fatalf("pointing at a second database is a valid change: %v", err)
	}
	if openAggregator(t, srv) == serving {
		t.Fatalf("CONTROL: a CHANGED file left the same database open, so the no-op assertion " +
			"above proves nothing -- it would hold for a reload that never reopens anything")
	}
	if got := locationsFor(t, srv); got.Provider != other.Provider {
		t.Errorf("CONTROL: the panel credits %q after reloading onto the database declared %q",
			got.Provider, other.Provider)
	}
}

// TestReloadCanDeliberatelyTurnTheSyntheticFeatureOff.
//
// The mirror of the keep-the-previous-database case above, and the reason that one is scoped to
// FAULTS rather than to "the new config produces no database". Clearing country_db_path is an
// unambiguous instruction, not a typo, and refusing to honour it would make the feature
// impossible to switch off without the restart #1998 removed.
func TestReloadCanDeliberatelyTurnTheSyntheticFeatureOff(t *testing.T) {
	running, _ := twoSyntheticDatabasesFromDifferentVendors(t)

	srv := syntheticServer(t, running)

	cfgPath := writeConfig(t, geoConfigYAML(geoSource{}))
	if err := srv.ReloadGeoDatabase(cfgPath); err != nil {
		t.Fatalf("clearing country_db_path is a valid instruction, not a fault: %v", err)
	}

	after := locationsFor(t, srv)
	if after.Available {
		t.Error("clearing country_db_path left the database open; the feature cannot be turned off")
	}
	if after.Reason != geoReasonNotConfigured {
		t.Errorf("reason %q, want %q", after.Reason, geoReasonNotConfigured)
	}
	// The assertion the mutation kills here: `available:false` is reachable without the handle
	// ever being released, and a database left open is a mapped file the operator asked the
	// gateway to stop using.
	if openAggregator(t, srv) != nil {
		t.Error("the aggregator survived a reload that turned the feature off")
	}
}
