package db

import (
	"fmt"
	"sort"
	"time"

	"lfr-tunnel/pkg/regionvocab"
)

// How clients chose their gateway (#1922).
//
// The question this answers: "my US colleagues are served by the Irish node -- are they pinned,
// or did they measure it as closest?" Nothing could answer it, because a pinned client runs no
// latency probe and therefore sends an empty probe set -- identical on the wire to a client with
// reporting switched off and to one too old to send any. All three looked like "no data".
//
// Recorded per user per DAY rather than per session, matching region_probes: this counts people,
// and a user who reconnects fifty times must not outweigh one who connects once.

// RegionSourceCount is how many distinct users chose their gateway a given way.
type RegionSourceCount struct {
	Source string `json:"source"`
	Users  int    `json:"users"`
	// Pinned marks the sources that mean the client cannot move between gateways, so a
	// portal can call them out without re-deriving the rule. Only -server/env qualifies:
	// -region skips the probe but keeps failover.
	Pinned bool `json:"pinned"`
}

// RecordRegionSource stores how one user's client chose its gateway today.
//
// Upsert on (user_id, day, source): a reconnect by the same route refreshes its row rather than
// adding one, so the counts stay per-person, and every route a user took that day is kept.
//
// It used to upsert on (user_id, day), so the latest registration of the day replaced the rest
// (#2372). That was meant to let a user who removes -server stop counting as pinned that day,
// but it also erased failovers and failbacks, which are almost always followed by another
// registration -- one failback survived against the audit log's 43. Keeping every route means a
// user pinned at any point in the window counts as pinned in it, which is already what a
// multi-day window did across days; it now holds within the day as well.
func (repo *SQLiteRegionProbeRepo) RecordRegionSource(userID, source string, at time.Time) error {
	if userID == "" || source == "" {
		return nil
	}
	// Stored even when unrecognised, deliberately: a client newer than this gateway may send
	// a source this build has never heard of, and dropping it would hide exactly the clients
	// most worth noticing. GetRegionSources reports it separately rather than counting it as
	// something it is not.
	_, err := repo.conn.Exec(`
		INSERT INTO client_region_source (user_id, day, source, recorded_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, day, source) DO UPDATE SET recorded_at = excluded.recorded_at`,
		userID, at.UTC().Format("2006-01-02"), source, sqliteTime(at))
	if err != nil {
		return fmt.Errorf("record region source: %w", err)
	}
	return nil
}

// GetRegionSources counts distinct users per source over the last `days` days.
//
// Returns the declared vocabulary in its declared order, including sources nobody reported --
// a zero is information ("nobody is pinned") and omitting it would read as "not measured".
// Anything unrecognised is appended after, named rather than folded into a known bucket.
func (repo *SQLiteRegionProbeRepo) GetRegionSources(days int) ([]RegionSourceCount, error) {
	// Through the shared helper rather than open-coding a floor, so "All Time" cannot mean a
	// month here while it means all time in the report this is embedded in -- the exact shape of
	// #1565, which survived in this one function because handleNodePlacement rejects days=0
	// before it can arrive and made the divergence unreachable rather than absent (#1981).
	//
	// The day form: client_region_source.day is a bare "YYYY-MM-DD".
	since := analyticsDayFloor(days)

	rows, err := repo.conn.Query(`
		SELECT source, COUNT(DISTINCT user_id) FROM client_region_source
		WHERE day >= ? GROUP BY source`, since)
	if err != nil {
		return nil, fmt.Errorf("region sources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := map[string]int{}
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, fmt.Errorf("region sources (scan): %w", err)
		}
		counts[src] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("region sources (rows): %w", err)
	}

	out := make([]RegionSourceCount, 0, len(counts)+len(regionvocab.Sources))
	for _, s := range regionvocab.Sources {
		out = append(out, RegionSourceCount{Source: s, Users: counts[s], Pinned: regionvocab.IsPinned(s)})
		delete(counts, s)
	}
	unknown := make([]string, 0, len(counts))
	for s := range counts {
		unknown = append(unknown, s)
	}
	sort.Strings(unknown)
	for _, s := range unknown {
		out = append(out, RegionSourceCount{Source: s, Users: counts[s]})
	}
	return out, nil
}
