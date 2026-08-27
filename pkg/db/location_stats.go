package db

import "database/sql"

// LocationStat is one row of the anonymous geographic aggregate (#1152): a bucket, and
// how many distinct users were seen in it during a period.
//
// There is deliberately no user column here and none in the table behind it. That is
// what makes the aggregate anonymous rather than pseudonymous, and it is a property of
// the schema rather than of the code that happens to write it -- so it cannot be
// undone by a later caller passing one more field.
type LocationStat struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

// UpsertLocationStats writes the counts for one period.
//
// The count is raised, never lowered (`MAX`). A restart mid-period loses the in-memory
// set the cardinality is computed from, so the rebuilt set starts empty and would
// otherwise overwrite a larger count with a smaller one. Taking the maximum keeps the
// stored value monotonic within a period: after a restart it can under-report, never
// over-report, and at no point does recovering the true figure require having kept who
// was counted.
func (repo *SQLiteMetricRepo) UpsertLocationStats(period string, stats []LocationStat) error {
	if period == "" || len(stats) == 0 {
		return nil
	}
	tx, err := repo.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck

	stmt, err := tx.Prepare(`
		INSERT INTO location_stats (period, bucket, count, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(period, bucket) DO UPDATE SET
			count = MAX(location_stats.count, excluded.count),
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }() //nolint:errcheck

	for _, s := range stats {
		if s.Bucket == "" || s.Count <= 0 {
			continue
		}
		if _, err := stmt.Exec(period, s.Bucket, s.Count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetLocationStats returns the buckets recorded for period, largest first. An empty
// period asks for the most recent one on record.
func (repo *SQLiteMetricRepo) GetLocationStats(period string) (string, []LocationStat, error) {
	if period == "" {
		var latest sql.NullString
		if err := repo.conn.QueryRow(`SELECT MAX(period) FROM location_stats`).Scan(&latest); err != nil {
			return "", nil, err
		}
		if !latest.Valid || latest.String == "" {
			return "", []LocationStat{}, nil
		}
		period = latest.String
	}

	rows, err := repo.conn.Query(`
		SELECT bucket, count FROM location_stats
		WHERE period = ?
		ORDER BY count DESC, bucket ASC
	`, period)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck

	stats := make([]LocationStat, 0)
	for rows.Next() {
		var s LocationStat
		if err := rows.Scan(&s.Bucket, &s.Count); err != nil {
			return "", nil, err
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return period, stats, nil
}
