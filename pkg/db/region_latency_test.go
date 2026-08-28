package db

import (
	"testing"
	"time"
)

// Latency as a placement signal (#1151).
//
// The figures here get read as "this is what people experience", so the two things that must
// hold are that the distribution counts PEOPLE rather than sessions, and that a user's own
// repeated reconnects cannot move it.

func ms(v int) *int { return &v }

func setupProbeRepo(t *testing.T) *SQLiteRegionProbeRepo {
	t.Helper()
	database := setupTestDB(t)
	return NewSQLiteRegionProbeRepo(database.conn)
}

// TestRegionProbesCountUsersNotSessions is the counting trap the issue calls out by name: the
// country panel counted sessions, so one user reconnecting repeatedly dominated it.
func TestRegionProbesCountUsersNotSessions(t *testing.T) {
	repo := setupProbeRepo(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	// One user reconnects fifty times, getting slower each time.
	for i := range 50 {
		if err := repo.RecordRegionProbes("noisy@example.com", []RegionProbeSample{
			{Region: "us", RTTMs: ms(20 + i)},
		}, now); err != nil {
			t.Fatalf("recording: %v", err)
		}
	}
	// A second user connects once.
	if err := repo.RecordRegionProbes("quiet@example.com", []RegionProbeSample{
		{Region: "us", RTTMs: ms(200)},
	}, now); err != nil {
		t.Fatalf("recording: %v", err)
	}

	report, err := repo.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Regions) != 1 {
		t.Fatalf("expected one region, got %d", len(report.Regions))
	}
	if got := report.Regions[0].Users; got != 2 {
		t.Errorf("fifty reconnects by one person must count once: expected 2 users, got %d", got)
	}
}

// TestRegionProbesPercentiles — the median and p90 have to describe the fleet.
func TestRegionProbesPercentiles(t *testing.T) {
	repo := setupProbeRepo(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	for i, rtt := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 500} {
		user := string(rune('a'+i)) + "@example.com"
		if err := repo.RecordRegionProbes(user, []RegionProbeSample{{Region: "eu", RTTMs: ms(rtt)}}, now); err != nil {
			t.Fatalf("recording: %v", err)
		}
	}

	report, err := repo.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	got := report.Regions[0]
	if got.Users != 10 {
		t.Fatalf("expected 10 users, got %d", got.Users)
	}
	if got.MedianMs != 50 {
		t.Errorf("median of 10..500 should be 50, got %d", got.MedianMs)
	}
	// Nearest-rank, so p90 is a value somebody actually measured rather than an interpolation
	// between two of them.
	if got.P90Ms != 90 {
		t.Errorf("p90 should be a measured value (90), got %d", got.P90Ms)
	}
}

// TestRegionProbesPoorlyServedUsesBestRegion is the figure the placement decision turns on.
//
// A region can look perfectly healthy on its own median while the people using it have no good
// option anywhere -- which is exactly the case that justifies a new edge.
func TestRegionProbesPoorlyServedUsesBestRegion(t *testing.T) {
	repo := setupProbeRepo(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	// Someone with a good option: slow to one region, fast to another. Not poorly served.
	if err := repo.RecordRegionProbes("served@example.com", []RegionProbeSample{
		{Region: "us", RTTMs: ms(400)},
		{Region: "eu", RTTMs: ms(20)},
	}, now); err != nil {
		t.Fatal(err)
	}
	// Someone with no good option anywhere. This is the person a new edge would be for.
	if err := repo.RecordRegionProbes("stranded@example.com", []RegionProbeSample{
		{Region: "us", RTTMs: ms(300)},
		{Region: "eu", RTTMs: ms(280)},
	}, now); err != nil {
		t.Fatal(err)
	}

	report, err := repo.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.PoorlyServedUsers != 1 {
		t.Errorf("only the user whose BEST region is slow counts as poorly served, got %d", report.PoorlyServedUsers)
	}
	if report.ThresholdMs != PoorLatencyThresholdMs {
		t.Errorf("the report must state the threshold it used, got %d", report.ThresholdMs)
	}
}

// TestRegionProbesRecordsUnreachable — a region nobody can reach is a placement fact, not
// missing data, and it must not be averaged in as though it were fast.
func TestRegionProbesRecordsUnreachable(t *testing.T) {
	repo := setupProbeRepo(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	if err := repo.RecordRegionProbes("a@example.com", []RegionProbeSample{
		{Region: "sa"},                // no answer
		{Region: "eu", RTTMs: ms(40)}, // fine
	}, now); err != nil {
		t.Fatal(err)
	}

	report, err := repo.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	byRegion := map[string]RegionLatency{}
	for _, r := range report.Regions {
		byRegion[r.Region] = r
	}
	sa, ok := byRegion["sa"]
	if !ok {
		t.Fatal("a region that answered nobody must still appear in the report")
	}
	if sa.Unreachable != 1 || sa.Users != 0 {
		t.Errorf("an unreachable probe is not a fast one: users=%d unreachable=%d", sa.Users, sa.Unreachable)
	}
	if sa.MedianMs != 0 {
		t.Errorf("a region with no successful probes has no median, got %d", sa.MedianMs)
	}
	if report.PoorlyServedUsers != 0 {
		t.Errorf("the user had a 40ms option, so is not poorly served; got %d", report.PoorlyServedUsers)
	}
}

// TestRegionProbesWindow — old samples fall out, so the report describes the fleet as it is now
// rather than as it was when an edge was somewhere else.
func TestRegionProbesWindow(t *testing.T) {
	repo := setupProbeRepo(t)

	old := time.Now().UTC().AddDate(0, 0, -90)
	recent := time.Now().UTC()

	if err := repo.RecordRegionProbes("old@example.com", []RegionProbeSample{{Region: "us", RTTMs: ms(500)}}, old); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRegionProbes("new@example.com", []RegionProbeSample{{Region: "us", RTTMs: ms(30)}}, recent); err != nil {
		t.Fatal(err)
	}

	report, err := repo.GetRegionLatency(7)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Regions[0].Users != 1 || report.Regions[0].MedianMs != 30 {
		t.Errorf("a 7-day report must exclude a 90-day-old sample, got users=%d median=%d",
			report.Regions[0].Users, report.Regions[0].MedianMs)
	}
}

// TestRegionProbesIgnoresEmptyInput — a client that reports nothing must not create rows, and an
// anonymous caller must not be attributed to anyone.
func TestRegionProbesIgnoresEmptyInput(t *testing.T) {
	repo := setupProbeRepo(t)
	now := time.Now().UTC()

	if err := repo.RecordRegionProbes("", []RegionProbeSample{{Region: "us", RTTMs: ms(10)}}, now); err != nil {
		t.Errorf("an empty user id must be ignored quietly, got: %v", err)
	}
	if err := repo.RecordRegionProbes("a@example.com", nil, now); err != nil {
		t.Errorf("no samples must be ignored quietly, got: %v", err)
	}

	report, err := repo.GetRegionLatency(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Regions) != 0 {
		t.Errorf("nothing should have been stored, got %d region(s)", len(report.Regions))
	}
}
