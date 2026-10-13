package db

import (
	"testing"
	"time"

	"lfr-tunnel/pkg/regionvocab"
)

// #1922: a pinned client runs no latency probe, so it sends an empty probe set -- identical on
// the wire to a client with reporting off and to one too old to send any. These assert the
// source is what separates them, and that the counts are per PERSON.

func TestAPinnedUserIsCountedAsPinned(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)
	now := time.Now()

	if err := repo.RecordRegionSource("pinned-user", regionvocab.SourceExplicitServer, now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := repo.RecordRegionSource("probing-user", regionvocab.SourceProbe, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	bySource := map[string]RegionSourceCount{}
	for _, c := range got {
		bySource[c.Source] = c
	}
	if bySource[regionvocab.SourceExplicitServer].Users != 1 {
		t.Errorf("want 1 pinned user, got %d", bySource[regionvocab.SourceExplicitServer].Users)
	}
	if !bySource[regionvocab.SourceExplicitServer].Pinned {
		t.Error("explicit_server must be flagged Pinned -- it is the source that cannot move")
	}
	if bySource[regionvocab.SourceProbe].Users != 1 {
		t.Errorf("want 1 probing user, got %d", bySource[regionvocab.SourceProbe].Users)
	}
	// The distinction that matters: -region skips the probe but KEEPS failover, so it must
	// not be reported alongside -server. Conflating them sends someone to fix a client that
	// is behaving correctly.
	if bySource[regionvocab.SourceExplicitRegion].Pinned {
		t.Error("explicit_region must NOT be flagged Pinned: -region keeps failover")
	}
}

func TestReconnectingDoesNotInflateTheCount(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)
	now := time.Now()

	// Fifty reconnects in a day must count as one person, matching region_probes. Otherwise
	// one developer on a flaky connection dominates the report and it describes them rather
	// than the fleet.
	for i := 0; i < 50; i++ {
		if err := repo.RecordRegionSource("busy-user", regionvocab.SourceExplicitServer, now); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, c := range got {
		if c.Source == regionvocab.SourceExplicitServer && c.Users != 1 {
			t.Errorf("50 reconnects by one user must count as 1, got %d", c.Users)
		}
	}
}

// TestEveryRouteOfTheDayIsKept is the control for #2372, and replaces
// TestTheLatestChoiceOfTheDayWins, which pinned the behaviour #2372 removed.
//
// The production shape, exactly: a planned drain moves a user to another gateway (failover), and
// an hour later the client moves back (failback). Keyed on (user, day) the failback overwrote the
// failover, so the report could show one route or the other for that user that day, never both --
// and since a transient route is nearly always followed by another registration, it was usually
// the transient one that vanished: of the two failbacks registered directly on central, one was
// overwritten. (The other 41 of the audit log's 43 were lost to the edge path never recording
// at all -- #2368, fixed separately.)
func TestEveryRouteOfTheDayIsKept(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)
	now := time.Now()

	for i, src := range []string{regionvocab.SourceProbe, regionvocab.SourceFailover, regionvocab.SourceFailback} {
		if err := repo.RecordRegionSource("wes", src, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("record %s: %v", src, err)
		}
	}

	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, c := range got {
		switch c.Source {
		case regionvocab.SourceProbe, regionvocab.SourceFailover, regionvocab.SourceFailback:
			if c.Users != 1 {
				t.Errorf("%s: want 1 user -- every route a user took today must be kept, got %d (#2372)", c.Source, c.Users)
			}
		}
	}
}

// TestAUserPinnedEarlierTodayStillCountsAsPinned states the one consequence #2372 accepted.
//
// The old upsert let someone who removed -server and reconnected stop counting as pinned the same
// day. Keeping every route means they count as pinned for any window that includes the day they
// were pinned -- which a multi-day window already did across days. So "pinned" now reads, within
// the day as well as across days: pinned at some point in the window.
func TestAUserPinnedEarlierTodayStillCountsAsPinned(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)
	// Noon UTC today, not time.Now(): run at 23:59:59 the two records could land on different
	// days, and then the OLD (user, day) key would also count them as pinned -- a control that
	// passes for the wrong reason (#2374 review).
	y, m, dd := time.Now().UTC().Date()
	now := time.Date(y, m, dd, 12, 0, 0, 0, time.UTC)

	if err := repo.RecordRegionSource("u", regionvocab.SourceExplicitServer, now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := repo.RecordRegionSource("u", regionvocab.SourceProbe, now.Add(time.Second)); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, c := range got {
		if c.Source == regionvocab.SourceExplicitServer && c.Users != 1 {
			t.Errorf("a user pinned earlier today must still count as pinned in today's window, got %d", c.Users)
		}
		if c.Source == regionvocab.SourceProbe && c.Users != 1 {
			t.Errorf("the later probe must be counted too, got %d", c.Users)
		}
	}
}

func TestEveryDeclaredSourceIsReportedEvenAtZero(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)

	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// A zero is information -- "nobody is pinned" -- and omitting the row would read as
	// "not measured", which is the ambiguity this whole feature exists to remove.
	if len(got) != len(regionvocab.Sources) {
		t.Fatalf("want all %d declared sources reported, got %d", len(regionvocab.Sources), len(got))
	}
	for i, s := range regionvocab.Sources {
		if got[i].Source != s {
			t.Errorf("position %d: want %q in declared order, got %q", i, s, got[i].Source)
		}
		if got[i].Users != 0 {
			t.Errorf("%s: want 0 on an empty database, got %d", s, got[i].Users)
		}
	}
}

func TestAnUnknownSourceIsNamedNotFolded(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)

	// A client newer than this gateway may send a source this build has never heard of.
	// Folding it into a known bucket would misreport it; dropping it would hide exactly the
	// clients most worth noticing.
	if err := repo.RecordRegionSource("future-user", "teleport", time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var found bool
	for _, c := range got {
		if c.Source == "teleport" {
			found = true
			if c.Users != 1 {
				t.Errorf("want 1 user on the unknown source, got %d", c.Users)
			}
			if c.Pinned {
				t.Error("an unknown source must not be assumed pinned")
			}
		}
	}
	if !found {
		t.Error("an unrecognised source must be reported by name, not silently dropped")
	}
	if len(got) != len(regionvocab.Sources)+1 {
		t.Errorf("want the declared vocabulary plus the unknown, got %d entries", len(got))
	}
}
