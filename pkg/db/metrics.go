package db

import (
	"database/sql"
	"time"
)

type SQLiteMetricRepo struct {
	conn *sql.DB
}

func NewSQLiteMetricRepo(conn *sql.DB) *SQLiteMetricRepo {
	return &SQLiteMetricRepo{conn: conn}
}

// RecordTunnelMetric writes a single bandwidth metric to the database.
func (repo *SQLiteMetricRepo) RecordTunnelMetric(m *TunnelMetric) error {
	nodeID := m.NodeID
	if nodeID == "" {
		nodeID = "control"
	}
	connectedAt := m.ConnectedAt
	if connectedAt.IsZero() {
		connectedAt = time.Now().UTC()
	}
	recordedAt := m.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	_, err := repo.conn.Exec(`
		INSERT INTO tunnel_metrics (user_id, subdomain_prefix, full_host, bytes_in, bytes_out, connected_at, recorded_at, node_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, m.UserID, m.SubdomainPrefix, m.FullHost, m.BytesIn, m.BytesOut, connectedAt.Format("2006-01-02 15:04:05"), recordedAt.Format("2006-01-02 15:04:05"), nodeID)
	return err
}

// GetGlobalAnalytics retrieves system-wide bandwidth stats for the last N days.
func (repo *SQLiteMetricRepo) GetGlobalAnalytics(days int) (*GlobalAnalytics, error) {
	timeLimit := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	dailyQuery := `
		SELECT COALESCE(strftime('%Y-%m-%d', recorded_at), CASE WHEN length(recorded_at) >= 10 AND substr(recorded_at, 5, 1) = '-' AND substr(recorded_at, 8, 1) = '-' THEN substr(recorded_at, 1, 10) END) as d, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE recorded_at >= ?
		GROUP BY d
		ORDER BY d ASC
	`
	rows, err := repo.conn.Query(dailyQuery, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	daily := make([]DailyBandwidth, 0)
	for rows.Next() {
		var dbw DailyBandwidth
		var dateNull sql.NullString
		if err := rows.Scan(&dateNull, &dbw.BytesIn, &dbw.BytesOut); err != nil {
			return nil, err
		}
		if dateNull.Valid {
			dbw.Date = dateNull.String
		} else {
			dbw.Date = "Unknown"
		}
		daily = append(daily, dbw)
	}

	topQuery := `
		SELECT COALESCE(u.email, m.user_id), SUM(m.bytes_in), SUM(m.bytes_out)
		FROM tunnel_metrics m
		LEFT JOIN users u ON m.user_id = u.id
		WHERE m.recorded_at >= ?
		GROUP BY m.user_id
		ORDER BY (SUM(m.bytes_in) + SUM(m.bytes_out)) DESC
		LIMIT 10
	`
	topRows, err := repo.conn.Query(topQuery, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = topRows.Close() }()

	top := make([]UserBandwidth, 0)
	for topRows.Next() {
		var ub UserBandwidth
		var emailNull sql.NullString
		if err := topRows.Scan(&emailNull, &ub.BytesIn, &ub.BytesOut); err != nil {
			return nil, err
		}
		if emailNull.Valid {
			ub.Email = emailNull.String
		} else {
			ub.Email = "Unknown"
		}
		top = append(top, ub)
	}

	portalQuery := `
		SELECT target_id, COUNT(*)
		FROM admin_audit_log
		WHERE action = 'portal.visit' AND created_at >= ?
		GROUP BY target_id
	`
	portalRows, err := repo.conn.Query(portalQuery, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = portalRows.Close() }()

	portalStats := make([]PortalUsageStats, 0)
	for portalRows.Next() {
		var ps PortalUsageStats
		var detailsNull sql.NullString
		if err := portalRows.Scan(&detailsNull, &ps.Count); err != nil {
			return nil, err
		}
		if detailsNull.Valid {
			ps.Version = detailsNull.String
		} else {
			ps.Version = "Unknown"
		}
		portalStats = append(portalStats, ps)
	}

	return &GlobalAnalytics{Daily: daily, TopUsers: top, PortalStats: portalStats}, nil
}

// GetUserAnalytics retrieves bandwidth stats for a specific user for the last N days.
func (repo *SQLiteMetricRepo) GetUserAnalytics(userID string, days int) (*UserAnalytics, error) {
	timeLimit := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	dailyQuery := `
		SELECT COALESCE(strftime('%Y-%m-%d', recorded_at), CASE WHEN length(recorded_at) >= 10 AND substr(recorded_at, 5, 1) = '-' AND substr(recorded_at, 8, 1) = '-' THEN substr(recorded_at, 1, 10) END) as d, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE user_id = ? AND recorded_at >= ?
		GROUP BY d
		ORDER BY d ASC
	`
	rows, err := repo.conn.Query(dailyQuery, userID, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	daily := make([]DailyBandwidth, 0)
	for rows.Next() {
		var dbw DailyBandwidth
		var dateNull sql.NullString
		if err := rows.Scan(&dateNull, &dbw.BytesIn, &dbw.BytesOut); err != nil {
			return nil, err
		}
		if dateNull.Valid {
			dbw.Date = dateNull.String
		} else {
			dbw.Date = "Unknown"
		}
		daily = append(daily, dbw)
	}

	tunnelQuery := `
		SELECT full_host, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE user_id = ? AND recorded_at >= ?
		GROUP BY full_host
		ORDER BY (SUM(bytes_in) + SUM(bytes_out)) DESC
	`
	tunnelRows, err := repo.conn.Query(tunnelQuery, userID, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tunnelRows.Close() }()

	tunnels := make([]TunnelBandwidth, 0)
	for tunnelRows.Next() {
		var tb TunnelBandwidth
		if err := tunnelRows.Scan(&tb.FullHost, &tb.BytesIn, &tb.BytesOut); err != nil {
			return nil, err
		}
		tunnels = append(tunnels, tb)
	}

	return &UserAnalytics{Daily: daily, Tunnels: tunnels}, nil
}

// GetClientVersionStats groups users by client version and OS.
func (repo *SQLiteMetricRepo) GetClientVersionStats() ([]ClientVersionStats, error) {
	rows, err := repo.conn.Query(`
		SELECT 
			COALESCE(NULLIF(last_client_version, ''), 'Unknown'),
			COALESCE(NULLIF(last_client_os, ''), 'Unknown'),
			COUNT(*)
		FROM users
		WHERE last_client_version IS NOT NULL AND last_client_version != ''
		GROUP BY last_client_version, last_client_os
		ORDER BY last_client_version DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stats []ClientVersionStats
	for rows.Next() {
		var stat ClientVersionStats
		if err := rows.Scan(&stat.Version, &stat.OS, &stat.Count); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

// RecordGatewayStart handles the startup lifecycle of the gateway server.
// It closes the previous run (if it was left open due to a crash or restart) by setting its end_time
// to the current time, and then inserts a new run record.
func (repo *SQLiteMetricRepo) RecordGatewayStart(startTime time.Time) error {

	queryClose := "UPDATE gateway_runs SET end_time = ? WHERE end_time IS NULL"
	_, err := repo.conn.Exec(queryClose, startTime)
	if err != nil {
		return err
	}

	queryInsert := "INSERT INTO gateway_runs (start_time, end_time) VALUES (?, NULL)"
	_, err = repo.conn.Exec(queryInsert, startTime)
	return err
}

// RecordGatewayCleanShutdown updates the current run's end_time to the shutdown time.
func (repo *SQLiteMetricRepo) RecordGatewayCleanShutdown() error {
	queryClose := "UPDATE gateway_runs SET end_time = ? WHERE end_time IS NULL"
	_, err := repo.conn.Exec(queryClose, time.Now())
	return err
}

// GetGatewayRuns retrieves the historical gateway runs up to a limit.
func (repo *SQLiteMetricRepo) GetGatewayRuns(limit int) ([]*GatewayRun, error) {
	rows, err := repo.conn.Query(`
		SELECT id, start_time, end_time
		FROM gateway_runs
		ORDER BY start_time DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var list []*GatewayRun
	for rows.Next() {
		var r GatewayRun
		var endTime sql.NullTime
		err := rows.Scan(&r.ID, &r.StartTime, &endTime)
		if err != nil {
			return nil, err
		}
		if endTime.Valid {
			r.EndTime = &endTime.Time
		}
		list = append(list, &r)
	}
	return list, nil
}
