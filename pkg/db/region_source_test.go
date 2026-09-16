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

func TestTheLatestChoiceOfTheDayWins(t *testing.T) {
	d := setupTestDB(t)
	repo := NewSQLiteRegionProbeRepo(d.conn)
	now := time.Now()

	// Someone who removes -server and reconnects must stop being reported as pinned.
	if err := repo.RecordRegionSource("u", regionvocab.SourceExplicitServer, now); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := repo.RecordRegionSource("u", regionvocab.SourceProbe, now.Add(time.Minute)); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := repo.GetRegionSources(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, c := range got {
		if c.Source == regionvocab.SourceExplicitServer && c.Users != 0 {
			t.Errorf("after un-pinning, the user must no longer count as pinned (got %d)", c.Users)
		}
		if c.Source == regionvocab.SourceProbe && c.Users != 1 {
			t.Errorf("the later choice must win, got %d probing users", c.Users)
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
