package db

import (
	"database/sql"
	"errors"
	"time"
)

type SQLiteInviteRepo struct {
	conn *sql.DB
}

func NewSQLiteInviteRepo(conn *sql.DB) *SQLiteInviteRepo {
	return &SQLiteInviteRepo{conn: conn}
}

// CreateGuestInvitation saves a new invitation.
func (repo *SQLiteInviteRepo) CreateGuestInvitation(invite *GuestInvitation) error {
	if invite.CreatedAt.IsZero() {
		invite.CreatedAt = time.Now().UTC()
	}
	res, err := repo.conn.Exec(`
		INSERT INTO guest_invitations (token, subdomain, domain, name, email, expires_at, created_by, claimed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, invite.Token, invite.Subdomain, invite.Domain, invite.Name, invite.Email, invite.ExpiresAt, invite.CreatedBy, invite.ClaimedAt, invite.CreatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	invite.ID = id
	return nil
}

// GetGuestInvitationByToken retrieves an invitation by token.
func (repo *SQLiteInviteRepo) GetGuestInvitationByToken(token string) (*GuestInvitation, error) {
	var invite GuestInvitation
	var claimedAt sql.NullTime

	err := repo.conn.QueryRow(`
		SELECT id, token, subdomain, domain, name, email, expires_at, created_by, claimed_at, created_at
		FROM guest_invitations
		WHERE token = ?
	`, token).Scan(&invite.ID, &invite.Token, &invite.Subdomain, &invite.Domain, &invite.Name, &invite.Email, &invite.ExpiresAt, &invite.CreatedBy, &claimedAt, &invite.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if claimedAt.Valid {
		invite.ClaimedAt = &claimedAt.Time
	}
	return &invite, nil
}

// MarkGuestInvitationClaimed marks an invitation as claimed.
func (repo *SQLiteInviteRepo) MarkGuestInvitationClaimed(token string) error {
	_, err := repo.conn.Exec(`
		UPDATE guest_invitations
		SET claimed_at = ?
		WHERE token = ?
	`, time.Now().UTC(), token)
	return err
}

// ListGuestInvitationsByCreator lists all guest invitations created by a user.
func (repo *SQLiteInviteRepo) ListGuestInvitationsByCreator(createdBy string) ([]*GuestInvitation, error) {
	rows, err := repo.conn.Query(`
		SELECT id, token, subdomain, domain, name, email, expires_at, created_by, claimed_at, created_at
		FROM guest_invitations
		WHERE created_by = ?
		ORDER BY created_at DESC
	`, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var list []*GuestInvitation
	for rows.Next() {
		var invite GuestInvitation
		var claimedAt sql.NullTime
		err := rows.Scan(&invite.ID, &invite.Token, &invite.Subdomain, &invite.Domain, &invite.Name, &invite.Email, &invite.ExpiresAt, &invite.CreatedBy, &claimedAt, &invite.CreatedAt)
		if err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			invite.ClaimedAt = &claimedAt.Time
		}
		list = append(list, &invite)
	}
	return list, nil
}

// DeleteGuestInvitation deletes an invitation by ID and also cleans up any associated SubdomainACL entry.
func (repo *SQLiteInviteRepo) DeleteGuestInvitation(id int64) error {
	var token, subdomain, domain string
	err := repo.conn.QueryRow("SELECT token, subdomain, domain FROM guest_invitations WHERE id = ?", id).Scan(&token, &subdomain, &domain)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	tx, err := repo.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.Exec("DELETE FROM guest_invitations WHERE id = ?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM subdomain_acl WHERE subdomain = ? AND domain = ? AND identity = ?", subdomain, domain, "guest:"+token)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ListAllGuestInvitations lists all invitations in the system.
func (repo *SQLiteInviteRepo) ListAllGuestInvitations() ([]*GuestInvitation, error) {
	rows, err := repo.conn.Query(`
		SELECT id, token, subdomain, domain, name, email, expires_at, created_by, claimed_at, created_at
		FROM guest_invitations
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var list []*GuestInvitation
	for rows.Next() {
		var invite GuestInvitation
		var claimedAt sql.NullTime
		err := rows.Scan(&invite.ID, &invite.Token, &invite.Subdomain, &invite.Domain, &invite.Name, &invite.Email, &invite.ExpiresAt, &invite.CreatedBy, &claimedAt, &invite.CreatedAt)
		if err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			invite.ClaimedAt = &claimedAt.Time
		}
		list = append(list, &invite)
	}
	return list, nil
}

// GetGuestInvitation retrieves an invitation by ID.
func (repo *SQLiteInviteRepo) GetGuestInvitation(id int64) (*GuestInvitation, error) {
	var invite GuestInvitation
	var claimedAt sql.NullTime

	err := repo.conn.QueryRow(`
		SELECT id, token, subdomain, domain, name, email, expires_at, created_by, claimed_at, created_at
		FROM guest_invitations
		WHERE id = ?
	`, id).Scan(&invite.ID, &invite.Token, &invite.Subdomain, &invite.Domain, &invite.Name, &invite.Email, &invite.ExpiresAt, &invite.CreatedBy, &claimedAt, &invite.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if claimedAt.Valid {
		invite.ClaimedAt = &claimedAt.Time
	}
	return &invite, nil
}
