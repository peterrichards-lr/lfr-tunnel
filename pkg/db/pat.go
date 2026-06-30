package db

import (
	"database/sql"
	"time"
)

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

	query := `INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := repo.conn.Exec(query, pat.UserID, pat.TokenHash, pat.TokenPrefix, pat.Name, expiresVal, revokedVal, lastUsedVal, pat.CreatedAt)
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
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE token_hash = ?`
	row := repo.conn.QueryRow(query, hash)

	var pat PersonalAccessToken
	var expires, revoked, lastUsed sql.NullTime

	err := row.Scan(&pat.ID, &pat.UserID, &pat.TokenHash, &pat.TokenPrefix, &pat.Name, &expires, &revoked, &lastUsed, &pat.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
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

	return &pat, nil
}

// ListPATs returns all PATs belonging to a specific user.
func (repo *SQLitePATRepo) ListPATs(userID string) ([]*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE user_id = ? ORDER BY created_at DESC`
	rows, err := repo.conn.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pats []*PersonalAccessToken
	for rows.Next() {
		var pat PersonalAccessToken
		var expires, revoked, lastUsed sql.NullTime

		err := rows.Scan(&pat.ID, &pat.UserID, &pat.TokenHash, &pat.TokenPrefix, &pat.Name, &expires, &revoked, &lastUsed, &pat.CreatedAt)
		if err != nil {
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

		pats = append(pats, &pat)
	}
	return pats, nil
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
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens ORDER BY created_at DESC`
	rows, err := repo.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pats []*PersonalAccessToken
	for rows.Next() {
		var pat PersonalAccessToken
		var expires, revoked, lastUsed sql.NullTime
		if err := rows.Scan(&pat.ID, &pat.UserID, &pat.TokenHash, &pat.TokenPrefix, &pat.Name, &expires, &revoked, &lastUsed, &pat.CreatedAt); err != nil {
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
		pats = append(pats, &pat)
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
