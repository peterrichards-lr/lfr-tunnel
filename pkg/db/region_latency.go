package db

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// Region latency, measured by the clients that actually use the service (#1151).
//
// The placement decision -- do we need an edge in this region -- turns on the latency users
// really experience, and that measurement was being taken and thrown away. Every client probes
// each advertised region at startup to choose one, then discards every result but the winner.
//
// Those numbers beat geography as a placement signal: they include the VPN, tethering and
// routing penalties a country code cannot show. A user in Spain and a user in Poland are both
// "Europe" and have very different experiences of an Irish edge.
//
// It also sidesteps the privacy question. An RTT is not personal data in the way an IP or a
// derived location is, so nothing here stores either.

// RegionProbeSample is one client's measurement of one region.
type RegionProbeSample struct {
	Region string
	// RTTMs is the round trip in milliseconds. Nil means the region did not answer, which is
	// itself a signal -- an edge nobody can reach is a placement fact, not missing data.
	RTTMs *int
}

// RegionLatency is the aggregated view of one region.
type RegionLatency struct {
	Region string `json:"region"`
	// Users is the number of DISTINCT users who measured this region, not the number of
	// sessions. See the primary key on region_probes for why that distinction is enforced in
	// the schema rather than in a query somebody has to remember to write correctly.
	Users       int `json:"users"`
	MedianMs    int `json:"median_ms"`
	P90Ms       int `json:"p90_ms"`
	Unreachable int `json:"unreachable_users"`
}

// RegionLatencyReport answers "where do we need an edge".
type RegionLatencyReport struct {
	Days    int             `json:"days"`
	Regions []RegionLatency `json:"regions"`
	// PoorlyServedUsers counts users whose BEST available region was still slower than
	// ThresholdMs. This is the figure the placement decision actually turns on: a region can
	// look fine on its own median while the people using it have no good option at all.
	PoorlyServedUsers int `json:"poorly_served_users"`
	ThresholdMs       int `json:"threshold_ms"`
}

// PoorLatencyThresholdMs is what counts as "no good option".
//
// 150ms is the point at which request/response cycles start to feel like waiting rather than
// like a delay, and a tunnel adds a round trip to everything the developer does. It is a
// reporting threshold, not a limit enforced anywhere, so it is a starting point to be revised
// once there is data -- which is the whole point of collecting it.
const PoorLatencyThresholdMs = 150

type SQLiteRegionProbeRepo struct {
	conn *sql.DB
}

func NewSQLiteRegionProbeRepo(conn *sql.DB) *SQLiteRegionProbeRepo {
	return &SQLiteRegionProbeRepo{conn: conn}
}

// RecordRegionProbes stores one client's probe set for today.
//
// Upserts on (user_id, region, day), so a user reconnecting all day contributes one sample per
// region rather than one per session. Without that, a single developer with a flaky connection
// would dominate the distribution and the report would describe them rather than the fleet.
func (repo *SQLiteRegionProbeRepo) RecordRegionProbes(userID string, samples []RegionProbeSample, at time.Time) error {
	if userID == "" || len(samples) == 0 {
		return nil
	}
	day := at.UTC().Format("2006-01-02")

	tx, err := repo.conn.Begin()
	if err != nil {
		return fmt.Errorf("region probes: %w", err)
	}
	// Rolled back explicitly rather than by `defer func() { _ = tx.Rollback() }()`. Discarding
	// that error is the errcheck suppression the ratchet exists to stop (#1498), and a rollback
	// that itself fails is worth saying: it means the write neither happened nor cleanly did
	// not.
	for _, s := range samples {
		if s.Region == "" {
			continue
		}
		var rtt any
		if s.RTTMs != nil {
			rtt = *s.RTTMs
		}
		if _, err := tx.Exec(`
			INSERT INTO region_probes (user_id, region, day, rtt_ms, recorded_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, region, day) DO UPDATE SET rtt_ms = excluded.rtt_ms, recorded_at = excluded.recorded_at`,
			userID, s.Region, day, rtt, at.UTC()); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				return fmt.Errorf("region probes: %w (rollback also failed: %v)", err, rbErr)
			}
			return fmt.Errorf("region probes: %w", err)
		}
	}
	return tx.Commit()
}

// GetRegionLatency aggregates the last `days` days.
//
// The percentiles are computed in Go rather than in SQL because SQLite has no percentile
// function, and the row count is bounded by users x regions x days -- small enough that fetching
// it is cheaper than the CTE gymnastics the alternative needs.
func (repo *SQLiteRegionProbeRepo) GetRegionLatency(days int) (*RegionLatencyReport, error) {
	// Shares analyticsFloor so a window means the same thing here as it does for the analytics
	// reports rendered beside this one. It previously defaulted days <= 0 to 30, which made All
	// Time show a month of latency next to all-time bandwidth on the same screen (#1565).
	since := analyticsFloor(days)

	rows, err := repo.conn.Query(`
		SELECT user_id, region, rtt_ms FROM region_probes WHERE day >= ?`, since)
	if err != nil {
		return nil, fmt.Errorf("region latency: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Per region, the best (lowest) RTT each user saw -- one figure per user, so the
	// distribution is over people. Per user, the best RTT across every region, which is what
	// says whether they had a good option at all.
	perRegion := map[string]map[string]int{}
	unreachable := map[string]map[string]bool{}
	bestPerUser := map[string]int{}

	for rows.Next() {
		var userID, region string
		var rtt sql.NullInt64
		if err := rows.Scan(&userID, &region, &rtt); err != nil {
			return nil, fmt.Errorf("region latency: %w", err)
		}
		if !rtt.Valid {
			if unreachable[region] == nil {
				unreachable[region] = map[string]bool{}
			}
			unreachable[region][userID] = true
			continue
		}
		ms := int(rtt.Int64)
		if perRegion[region] == nil {
			perRegion[region] = map[string]int{}
		}
		if seen, ok := perRegion[region][userID]; !ok || ms < seen {
			perRegion[region][userID] = ms
		}
		if seen, ok := bestPerUser[userID]; !ok || ms < seen {
			bestPerUser[userID] = ms
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("region latency: %w", err)
	}

	report := &RegionLatencyReport{Days: days, ThresholdMs: PoorLatencyThresholdMs}
	for _, best := range bestPerUser {
		if best > PoorLatencyThresholdMs {
			report.PoorlyServedUsers++
		}
	}

	regions := make([]string, 0, len(perRegion))
	for region := range perRegion {
		regions = append(regions, region)
	}
	for region := range unreachable {
		if _, ok := perRegion[region]; !ok {
			regions = append(regions, region)
		}
	}
	sort.Strings(regions)

	for _, region := range regions {
		samples := make([]int, 0, len(perRegion[region]))
		for _, ms := range perRegion[region] {
			samples = append(samples, ms)
		}
		sort.Ints(samples)
		report.Regions = append(report.Regions, RegionLatency{
			Region:      region,
			Users:       len(samples),
			MedianMs:    percentile(samples, 50),
			P90Ms:       percentile(samples, 90),
			Unreachable: len(unreachable[region]),
		})
	}
	return report, nil
}

// percentile returns the nearest-rank percentile of a sorted slice, or 0 when there is nothing
// to report. Nearest-rank rather than interpolated: with a handful of samples an interpolated
// p90 invents a value nobody measured, and these figures are read as "somebody experienced this".
func percentile(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
