package db

import (
	"database/sql"
	"time"
)

// Acknowledgement is one record of a user accepting a specific version of a specific
// document (#1707).
//
// The table behind this is APPEND-ONLY. There is deliberately no update or delete
// method on the repository below, and nothing anywhere overwrites a row. Consent is
// only worth recording if "what did this user agree to, and when" stays answerable
// after the document changes -- and a single mutable `policy_consent_at` column, which
// is what this replaces, loses precisely that. `users.policy_consent_at` is still
// written at registration and left alone here; it is the pre-#1707 record and remains
// true of the version that was current when it was stamped.
type Acknowledgement struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	DocumentID string    `json:"document_id"`
	Version    string    `json:"version"`
	AcceptedAt time.Time `json:"accepted_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
}

// SQLiteAcknowledgementRepo is the SQLite implementation of AcknowledgementRepository.
type SQLiteAcknowledgementRepo struct {
	conn *sql.DB
}

// NewSQLiteAcknowledgementRepo constructs the repository over an open connection.
func NewSQLiteAcknowledgementRepo(conn *sql.DB) *SQLiteAcknowledgementRepo {
	return &SQLiteAcknowledgementRepo{conn: conn}
}

// RecordAcknowledgement appends one acceptance. Calling it twice for the same
// (user, document, version) stores two rows on purpose: re-accepting is an event, and
// collapsing the second into the first would discard when it happened.
func (repo *SQLiteAcknowledgementRepo) RecordAcknowledgement(a *Acknowledgement) error {
	if a.AcceptedAt.IsZero() {
		a.AcceptedAt = time.Now().UTC()
	}
	res, err := repo.conn.Exec(`
		INSERT INTO user_acknowledgements (user_id, document_id, version, accepted_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)
	`, a.UserID, a.DocumentID, a.Version, a.AcceptedAt, a.IP, a.UserAgent)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil {
		a.ID = id
	}
	return nil
}

// HasAcknowledged reports whether this exact version has ever been accepted.
//
// Exact match rather than ">= version" by design: versions here are opaque labels an
// operator chooses (a date, a hash, "2"), so they carry no ordering the database could
// compare. Anything not accepted is outstanding.
func (repo *SQLiteAcknowledgementRepo) HasAcknowledged(userID, documentID, version string) (bool, error) {
	var n int
	err := repo.conn.QueryRow(`
		SELECT COUNT(1) FROM user_acknowledgements
		WHERE user_id = ? AND document_id = ? AND version = ?
	`, userID, documentID, version).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListAcknowledgements returns a user's full acceptance history, newest first. This is
// the audit trail; it is never filtered by the current version.
func (repo *SQLiteAcknowledgementRepo) ListAcknowledgements(userID string) ([]*Acknowledgement, error) {
	rows, err := repo.conn.Query(`
		SELECT id, user_id, document_id, version, accepted_at, ip, user_agent
		FROM user_acknowledgements
		WHERE user_id = ?
		ORDER BY accepted_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	// Any error that ends iteration early surfaces through rows.Err(), which is checked
	// below; reporting Close's copy of it from a defer would mask the returned one.
	defer func() { _ = rows.Close() }()

	out := make([]*Acknowledgement, 0)
	for rows.Next() {
		a := &Acknowledgement{}
		var ip, ua sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.DocumentID, &a.Version, &a.AcceptedAt, &ip, &ua); err != nil {
			return nil, err
		}
		a.IP = ip.String
		a.UserAgent = ua.String
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// RecordFirstSeen stamps when this user first had a given document version put in front
// of them, and returns the stamp that is now stored.
//
// It is idempotent: the first call wins and every later one returns that same instant.
// This is what makes the grace window run from the user's own first sight rather than
// from the document's effective date (#1707) -- somebody returning from two weeks of
// leave gets the full window, not a deadline that expired while they were away.
//
// `INSERT ... ON CONFLICT DO NOTHING` then `SELECT` rather than a read-then-write: the
// portal and the client can both reach this concurrently for the same user, and a
// check-then-insert would race into a duplicate-key error or, worse, a moved deadline.
func (repo *SQLiteAcknowledgementRepo) RecordFirstSeen(userID, documentID, version string, at time.Time) (time.Time, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := repo.conn.Exec(`
		INSERT INTO user_acknowledgement_notices (user_id, document_id, version, first_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, document_id, version) DO NOTHING
	`, userID, documentID, version, at.UTC()); err != nil {
		return time.Time{}, err
	}
	return repo.GetFirstSeen(userID, documentID, version)
}

// GetFirstSeen returns the recorded first-sight instant, or the zero time when the user
// has not been shown this version yet. A zero time is not an error: it is the normal
// state for everyone who has not logged in or started a client since the version was
// published, and callers treat it as "no deadline has started".
func (repo *SQLiteAcknowledgementRepo) GetFirstSeen(userID, documentID, version string) (time.Time, error) {
	var t time.Time
	err := repo.conn.QueryRow(`
		SELECT first_seen_at FROM user_acknowledgement_notices
		WHERE user_id = ? AND document_id = ? AND version = ?
	`, userID, documentID, version).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// MarkWarningNotified records that the warning-window email has gone out for this
// version, and reports whether this call is the one that claimed it. A second caller
// gets false, so the notifier can be safe to run on every tick without mailing anybody
// twice.
func (repo *SQLiteAcknowledgementRepo) MarkWarningNotified(userID, documentID, version string) (bool, error) {
	res, err := repo.conn.Exec(`
		UPDATE user_acknowledgement_notices
		SET warning_sent_at = ?
		WHERE user_id = ? AND document_id = ? AND version = ? AND warning_sent_at IS NULL
	`, time.Now().UTC(), userID, documentID, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListPendingWarnings returns the users whose first sight of documentID/version is at or
// before cutoff and who have not yet been sent the warning email. The caller still has to
// check whether each has since accepted -- this narrows the scan, it does not decide.
func (repo *SQLiteAcknowledgementRepo) ListPendingWarnings(documentID, version string, cutoff time.Time) ([]string, error) {
	rows, err := repo.conn.Query(`
		SELECT user_id FROM user_acknowledgement_notices
		WHERE document_id = ? AND version = ? AND warning_sent_at IS NULL AND first_seen_at <= ?
	`, documentID, version, cutoff.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
