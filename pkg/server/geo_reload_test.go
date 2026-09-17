package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lfr-tunnel/pkg/geo"
)

// Switching geo-IP vendor without restarting central (#1998).
//
// country_db_path and country_db_provider were read once in NewServer, so changing vendor meant
// a restart -- which drops every tunnel central serves, for a change that affects one analytics
// panel. That made verifying a vendor expensive enough not to do, which is how the IP2Location
// attribution stayed wrong until someone finally downloaded the file (#1964).
//
// These tests cover the four things the issue names: the swap and the attribution following it,
// a bad path keeping the running database, an undeclared vendor keeping it too, and a reload
// that is genuinely a no-op when nothing changed. Plus the narrowness rule SIGHUP inherits from
// #1309: the whole file is re-read, and only the keys a reload claims to support are applied.

// geoConfigYAML renders the smallest config that names a country database.
//
// Written through config.LoadServerConfig rather than set on a struct, because the reload path
// under test starts at a file on disk -- a struct-level test would skip the parse, which is
// where a half-saved file goes wrong.
func geoConfigYAML(src geoSource) string {
	return "domains:\n  - example.com\n" +
		"country_db_path: \"" + src.Path + "\"\n" +
		"country_db_provider: \"" + src.Provider + "\"\n"
}

// geoSourceOf reads what the running server has in force, under the same lock production uses.
func geoSourceOf(t *testing.T, srv *Server) geoSource {
	t.Helper()
	srv.geoMu.RLock()
	defer srv.geoMu.RUnlock()
	return srv.geoSource
}

// openAggregator returns the aggregator pointer currently in force.
//
// Identity, not contents: it is how "the open file was left alone" is told apart from "closed
// and reopened to the same path", which is exactly the distinction the unchanged-config case
// turns on and which no field of the response can express.
func openAggregator(t *testing.T, srv *Server) *geo.Aggregator {
	t.Helper()
	srv.geoMu.RLock()
	defer srv.geoMu.RUnlock()
	return srv.geo
}

// vendorFromFilename derives the declared vendor from the `<vendor>-<edition>-<yyyy-mm>.mmdb`
// convention #1998 standardises, so a two-database run needs two env vars rather than four.
//
// Overridable, because the convention is an operator habit and not a guarantee: a file named
// anything else needs LFT_GEO_TEST_DB_PROVIDER / LFT_GEO_TEST_DB_ALT_PROVIDER set explicitly.
func vendorFromFilename(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasPrefix(base, "dbip"):
		return string(geo.ProviderDBIP)
	case strings.HasPrefix(base, "ip2location"):
		return string(geo.ProviderIP2Location)
	case strings.HasPrefix(base, "geolite"), strings.HasPrefix(base, "geoip"), strings.HasPrefix(base, "maxmind"):
		return string(geo.ProviderMaxMind)
	default:
		return ""
	}
}

// aRealDatabase is one downloaded .mmdb with its vendor declared, or the test skips.
//
// No database ships with this repo and none can -- every vendor forbids redistribution, which
// is the whole reason #1964 and this issue exist.
//
//	LFT_GEO_TEST_DB=/path/to/dbip-city-lite-2026-09.mmdb make test PKG=./pkg/server/
func aRealDatabase(t *testing.T) geoSource {
	t.Helper()
	path := os.Getenv("LFT_GEO_TEST_DB")
	if path == "" {
		t.Skip("set LFT_GEO_TEST_DB to a real .mmdb to exercise a reload against one")
	}
	provider := os.Getenv("LFT_GEO_TEST_DB_PROVIDER")
	if provider == "" {
		provider = vendorFromFilename(path)
	}
	if provider == "" {
		t.Fatalf("cannot tell which vendor published %s; set LFT_GEO_TEST_DB_PROVIDER", path)
	}
	return geoSource{Path: path, Provider: provider}
}

// twoRealDatabasesFromDifferentVendors is what makes a swap OBSERVABLE, and is the whole
// premise of the control below.
//
// If both files were the same vendor the panel would credit the same name before and after, and
// a reload that did nothing at all would pass. So the difference is asserted here rather than
// assumed: a run configured with two files from one vendor fails loudly instead of quietly
// proving nothing.
//
//	LFT_GEO_TEST_DB=/…/dbip-city-lite-2026-09.mmdb \
//	LFT_GEO_TEST_DB_ALT=/…/ip2location-lite-db11-2026-09.mmdb \
//	  make test PKG=./pkg/server/
func twoRealDatabasesFromDifferentVendors(t *testing.T) (first, second geoSource) {
	t.Helper()
	first = aRealDatabase(t)
	path := os.Getenv("LFT_GEO_TEST_DB_ALT")
	if path == "" {
		t.Skip("set LFT_GEO_TEST_DB_ALT to a second vendor's .mmdb to exercise a vendor swap")
	}
	provider := os.Getenv("LFT_GEO_TEST_DB_ALT_PROVIDER")
	if provider == "" {
		provider = vendorFromFilename(path)
	}
	if provider == "" {
		t.Fatalf("cannot tell which vendor published %s; set LFT_GEO_TEST_DB_ALT_PROVIDER", path)
	}
	second = geoSource{Path: path, Provider: provider}

	if first.Provider == second.Provider {
		t.Fatalf("both databases declare vendor %q (%s, %s) -- a swap between two files from the "+
			"same vendor renders the same credit before and after, so this test could not tell a "+
			"working reload from one that did nothing",
			first.Provider, first.Path, second.Path)
	}
	if first.Path == second.Path {
		t.Fatalf("LFT_GEO_TEST_DB and LFT_GEO_TEST_DB_ALT name the same file (%s)", first.Path)
	}
	return first, second
}

// TestReloadSwapsTheDatabaseAndTheAttributionFollowsIt is THE control this issue asks for.
//
// The panel's credit has to follow the file that is actually open: that is the point of the
// exercise, because the failure #1964 fixed was a panel crediting MaxMind over IP2Location's
// data. So this asserts the provider DIFFERS before and after, against two real files from two
// real vendors -- a reload that quietly did nothing would still report the first vendor and go
// red here.
//
// It asserts the path in force moved too. Attribution alone is not enough: the vendor is
// declared rather than derived (#1964), so a reload that applied country_db_provider and
// ignored country_db_path would change the credit while still serving the old file -- which is
// precisely the false attribution this is supposed to prevent, arrived at from the other side.
func TestReloadSwapsTheDatabaseAndTheAttributionFollowsIt(t *testing.T) {
	first, second := twoRealDatabasesFromDifferentVendors(t)

	srv := geoServerWithVendor(t, first.Path, first.Provider)
	before := locationsFor(t, srv)
	beforeAggregator := openAggregator(t, srv)

	if !before.Available {
		t.Fatalf("premise broken: %s did not open (reason %q), so there is nothing to swap FROM",
			first.Path, before.Reason)
	}
	if before.Provider != first.Provider {
		t.Fatalf("premise broken: the panel credits %q before the reload, want %q",
			before.Provider, first.Provider)
	}

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
	if before.Provider == after.Provider {
		t.Errorf("the credit is %q before and after: a reload that did nothing at all would look "+
			"identical to this", after.Provider)
	}
	if got := geoSourceOf(t, srv); got.Path != second.Path {
		t.Errorf("the open path is %q, want %q -- the vendor declaration moved without the file, "+
			"which credits the new vendor for the old vendor's data", got.Path, second.Path)
	}
	if openAggregator(t, srv) == beforeAggregator {
		t.Error("the same aggregator is in force after the reload, so no new database was opened")
	}

	// And the newly opened file genuinely answers lookups. Without this the test would pass on a
	// reload that recorded the new path and left a dead resolver behind it -- "the fields
	// changed" is satisfied by any half of the swap.
	const threshold = geo.DefaultThreshold
	for i := 0; i <= threshold; i++ {
		srv.observeGeoLocation(fmt.Sprintf("swap-user-%d@example.com", i), "8.8.8.8")
	}
	srv.withGeoAggregator(func(a *geo.Aggregator) {
		if err := a.Flush(); err != nil {
			t.Fatalf("flushing after the swap: %v", err)
		}
	})
	if got := locationsFor(t, srv); len(got.Buckets) == 0 {
		t.Errorf("the database opened by the reload resolved nothing: %d registrations from a "+
			"well-known public address produced no bucket", threshold+1)
	}
}

// TestReloadWithABadPathKeepsThePreviousDatabaseServing.
//
// A reload that breaks a working feature because of a typo is worse than no reload at all --
// the precedent ReloadEdgeNodes set with "keeping the edge nodes already in force". An operator
// switching vendor is exactly the person likely to mistype a filename, and the panel going dark
// is not a helpful way to be told.
func TestReloadWithABadPathKeepsThePreviousDatabaseServing(t *testing.T) {
	running := aRealDatabase(t)
	srv := geoServerWithVendor(t, running.Path, running.Provider)
	if before := locationsFor(t, srv); !before.Available {
		t.Fatalf("premise broken: %s did not open (reason %q)", running.Path, before.Reason)
	}
	serving := openAggregator(t, srv)

	typo := filepath.Join(t.TempDir(), "geoip", "dbip-city-lite-2026-O9.mmdb")
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

// TestReloadWithNoDeclaredVendorKeepsThePreviousDatabase.
//
// Separate from the bad-path case because it is a different key and a different mistake: an
// operator who edits country_db_path and forgets country_db_provider has a file that is present
// and perfectly readable. Turning the panel off for it would be the reload breaking a working
// feature over an omission -- and the more tempting failure, because "no vendor declared" does
// legitimately mean "off" at STARTUP (#1964). It must not mean it here.
func TestReloadWithNoDeclaredVendorKeepsThePreviousDatabase(t *testing.T) {
	running := aRealDatabase(t)
	srv := geoServerWithVendor(t, running.Path, running.Provider)
	if before := locationsFor(t, srv); !before.Available {
		t.Fatalf("premise broken: %s did not open (reason %q)", running.Path, before.Reason)
	}
	serving := openAggregator(t, srv)

	cfgPath := writeConfig(t, geoConfigYAML(geoSource{Path: running.Path, Provider: ""}))

	// Composed with #1995, which moved the vendor gate from construction to the panel: the
	// reload SUCCEEDS, because the file is readable and a display value is not a reason to
	// refuse to open one. What the dropped vendor costs is publication, not the database.
	if err := srv.ReloadGeoDatabase(cfgPath); err != nil {
		t.Fatalf("dropping country_db_provider must not fail the reload -- the file is present "+
			"and readable, and the vendor decides attribution rather than decoding: %v", err)
	}

	// The database stays open, which is what this test has always been for. Counts keep
	// accruing while nobody is looking at them, so naming the vendor later makes the history
	// visible rather than starting the week again.
	if openAggregator(t, srv) != serving {
		t.Error("the open database was replaced when only its declared vendor changed")
	}

	// And the panel goes to the "choose a vendor" state rather than carrying on crediting the
	// vendor the config no longer declares. Keeping the old credit would be the #1964 failure
	// exactly: publishing one vendor's data under another vendor's name, on the strength of a
	// declaration that has been withdrawn.
	after := locationsFor(t, srv)
	if after.Available {
		t.Errorf("the panel still publishes as %q after the declaration was withdrawn", after.Provider)
	}
	if after.Reason != geoReasonProviderNotDeclared {
		t.Errorf("reason %q, want %q -- an operator who deleted one key must be told which one",
			after.Reason, geoReasonProviderNotDeclared)
	}
	if after.ConfiguredPath != running.Path {
		t.Errorf("the panel reports path %q, want %q -- it must still show which file was found",
			after.ConfiguredPath, running.Path)
	}
}

// TestReloadWithAnIdenticalConfigDoesNotChurnTheOpenFile.
//
// SIGHUP is the edge-token withdrawal signal too (#1309), so this path runs every time an
// operator revokes a credential -- and a reopen is not free: closing the aggregator flushes and
// then DISCARDS the current period's in-memory user sets, so an unrelated edit would cost the
// week's distinct-user accuracy. Pointer identity is the only assertion that can tell "left
// alone" from "closed and reopened to the same path".
func TestReloadWithAnIdenticalConfigDoesNotChurnTheOpenFile(t *testing.T) {
	running := aRealDatabase(t)
	srv := geoServerWithVendor(t, running.Path, running.Provider)
	if before := locationsFor(t, srv); !before.Available {
		t.Fatalf("premise broken: %s did not open (reason %q)", running.Path, before.Reason)
	}
	serving := openAggregator(t, srv)

	same := writeConfig(t, geoConfigYAML(running))
	if err := srv.ReloadGeoDatabase(same); err != nil {
		t.Fatalf("an unchanged config must be a successful no-op, got: %v", err)
	}
	if openAggregator(t, srv) != serving {
		t.Error("an unchanged config reopened the database, losing this period's in-memory user sets")
	}

	// Redeclaring the VENDOR on the same file is also a no-op for the handle, and is asserted
	// rather than left implied (#1995 + #1998). The vendor never touches decoding, so the same
	// path with a new label is the same open file -- and reopening it would cost the period's
	// user sets for a display value. The credit must still follow, which is what stops this
	// reading as "the reload ignored me".
	other := geo.ProviderMaxMind
	if running.Provider == string(other) {
		other = geo.ProviderDBIP
	}
	relabelled := writeConfig(t, geoConfigYAML(geoSource{Path: running.Path, Provider: string(other)}))
	if err := srv.ReloadGeoDatabase(relabelled); err != nil {
		t.Fatalf("redeclaring the vendor is a valid change: %v", err)
	}
	if openAggregator(t, srv) != serving {
		t.Error("redeclaring the vendor reopened the same file, losing this period's in-memory user sets")
	}
	if got := locationsFor(t, srv); got.Provider != string(other) {
		t.Errorf("the panel credits %q after redeclaring the vendor as %q -- the label did not move",
			got.Provider, other)
	}

	// CONTROL. Without it, both assertions above are satisfied by a reload that never reopens
	// anything -- including the broken one this whole issue exists to replace. A different FILE
	// is the only change that must reopen, now that a vendor change deliberately must not.
	_, second := twoRealDatabasesFromDifferentVendors(t)
	changed := writeConfig(t, geoConfigYAML(second))
	if err := srv.ReloadGeoDatabase(changed); err != nil {
		t.Fatalf("pointing at a second real database is a valid change: %v", err)
	}
	if openAggregator(t, srv) == serving {
		t.Fatalf("CONTROL: a CHANGED file left the same database open, so the no-op assertions " +
			"above prove nothing -- they would hold for a reload that never reopens anything")
	}
}

// TestReloadCanDeliberatelyTurnTheFeatureOff.
//
// The mirror of the two keep-the-previous-database tests, and the reason they are scoped to
// FAULTS rather than to "the new config produces no database". Clearing country_db_path is an
// unambiguous instruction, not a typo, and refusing to honour it would make the feature
// impossible to switch off without the restart this issue removes.
func TestReloadCanDeliberatelyTurnTheFeatureOff(t *testing.T) {
	running := aRealDatabase(t)
	srv := geoServerWithVendor(t, running.Path, running.Provider)
	if before := locationsFor(t, srv); !before.Available {
		t.Fatalf("premise broken: %s did not open (reason %q)", running.Path, before.Reason)
	}

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
	if openAggregator(t, srv) != nil {
		t.Error("the aggregator survived a reload that turned the feature off")
	}
}

// TestReloadAConfigThatWillNotParseKeepsTheDatabase mirrors ReloadEdgeNodes's own contract: a
// half-saved file changes nothing. Needs no real database -- the running state is whatever it
// was, and the assertion is that the reload did not touch it.
func TestReloadAConfigThatWillNotParseKeepsTheDatabase(t *testing.T) {
	srv := geoServerWithVendor(t, "", "")
	before := geoSourceOf(t, srv)

	broken := writeConfig(t, "domains:\n  - example.com\ncountry_db_path: [unclosed\n")
	err := srv.ReloadGeoDatabase(broken)
	if err == nil {
		t.Fatal("a config that does not parse must be reported as a failed reload")
	}
	if !strings.Contains(err.Error(), "keeping the geo-IP database already in force") {
		t.Errorf("the error must say the running database was kept, got: %v", err)
	}
	if got := geoSourceOf(t, srv); got != before {
		t.Errorf("a config that does not parse changed what is in force: %+v -> %+v", before, got)
	}
}

// TestReloadAppliesOnlyTheGeoKeys.
//
// SIGHUP re-reads the WHOLE config file, and only the keys a reload claims to support may be
// applied. Silently adopting an unrelated edit half-applies a config -- the operator edited two
// things, one took effect, and nothing anywhere says which. The narrowness is the same rule
// #1454 set for edge_nodes, restated here because this is the second reloadable key and the
// first one's guarantee does not automatically extend.
func TestReloadAppliesOnlyTheGeoKeys(t *testing.T) {
	srv := geoServerWithVendor(t, "", "")
	wantDomains := append([]string(nil), srv.cfg.Domains...)
	wantForceMFA := srv.cfg.ForceMFA
	wantDBPath := srv.cfg.DBPath

	// Every one of these differs from what the server started with, and none of them is a geo
	// key. force_mfa is the sharpest: adopting it on reload would silently change an
	// authentication requirement for every user.
	cfgPath := writeConfig(t, "domains:\n  - reloaded.example.net\n"+
		"force_mfa: true\n"+
		"db_path: \"/tmp/reload-must-not-adopt-this.db\"\n"+
		"country_db_path: \"\"\ncountry_db_provider: \"\"\n")

	if err := srv.ReloadGeoDatabase(cfgPath); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if strings.Join(srv.cfg.Domains, ",") != strings.Join(wantDomains, ",") {
		t.Errorf("the reload adopted domains=%v, want %v -- it is not a geo key", srv.cfg.Domains, wantDomains)
	}
	if srv.cfg.ForceMFA != wantForceMFA {
		t.Errorf("the reload adopted force_mfa=%v; an unrelated edit changed an authentication "+
			"requirement for every user", srv.cfg.ForceMFA)
	}
	if srv.cfg.DBPath != wantDBPath {
		t.Errorf("the reload adopted db_path=%q, want %q", srv.cfg.DBPath, wantDBPath)
	}
}

// TestReloadIsRaceFreeAgainstObserveGeoLocation.
//
// observeGeoLocation reads the aggregator on every registration, so a reload cannot simply
// assign over it. Under -race an unguarded swap is a reported data race rather than a rare
// wrong answer, which is why this exists and why CI runs a race-detector job:
//
//	make test PKG=./pkg/server/ TEST_BUILD_FLAGS=-race
//
// Runs with or without a real database. Without one, the two configs still differ from each
// other every iteration, so the unchanged-config no-op never short-circuits and the guarded
// fields are genuinely written while the registration path is reading them. With two real
// databases (LFT_GEO_TEST_DB / LFT_GEO_TEST_DB_ALT) it additionally exercises the close of the
// outgoing resolver, which is the half a use-after-close would show up in.
func TestReloadIsRaceFreeAgainstObserveGeoLocation(t *testing.T) {
	first := geoSource{Path: filepath.Join(t.TempDir(), "absent-a.mmdb"), Provider: string(geo.ProviderDBIP)}
	second := geoSource{Path: filepath.Join(t.TempDir(), "absent-b.mmdb"), Provider: string(geo.ProviderMaxMind)}
	if path := os.Getenv("LFT_GEO_TEST_DB"); path != "" {
		if alt := os.Getenv("LFT_GEO_TEST_DB_ALT"); alt != "" {
			first, second = twoRealDatabasesFromDifferentVendors(t)
			t.Logf("racing real databases: %s <-> %s", first.Path, second.Path)
		}
	}

	srv := geoServerWithVendor(t, first.Path, first.Provider)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for worker := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				srv.observeGeoLocation(fmt.Sprintf("racer-%d-%d@example.com", worker, n), "8.8.8.8")
			}
		}(worker)
	}

	for i := range 20 {
		src := first
		if i%2 == 1 {
			src = second
		}
		if err := srv.applyGeoDatabase(src, false); err != nil {
			t.Errorf("iteration %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}

// TestTheReloadSummaryNamesEveryReloadableKey.
//
// The summary an operator reads in the journal is the only place the narrowness is stated, and
// it said "nothing else is re-read" for as long as edge_nodes was the only reloadable key. That
// sentence became false the moment a second key joined, and a stale narrowness claim is worse
// than none: it is read as current and actively misleads the person editing a third key.
//
// Asserted as a property of the constant rather than as three string literals, so adding a
// reloadable key and forgetting the sentence turns this red.
func TestTheReloadSummaryNamesEveryReloadableKey(t *testing.T) {
	for _, key := range []string{"edge_nodes", "country_db_path", "country_db_provider"} {
		if !strings.Contains(reloadNarrowness, key) {
			t.Errorf("the reload summary does not name %q, so an operator cannot tell from it "+
				"which keys SIGHUP applies: %q", key, reloadNarrowness)
		}
	}
	if !strings.Contains(reloadNarrowness, "nothing else") {
		t.Errorf("the reload summary must still say the list is exhaustive: %q", reloadNarrowness)
	}
}
