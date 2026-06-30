package db

import (
	"database/sql"
)

type SQLiteBlacklistRepo struct {
	conn *sql.DB
}

func NewSQLiteBlacklistRepo(conn *sql.DB) *SQLiteBlacklistRepo {
	return &SQLiteBlacklistRepo{conn: conn}
}

// AddBlacklistIP adds an IP to the database blacklist.
func (repo *SQLiteBlacklistRepo) AddBlacklistIP(ip, reason string) error {
	query := "INSERT OR IGNORE INTO ip_blacklist (ip_address, reason) VALUES (?, ?)"
	_, err := repo.conn.Exec(query, ip, reason)
	return err
}

// RemoveBlacklistIP removes an IP from the database blacklist.
func (repo *SQLiteBlacklistRepo) RemoveBlacklistIP(ip string) error {
	query := "DELETE FROM ip_blacklist WHERE ip_address = ?"
	_, err := repo.conn.Exec(query, ip)
	return err
}

// IsBlacklisted checks if an IP is currently blacklisted.
func (repo *SQLiteBlacklistRepo) IsBlacklisted(ip string) (bool, error) {
	query := "SELECT 1 FROM ip_blacklist WHERE ip_address = ?"
	var dummy int
	err := repo.conn.QueryRow(query, ip).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListBlacklistedIPs returns all blacklisted IPs.
func (repo *SQLiteBlacklistRepo) ListBlacklistedIPs() ([]*BlacklistEntry, error) {
	query := "SELECT ip_address, reason, banned_at FROM ip_blacklist ORDER BY banned_at DESC"
	rows, err := repo.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []*BlacklistEntry
	for rows.Next() {
		var e BlacklistEntry
		var reason sql.NullString
		if err := rows.Scan(&e.IPAddress, &reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		if reason.Valid {
			e.Reason = reason.String
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
