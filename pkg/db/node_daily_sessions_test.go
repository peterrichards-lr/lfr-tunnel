package db

import (
	"os"
	"testing"
	"time"
)

// Sessions per gateway per day (#1150).
//
// There was no way to see which edges were carrying sessions, so nothing surfaced that a
// region was unused, oversubscribed, or that a user was stranded on the wrong one. A
// US-based user ran against the control plane rather than the `us` edge for a whole day,
// because `us` was inside its scheduled power-off window when they started and the 24h
// region cache pinned the choice (#1148). It took reading journal logs to find.

func setupMetricRepo(t *testing.T) *SQLiteMetricRepo {
	t.Helper()
	database, tmpDir := setupTestDB(t)
	t.Cleanup(func() {
		_ = database.Close()     //nolint:errcheck
		_ = os.RemoveAll(tmpDir) //nolint:errcheck
	})
	return NewSQLiteMetricRepo(database.conn)
}

func sample(t *testing.T, repo *SQLiteMetricRepo, node, host string, connectedAt, recordedAt time.Time) {
	t.Helper()
	if err := repo.RecordTunnelMetric(&TunnelMetric{
		UserID:          "u1",
		SubdomainPrefix: host,
		FullHost:        host + ".example.com",
		BytesIn:         100,
		BytesOut:        200,
		ConnectedAt:     connectedAt,
		RecordedAt:      recordedAt,
		NodeID:          node,
	}); err != nil {
		t.Fatalf("RecordTunnelMetric: %v", err)
	}
}

func nodeDailyFor(t *testing.T, repo *SQLiteMetricRepo, date, node string) int {
	t.Helper()
	stats, err := repo.GetGlobalAnalytics(30)
	if err != nil {
		t.Fatalf("GetGlobalAnalytics: %v", err)
	}
	for _, nd := range stats.NodeDaily {
		if nd.Date == date && nd.NodeID == node {
			return nd.Sessions
		}
	}
	return -1
}

// The assertion that carries this feature: a row is a five-minute SAMPLE, not a session.
// MetricsCollector's ticker writes one per lease per interval while bytes are moving, so
// counting rows would measure how long tunnels stayed busy and label it demand -- one
// long-lived tunnel would outrank a dozen short ones and the panel would say the opposite
// of the truth.
func TestNodeDailySessionsCountsSessionsNotSamples(t *testing.T) {
	repo := setupMetricRepo(t)

	day := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	connected := day.Add(9 * time.Hour)

	// One session on `us`, sampled six times across half an hour.
	for i := 0; i < 6; i++ {
		sample(t, repo, "us", "alpha", connected, connected.Add(time.Duration(i*5)*time.Minute))
	}
	// Two separate sessions on `eu`, one sample each.
	sample(t, repo, "eu", "beta", connected, connected)
	sample(t, repo, "eu", "gamma", connected, connected)

	if got := nodeDailyFor(t, repo, "2026-08-20", "us"); got != 1 {
		t.Errorf("us: expected 1 session from 6 samples, got %d", got)
	}
	if got := nodeDailyFor(t, repo, "2026-08-20", "eu"); got != 2 {
		t.Errorf("eu: expected 2 sessions, got %d", got)
	}
}

// A reconnect is a new session. It gets a new connected_at, and it may land on a different
// node -- which is precisely the movement this panel exists to reveal.
func TestNodeDailySessionsCountsAReconnectSeparately(t *testing.T) {
	repo := setupMetricRepo(t)

	day := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	first := day.Add(9 * time.Hour)
	second := day.Add(14 * time.Hour)

	sample(t, repo, "us", "alpha", first, first)
	sample(t, repo, "us", "alpha", first, first.Add(5*time.Minute))
	sample(t, repo, "us", "alpha", second, second)

	if got := nodeDailyFor(t, repo, "2026-08-21", "us"); got != 2 {
		t.Errorf("expected 2 sessions across a reconnect, got %d", got)
	}
}

// The control plane is a first-class row, not a placeholder to filter out. #1148 is
// exactly a user sitting on `control` when they should have been on an edge, so hiding it
// would hide the case the panel is for. An empty node_id normalises to it, matching
// RecordTunnelMetric.
func TestNodeDailySessionsTreatsControlAsANode(t *testing.T) {
	repo := setupMetricRepo(t)

	day := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	sample(t, repo, "", "alpha", day, day)       // empty -> control
	sample(t, repo, "control", "beta", day, day) // explicit
	sample(t, repo, "us", "gamma", day, day)

	if got := nodeDailyFor(t, repo, "2026-08-22", "control"); got != 2 {
		t.Errorf("expected 2 control sessions, got %d", got)
	}
	if got := nodeDailyFor(t, repo, "2026-08-22", "us"); got != 1 {
		t.Errorf("expected 1 us session, got %d", got)
	}
}

// Separate days stay separate: the whole point is watching a node's line over time, and a
// query that collapsed days would show a flat total that never changes.
func TestNodeDailySessionsSplitsByDay(t *testing.T) {
	repo := setupMetricRepo(t)

	d1 := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	sample(t, repo, "us", "alpha", d1, d1)
	sample(t, repo, "us", "beta", d2, d2)
	sample(t, repo, "us", "gamma", d2, d2)

	if got := nodeDailyFor(t, repo, "2026-08-23", "us"); got != 1 {
		t.Errorf("day 1: expected 1, got %d", got)
	}
	if got := nodeDailyFor(t, repo, "2026-08-24", "us"); got != 2 {
		t.Errorf("day 2: expected 2, got %d", got)
	}
}

// An edge that has stopped receiving sessions is the signal worth seeing. It has no rows
// for the silent days, so it is simply absent from those -- the renderers have to plot a
// gap as zero rather than skipping it, and this documents where that responsibility sits.
func TestNodeDailySessionsOmitsDaysANodeCarriedNothing(t *testing.T) {
	repo := setupMetricRepo(t)

	d1 := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	sample(t, repo, "us", "alpha", d1, d1)
	sample(t, repo, "eu", "beta", d1, d1)
	sample(t, repo, "eu", "gamma", d2, d2) // us went quiet on d2

	if got := nodeDailyFor(t, repo, "2026-08-26", "us"); got != -1 {
		t.Errorf("expected no row for a node that carried nothing, got %d", got)
	}
	if got := nodeDailyFor(t, repo, "2026-08-26", "eu"); got != 1 {
		t.Errorf("eu on day 2: expected 1, got %d", got)
	}
}
