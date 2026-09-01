package db

import (
	"database/sql"
	"time"
)

// PortalSession is a logged-in portal session, durable across a restart (#1304).
//
// Sessions used to live only in an in-memory map, so every restart -- including a routine
// deploy -- signed out every logged-in user. The sliding expiry that extends a session on each
// request was undermined by the same thing: the lifetime was carefully maintained and then
// discarded wholesale the next time the process stopped.
//
// TokenHash is a hash of the cookie value, never the value itself, on the same reasoning as
// PersonalAccessToken: a row is then useless to anyone who can read the database.
type PortalSession struct {
	TokenHash             string
	Email                 string
	ClientIP              string
	ViewAsRole            string
	PreviousLoginAt       *time.Time
	KilledPreviousSession bool
	CreatedAt             time.Time
	ExpiresAt             time.Time
	// SameSite is the cookie's SameSite mode as a string ("Strict"/"Lax"), recorded at login
	// (#1655). Empty for sessions created before the column existed.
	SameSite string
}

type SQLitePortalSessionRepo struct {
	conn *sql.DB
}

func NewSQLitePortalSessionRepo(conn *sql.DB) *SQLitePortalSessionRepo {
	return &SQLitePortalSessionRepo{conn: conn}
}

// UpsertPortalSession creates or refreshes a session. Refreshing rather than inserting matters
// because of the sliding expiry: the same session is written back on every request that
// extends it, and a plain insert would collide on the primary key each time.
func (repo *SQLitePortalSessionRepo) UpsertPortalSession(sess *PortalSession) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	_, err := repo.conn.Exec(`
		INSERT INTO portal_sessions
			(token_hash, email, client_ip, view_as_role, previous_login_at, killed_previous_session, created_at, expires_at, same_site)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token_hash) DO UPDATE SET
			email = excluded.email,
			client_ip = excluded.client_ip,
			view_as_role = excluded.view_as_role,
			previous_login_at = excluded.previous_login_at,
			killed_previous_session = excluded.killed_previous_session,
			expires_at = excluded.expires_at,
			-- Not overwritten with an empty value: a slide writes the whole row back, and a
			-- caller that did not carry the mode through would erase it and leave the session
			-- un-refreshable (#1655).
			same_site = CASE WHEN excluded.same_site = '' THEN portal_sessions.same_site ELSE excluded.same_site END
	`, sess.TokenHash, sess.Email, sess.ClientIP, sess.ViewAsRole,
		sess.PreviousLoginAt, boolToInt(sess.KilledPreviousSession),
		sess.CreatedAt.UTC(), sess.ExpiresAt.UTC(), sess.SameSite)
	return err
}

// GetPortalSession returns a session by token hash, or nil when there is none. An expired row
// is treated as absent and left for the pruner rather than deleted here, so a read stays a
// read -- a lookup on the hot path should not be taking write locks.
func (repo *SQLitePortalSessionRepo) GetPortalSession(tokenHash string) (*PortalSession, error) {
	row := repo.conn.QueryRow(`
		SELECT token_hash, email, COALESCE(client_ip, ''), COALESCE(view_as_role, ''),
		       previous_login_at, killed_previous_session, created_at, expires_at,
		       COALESCE(same_site, '')
		FROM portal_sessions
		WHERE token_hash = ? AND expires_at > datetime('now')
	`, tokenHash)

	var sess PortalSession
	var killed int
	var previousLogin sql.NullTime
	err := row.Scan(&sess.TokenHash, &sess.Email, &sess.ClientIP, &sess.ViewAsRole,
		&previousLogin, &killed, &sess.CreatedAt, &sess.ExpiresAt, &sess.SameSite)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if previousLogin.Valid {
		t := previousLogin.Time
		sess.PreviousLoginAt = &t
	}
	sess.KilledPreviousSession = killed != 0
	return &sess, nil
}

// DeletePortalSession removes a single session, for logout.
func (repo *SQLitePortalSessionRepo) DeletePortalSession(tokenHash string) error {
	_, err := repo.conn.Exec(`DELETE FROM portal_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeletePortalSessionsForEmail removes every session belonging to one account, which is what
// the strict-concurrency takeover needs: logging in elsewhere invalidates the older session,
// and that has to survive a restart too or the "killed" session comes back to life.
// Returns how many were removed, so a caller can report the takeover accurately after a
// restart -- when the previous session exists only in the database and not in the cache.
func (repo *SQLitePortalSessionRepo) DeletePortalSessionsForEmail(email string) (int64, error) {
	res, err := repo.conn.Exec(`DELETE FROM portal_sessions WHERE email = ?`, email)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // the delete succeeded; only the count is unavailable
	}
	return n, nil
}

// CountActivePortalSessions reports how many portal sessions are currently valid.
//
// Exists so an operator can answer "is anybody using this?" for the PORTAL before restarting the
// control plane. The drain endpoint already answers it for tunnels via local_leases, and the two
// audiences genuinely diverge -- on 2026-08-27 central had zero tunnels attached and one active
// portal session at the moment of a restart (#1455).
//
// Counts only unexpired rows. Expired ones are deleted on the cleanup cycle rather than
// immediately, so counting every row would overstate the number of people actually logged in.
func (repo *SQLitePortalSessionRepo) CountActivePortalSessions() (int, error) {
	var n int
	err := repo.conn.QueryRow(
		`SELECT COUNT(*) FROM portal_sessions WHERE expires_at > datetime('now')`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// PrunePortalSessions clears out expired rows. Called from the existing cleanup routine rather
// than on its own timer.
func (repo *SQLitePortalSessionRepo) PrunePortalSessions() (int64, error) {
	res, err := repo.conn.Exec(`DELETE FROM portal_sessions WHERE expires_at <= datetime('now')`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // the delete succeeded; only the count is unavailable
	}
	return n, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
