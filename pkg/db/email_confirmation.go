package db

import (
	"database/sql"
	"errors"
)

// RecordEmailConfirmed notes that the gateway has itself confirmed userID's mailbox (#2472):
// the person opened a link sent to it. method says how ("sso_link", "magic_link"). A second
// confirmation keeps the first row; it is the earliest proof that matters.
func (d *DB) RecordEmailConfirmed(userID, method string) error {
	_, err := d.conn.Exec(`INSERT OR IGNORE INTO user_email_confirmations (user_id, method) VALUES (?, ?)`, userID, method)
	return err
}

// EmailConfirmed reports whether RecordEmailConfirmed has been called for userID.
func (d *DB) EmailConfirmed(userID string) (bool, error) {
	var one int
	err := d.conn.QueryRow(`SELECT 1 FROM user_email_confirmations WHERE user_id = ?`, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
