package db

import (
	"database/sql"
	"time"
)

// A Personal Access Token's permanence request moves through these states (#2267). Only
// meaningful where never_expires.tokens is "approval"; under the other two policies a token is
// either created permanent or cannot be, and nothing is ever pending.
const (
	// PATPermanenceNone is the zero value and means nobody has asked. It is also what every
	// token created before #2267 carries, which is correct: none of them asked either.
	PATPermanenceNone = ""
	// PATPermanencePending means the holder has asked and no admin has decided.
	PATPermanencePending = "pending"
	// PATPermanenceGranted records that an admin said yes. Kept after the grant rather than
	// cleared back to none, so the audit question "who made this token permanent, and was it
	// asked for" has an answer on the row itself.
	PATPermanenceGranted = "granted"
	// PATPermanenceDenied means an admin said no.
	//
	// A real state, not the absence of one. Without it a denied request is indistinguishable
	// from one nobody has looked at yet: the holder is told nothing, sees the option still
	// offered, and asks again -- and the admin cannot tell a fresh request from the one they
	// already refused.
	PATPermanenceDenied = "denied"
)

// patColumns is the single spelling of the SELECT list, shared by the three queries that read
// tokens. They were three separate copies, and adding a column meant remembering all three --
// the way a column comes to exist in the model, be written on create, and read back empty from
// one listing nobody checked.
const patColumns = `id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at, permanence_state`

// patScanner is satisfied by both *sql.Row and *sql.Rows.
type patScanner interface {
	Scan(dest ...interface{}) error
}

// scanPAT reads one row selected with patColumns.
func scanPAT(sc patScanner) (*PersonalAccessToken, error) {
	var pat PersonalAccessToken
	var expires, revoked, lastUsed sql.NullTime
	var permanence sql.NullString
	if err := sc.Scan(&pat.ID, &pat.UserID, &pat.TokenHash, &pat.TokenPrefix, &pat.Name,
		&expires, &revoked, &lastUsed, &pat.CreatedAt, &permanence); err != nil {
		return nil, err
	}
	if expires.Valid {
		pat.ExpiresAt = &expires.Time
	}
	if revoked.Valid {
		pat.RevokedAt = &revoked.Time
	}
	if lastUsed.Valid {
		pat.LastUsedAt = &lastUsed.Time
	}
	if permanence.Valid {
		pat.PermanenceState = permanence.String
	}
	return &pat, nil
}

type SQLitePATRepo struct {
	conn *sql.DB
}

func NewSQLitePATRepo(conn *sql.DB) *SQLitePATRepo {
	return &SQLitePATRepo{conn: conn}
}

// CreatePAT generates a personal access token entry in the database.
func (repo *SQLitePATRepo) CreatePAT(pat *PersonalAccessToken) error {
	if pat.CreatedAt.IsZero() {
		pat.CreatedAt = time.Now().UTC()
	}

	var expiresVal interface{}
	if pat.ExpiresAt != nil {
		expiresVal = *pat.ExpiresAt
	}

	var revokedVal interface{}
	if pat.RevokedAt != nil {
		revokedVal = *pat.RevokedAt
	}

	var lastUsedVal interface{}
	if pat.LastUsedAt != nil {
		lastUsedVal = *pat.LastUsedAt
	}

	query := `INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at, permanence_state)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := repo.conn.Exec(query, pat.UserID, pat.TokenHash, pat.TokenPrefix, pat.Name, expiresVal, revokedVal, lastUsedVal, pat.CreatedAt, pat.PermanenceState)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	pat.ID = id
	return nil
}

// GetPATByHash looks up a PAT by its SHA-256 hash.
func (repo *SQLitePATRepo) GetPATByHash(hash string) (*PersonalAccessToken, error) {
	row := repo.conn.QueryRow(`SELECT `+patColumns+` FROM personal_access_tokens WHERE token_hash = ?`, hash)
	pat, err := scanPAT(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return pat, nil
}

// GetPATByID looks up a PAT by its primary key.
//
// Added for the permanence-request flow (#2267), which acts on one token by id and reads back
// what it changed. Every caller before it worked from ListPATs and filtered in Go, which is fine
// for a user's ten tokens and wrong for an admin acting on one out of every token on the gateway.
func (repo *SQLitePATRepo) GetPATByID(patID int64) (*PersonalAccessToken, error) {
	row := repo.conn.QueryRow(`SELECT `+patColumns+` FROM personal_access_tokens WHERE id = ?`, patID)
	pat, err := scanPAT(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return pat, nil
}

// ListPATs returns all PATs belonging to a specific user.
func (repo *SQLitePATRepo) ListPATs(userID string) ([]*PersonalAccessToken, error) {
	rows, err := repo.conn.Query(`SELECT `+patColumns+` FROM personal_access_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pats []*PersonalAccessToken
	for rows.Next() {
		pat, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		pats = append(pats, pat)
	}
	return pats, rows.Err()
}

// RevokePAT marks a PAT as revoked.
func (repo *SQLitePATRepo) RevokePAT(patID int64) error {
	now := time.Now().UTC()
	query := `UPDATE personal_access_tokens SET revoked_at = ? WHERE id = ?`
	res, err := repo.conn.Exec(query, now, patID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePATUsed updates the last_used_at field for audit tracking.
func (repo *SQLitePATRepo) UpdatePATUsed(patID int64) error {
	now := time.Now().UTC()
	query := `UPDATE personal_access_tokens SET last_used_at = ? WHERE id = ?`
	res, err := repo.conn.Exec(query, now, patID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePATExpiry updates the expires_at field of a PAT.
func (repo *SQLitePATRepo) UpdatePATExpiry(patID int64, expiresAt *time.Time) error {
	query := `UPDATE personal_access_tokens SET expires_at = ? WHERE id = ?`
	res, err := repo.conn.Exec(query, expiresAt, patID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAllPATs returns all PATs across all users (admin view).
func (repo *SQLitePATRepo) ListAllPATs() ([]*PersonalAccessToken, error) {
	rows, err := repo.conn.Query(`SELECT ` + patColumns + ` FROM personal_access_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pats []*PersonalAccessToken
	for rows.Next() {
		pat, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		pats = append(pats, pat)
	}
	return pats, rows.Err()
}

// TransitionPATPermanenceState moves a token from one permanence state to another, and reports
// ErrStateChanged when it is not in the state the caller believed.
//
// One conditional statement rather than read-check-write. SetMaxOpenConns(1) serialises
// STATEMENTS, not sequences: two admins deciding the same request each take the connection in
// turn for their own read, both see "pending", and both proceed. The worst outcome is a row
// recorded `denied` while its expiry has been removed -- refused on paper, permanent in fact,
// out of the queue, with two contradictory audit entries (#2267 review).
//
// Unlikely on a single-node gateway with two admins and no bulk action, and cheap enough to
// close that the argument for leaving it was never very good.
func (repo *SQLitePATRepo) TransitionPATPermanenceState(patID int64, from, to string) error {
	res, err := repo.conn.Exec(
		`UPDATE personal_access_tokens SET permanence_state = ? WHERE id = ? AND permanence_state = ?`,
		to, patID, from)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Either the token is gone or somebody else decided first. The caller distinguishes
		// them with a read; from here they are the same answer -- this write did not happen.
		return ErrStateChanged
	}
	return nil
}

// SetPATPermanenceState records where a token's permanence request has got to, unconditionally.
func (repo *SQLitePATRepo) SetPATPermanenceState(patID int64, state string) error {
	res, err := repo.conn.Exec(`UPDATE personal_access_tokens SET permanence_state = ? WHERE id = ?`, state, patID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPATPermanenceRequests returns every token waiting on an admin decision.
//
// Revoked tokens are excluded: a request to make a deleted credential permanent is not a decision
// anybody needs to make, and leaving them in means a queue that grows with abandoned rows and
// stops being read.
func (repo *SQLitePATRepo) ListPATPermanenceRequests() ([]*PersonalAccessToken, error) {
	rows, err := repo.conn.Query(`SELECT `+patColumns+` FROM personal_access_tokens
		WHERE permanence_state = ? AND revoked_at IS NULL ORDER BY created_at ASC`, PATPermanencePending)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pats []*PersonalAccessToken
	for rows.Next() {
		pat, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		pats = append(pats, pat)
	}
	return pats, rows.Err()
}

// PruneExpiredOrRevokedPATs deletes revoked or expired PATs that exceed the retention period.
func (repo *SQLitePATRepo) PruneExpiredOrRevokedPATs(retentionDays int) error {
	if retentionDays < 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	query := `DELETE FROM personal_access_tokens 
	          WHERE (revoked_at IS NOT NULL AND revoked_at < ?) 
	             OR (expires_at IS NOT NULL AND expires_at < ?)`
	_, err := repo.conn.Exec(query, cutoff, cutoff)
	return err
}
