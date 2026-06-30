package db

import (
	"database/sql"
	"time"
)

type SQLiteMagicLinkRepo struct {
	conn *sql.DB
}

func NewSQLiteMagicLinkRepo(conn *sql.DB) *SQLiteMagicLinkRepo {
	return &SQLiteMagicLinkRepo{conn: conn}
}

// CreateMagicLink saves a new magic link to the database.
func (repo *SQLiteMagicLinkRepo) CreateMagicLink(email, tokenHash, clientIP string, expiresAt time.Time) error {
	query := `INSERT INTO admin_magic_links (email, token_hash, client_ip, expires_at) VALUES (?, ?, ?, ?)`
	_, err := repo.conn.Exec(query, email, tokenHash, clientIP, expiresAt)
	return err
}

// GetMagicLink retrieves a magic link by its token hash.
func (repo *SQLiteMagicLinkRepo) GetMagicLink(tokenHash string) (*MagicLink, error) {
	query := `SELECT id, email, token_hash, client_ip, created_at, expires_at, used_at FROM admin_magic_links WHERE token_hash = ?`
	row := repo.conn.QueryRow(query, tokenHash)

	var link MagicLink
	var usedAt sql.NullTime
	err := row.Scan(&link.ID, &link.Email, &link.TokenHash, &link.ClientIP, &link.CreatedAt, &link.ExpiresAt, &usedAt)
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		link.UsedAt = &usedAt.Time
	}
	return &link, nil
}

// PruneExpiredMagicLinks deletes any magic links that have expired from the database.
func (repo *SQLiteMagicLinkRepo) PruneExpiredMagicLinks() error {
	_, err := repo.conn.Exec("DELETE FROM admin_magic_links WHERE expires_at < CURRENT_TIMESTAMP")
	return err
}

// MarkMagicLinkUsed marks a magic link as used.
func (repo *SQLiteMagicLinkRepo) MarkMagicLinkUsed(id int) error {
	query := `UPDATE admin_magic_links SET used_at = datetime('now') WHERE id = ?`
	_, err := repo.conn.Exec(query, id)
	return err
}

// InvalidateOtherMagicLinks expires all other unused magic links for a given email.
func (repo *SQLiteMagicLinkRepo) InvalidateOtherMagicLinks(email string, excludeID int) error {
	query := `UPDATE admin_magic_links SET expires_at = CURRENT_TIMESTAMP WHERE email = ? AND id != ? AND used_at IS NULL`
	_, err := repo.conn.Exec(query, email, excludeID)
	return err
}

// ListMagicLinks returns all magic links, ordered by newest first.
func (repo *SQLiteMagicLinkRepo) ListMagicLinks() ([]*MagicLink, error) {
	query := `SELECT id, email, client_ip, created_at, expires_at, used_at FROM admin_magic_links ORDER BY created_at DESC LIMIT 100`
	rows, err := repo.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var links []*MagicLink
	for rows.Next() {
		var link MagicLink
		var usedAt sql.NullTime
		if err := rows.Scan(&link.ID, &link.Email, &link.ClientIP, &link.CreatedAt, &link.ExpiresAt, &usedAt); err != nil {
			return nil, err
		}
		if usedAt.Valid {
			link.UsedAt = &usedAt.Time
		}
		links = append(links, &link)
	}
	return links, rows.Err()
}
