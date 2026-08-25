package db

import (
	"database/sql"
	"time"
)

// The IP blacklist holds two kinds of entry, told apart by whether they expire (#1353).
//
// A manual ban placed by an admin never expires: a person looked at the address and decided.
// An automatic ban from the rate limiter does expire, because it acts on a noisy signal --
// shared NAT, CGNAT, mobile carriers and corporate egress all look like one busy address --
// and because the enforcement point sits ahead of all routing, so a banned address cannot
// reach the admin API to undo it.
//
// Expired rows are kept rather than deleted. ban_count drives the escalating ban time for
// repeat offenders, and deleting the row on expiry would reset that to zero every time.

type SQLiteBlacklistRepo struct {
	conn *sql.DB
}

func NewSQLiteBlacklistRepo(conn *sql.DB) *SQLiteBlacklistRepo {
	return &SQLiteBlacklistRepo{conn: conn}
}

// AddBlacklistIP adds a permanent ban, which is what a manual admin ban is.
func (repo *SQLiteBlacklistRepo) AddBlacklistIP(ip, reason string) error {
	query := "INSERT OR IGNORE INTO ip_blacklist (ip_address, reason) VALUES (?, ?)"
	_, err := repo.conn.Exec(query, ip, reason)
	return err
}

// AddAutoBan records an automatic ban that expires, and returns the entry it wrote.
//
// The row is upserted rather than inserted: an address being auto-banned again already has a
// row, either still active or expired-but-retained, and its ban_count is what makes the next
// ban longer than the last.
//
// A duration of zero or less means the ban never expires, preserving the previous behaviour
// for anyone who configures it that way.
func (repo *SQLiteBlacklistRepo) AddAutoBan(ip, reason string, duration time.Duration, factor float64, maxDuration time.Duration) (*BlacklistEntry, error) {
	priorBans, err := repo.banCount(ip)
	if err != nil {
		return nil, err
	}

	entry := &BlacklistEntry{
		IPAddress: ip,
		Reason:    reason,
		CreatedAt: time.Now().UTC(),
		BanCount:  priorBans + 1,
	}

	if duration > 0 {
		expiry := time.Now().UTC().Add(EscalatedBanDuration(duration, factor, maxDuration, entry.BanCount))
		entry.ExpiresAt = &expiry
	}

	// banned_by is set to "system" so an automatic ban is distinguishable from a manual one
	// after the fact, without inferring it from whether an expiry happens to be set.
	_, err = repo.conn.Exec(`
		INSERT INTO ip_blacklist (ip_address, reason, banned_by, banned_at, expires_at, ban_count)
		VALUES (?, ?, 'system', ?, ?, ?)
		ON CONFLICT(ip_address) DO UPDATE SET
			reason = excluded.reason,
			banned_by = excluded.banned_by,
			banned_at = excluded.banned_at,
			expires_at = excluded.expires_at,
			ban_count = excluded.ban_count
	`, ip, reason, entry.CreatedAt, entry.ExpiresAt, entry.BanCount)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// EscalatedBanDuration is fail2ban's bantime.increment, as a pure function so it can be tested
// and reasoned about on its own: each repeat offence multiplies the base by factor, capped.
//
// banCount is 1 for a first offence, so a first ban is exactly the configured base duration.
func EscalatedBanDuration(base time.Duration, factor float64, maxDuration time.Duration, banCount int) time.Duration {
	if banCount < 1 {
		banCount = 1
	}
	// factor <= 1 disables escalation rather than shrinking the ban, which is the only
	// sensible reading of a misconfigured value.
	if factor <= 1 {
		return capDuration(base, maxDuration)
	}

	d := float64(base)
	for i := 1; i < banCount; i++ {
		d *= factor
		// Stop once the cap is passed. Without this a long-lived repeat offender overflows
		// float64 into +Inf, and the conversion below becomes undefined.
		if maxDuration > 0 && d >= float64(maxDuration) {
			return maxDuration
		}
	}
	return capDuration(time.Duration(d), maxDuration)
}

func capDuration(d, maxDuration time.Duration) time.Duration {
	if maxDuration > 0 && d > maxDuration {
		return maxDuration
	}
	return d
}

// banCount returns how many times this address has already been auto-banned, including bans
// that have since expired.
func (repo *SQLiteBlacklistRepo) banCount(ip string) (int, error) {
	var count int
	err := repo.conn.QueryRow("SELECT ban_count FROM ip_blacklist WHERE ip_address = ?", ip).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

// RemoveBlacklistIP removes an IP from the database blacklist.
func (repo *SQLiteBlacklistRepo) RemoveBlacklistIP(ip string) error {
	query := "DELETE FROM ip_blacklist WHERE ip_address = ?"
	_, err := repo.conn.Exec(query, ip)
	return err
}

// IsBlacklisted reports whether an IP is blocked right now. An expired row is not a block,
// even though it is still present for the sake of escalation.
func (repo *SQLiteBlacklistRepo) IsBlacklisted(ip string) (bool, error) {
	query := `
		SELECT 1 FROM ip_blacklist
		WHERE ip_address = ? AND (expires_at IS NULL OR expires_at > ?)
	`
	var dummy int
	err := repo.conn.QueryRow(query, ip, time.Now().UTC()).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListBlacklistedIPs returns the bans that are currently in force. This is what populates the
// in-memory cache, so an expired row must not appear here.
func (repo *SQLiteBlacklistRepo) ListBlacklistedIPs() ([]*BlacklistEntry, error) {
	query := `
		SELECT ip_address, reason, banned_at, expires_at, ban_count
		FROM ip_blacklist
		WHERE expires_at IS NULL OR expires_at > ?
		ORDER BY banned_at DESC
	`
	rows, err := repo.conn.Query(query, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []*BlacklistEntry
	for rows.Next() {
		var e BlacklistEntry
		var reason sql.NullString
		var expires sql.NullTime
		if err := rows.Scan(&e.IPAddress, &reason, &e.CreatedAt, &expires, &e.BanCount); err != nil {
			return nil, err
		}
		if reason.Valid {
			e.Reason = reason.String
		}
		if expires.Valid {
			t := expires.Time
			e.ExpiresAt = &t
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// PruneBlacklist removes expired bans that are older than the retention period, and returns
// how many went.
//
// Retention exists so escalation works across a gap: an address banned, released, and banned
// again a week later should be treated as a repeat offender. Only once it has stayed clean for
// the whole retention period is its history forgotten.
func (repo *SQLiteBlacklistRepo) PruneBlacklist(retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-retention)
	res, err := repo.conn.Exec(
		"DELETE FROM ip_blacklist WHERE expires_at IS NOT NULL AND expires_at <= ?", cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // the delete succeeded; only the count is unavailable
	}
	return n, nil
}
