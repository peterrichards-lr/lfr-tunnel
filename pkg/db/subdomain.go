package db

import (
	"database/sql"
	"errors"
	"time"
)

type SQLiteSubdomainRepo struct {
	conn *sql.DB
}

func NewSQLiteSubdomainRepo(conn *sql.DB) *SQLiteSubdomainRepo {
	return &SQLiteSubdomainRepo{conn: conn}
}

// CreateSubdomainReservation registers a new subdomain reservation lease in the database.
func (repo *SQLiteSubdomainRepo) CreateSubdomainReservation(r *SubdomainReservation) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}

	extReq := 0
	if r.ExtensionRequested {
		extReq = 1
	}

	res, err := repo.conn.Exec(`
		INSERT INTO subdomain_reservations (user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.UserID, r.Subdomain, r.Domain, r.ExpiresAt, extReq, r.Passcode, r.WhitelistIPs, r.AccessMode, r.ExpiryWarningSent, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

// GetSubdomainReservation retrieves a subdomain reservation by its ID.
func (repo *SQLiteSubdomainRepo) GetSubdomainReservation(id int64) (*SubdomainReservation, error) {
	var r SubdomainReservation
	var expiresAt sql.NullTime
	var extReq, warnSent int
	var passcode, whitelistIPs, accessMode sql.NullString

	err := repo.conn.QueryRow(`
		SELECT id, user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at
		FROM subdomain_reservations
		WHERE id = ?
	`, id).Scan(&r.ID, &r.UserID, &r.Subdomain, &r.Domain, &expiresAt, &extReq, &passcode, &whitelistIPs, &accessMode, &warnSent, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if expiresAt.Valid {
		r.ExpiresAt = &expiresAt.Time
	} else {
		r.ExpiresAt = nil
	}
	r.ExtensionRequested = extReq == 1
	r.Passcode = passcode.String
	r.WhitelistIPs = whitelistIPs.String
	r.AccessMode = accessMode.String
	if r.AccessMode == "" {
		r.AccessMode = "or"
	}
	r.ExpiryWarningSent = warnSent

	return &r, nil
}

// GetSubdomainReservationByName fetches a reservation by its subdomain and domain prefix combo.
func (repo *SQLiteSubdomainRepo) GetSubdomainReservationByName(subdomain, domain string) (*SubdomainReservation, error) {
	var r SubdomainReservation
	var expiresAt sql.NullTime
	var extReq, warnSent int
	var passcode, whitelistIPs, accessMode sql.NullString

	err := repo.conn.QueryRow(`
		SELECT id, user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at
		FROM subdomain_reservations
		WHERE subdomain = ? AND domain = ?
	`, subdomain, domain).Scan(&r.ID, &r.UserID, &r.Subdomain, &r.Domain, &expiresAt, &extReq, &passcode, &whitelistIPs, &accessMode, &warnSent, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if expiresAt.Valid {
		r.ExpiresAt = &expiresAt.Time
	} else {
		r.ExpiresAt = nil
	}
	r.ExtensionRequested = extReq == 1
	r.Passcode = passcode.String
	r.WhitelistIPs = whitelistIPs.String
	r.AccessMode = accessMode.String
	if r.AccessMode == "" {
		r.AccessMode = "or"
	}
	r.ExpiryWarningSent = warnSent

	return &r, nil
}

// ListSubdomainReservationsByUserID lists all reservations held by a specific user.
func (repo *SQLiteSubdomainRepo) ListSubdomainReservationsByUserID(userID string) ([]*SubdomainReservation, error) {
	rows, err := repo.conn.Query(`
		SELECT id, user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at
		FROM subdomain_reservations
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []*SubdomainReservation
	for rows.Next() {
		var r SubdomainReservation
		var expiresAt sql.NullTime
		var extReq, warnSent int
		var passcode, whitelistIPs, accessMode sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.Subdomain, &r.Domain, &expiresAt, &extReq, &passcode, &whitelistIPs, &accessMode, &warnSent, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			r.ExpiresAt = &expiresAt.Time
		}
		r.ExtensionRequested = extReq == 1
		r.Passcode = passcode.String
		r.WhitelistIPs = whitelistIPs.String
		r.AccessMode = accessMode.String
		if r.AccessMode == "" {
			r.AccessMode = "or"
		}
		r.ExpiryWarningSent = warnSent
		list = append(list, &r)
	}
	return list, nil
}

// ListAllSubdomainReservations returns all reservations in the system.
func (repo *SQLiteSubdomainRepo) ListAllSubdomainReservations() ([]*SubdomainReservation, error) {
	rows, err := repo.conn.Query(`
		SELECT id, user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at
		FROM subdomain_reservations
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []*SubdomainReservation
	for rows.Next() {
		var r SubdomainReservation
		var expiresAt sql.NullTime
		var extReq, warnSent int
		var passcode, whitelistIPs, accessMode sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.Subdomain, &r.Domain, &expiresAt, &extReq, &passcode, &whitelistIPs, &accessMode, &warnSent, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			r.ExpiresAt = &expiresAt.Time
		}
		r.ExtensionRequested = extReq == 1
		r.Passcode = passcode.String
		r.WhitelistIPs = whitelistIPs.String
		r.AccessMode = accessMode.String
		if r.AccessMode == "" {
			r.AccessMode = "or"
		}
		r.ExpiryWarningSent = warnSent
		list = append(list, &r)
	}
	return list, nil
}

// UpdateSubdomainReservation updates an existing subdomain reservation's expiration time and extension status.
func (repo *SQLiteSubdomainRepo) UpdateSubdomainReservation(r *SubdomainReservation) error {
	r.UpdatedAt = time.Now()
	extReq := 0
	if r.ExtensionRequested {
		extReq = 1
	}
	_, err := repo.conn.Exec(`
		UPDATE subdomain_reservations
		SET expires_at = ?, extension_requested = ?, passcode = ?, whitelist_ips = ?, access_mode = ?, expiry_warning_sent = ?, updated_at = ?
		WHERE id = ?
	`, r.ExpiresAt, extReq, r.Passcode, r.WhitelistIPs, r.AccessMode, r.ExpiryWarningSent, r.UpdatedAt, r.ID)
	return err
}

// DeleteSubdomainReservation removes a subdomain reservation.
func (repo *SQLiteSubdomainRepo) DeleteSubdomainReservation(id int64) error {
	_, err := repo.conn.Exec("DELETE FROM subdomain_reservations WHERE id = ?", id)
	return err
}

// GetExpiringSubdomainReservations retrieves all reservations expiring before 'before'
// that have not yet sent the warning or expiry alert corresponding to their state.
func (repo *SQLiteSubdomainRepo) GetExpiringSubdomainReservations(now time.Time, before time.Time) ([]*SubdomainReservation, error) {
	rows, err := repo.conn.Query(`
		SELECT id, user_id, subdomain, domain, expires_at, extension_requested, passcode, whitelist_ips, access_mode, expiry_warning_sent, created_at, updated_at
		FROM subdomain_reservations
		WHERE expires_at IS NOT NULL AND (
			(expires_at <= ? AND expires_at > ? AND expiry_warning_sent = 0) OR
			(expires_at <= ? AND expiry_warning_sent < 2)
		)
	`, before, now, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []*SubdomainReservation
	for rows.Next() {
		var r SubdomainReservation
		var expiresAt sql.NullTime
		var extReq, warnSent int
		var passcode, whitelistIPs, accessMode sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.Subdomain, &r.Domain, &expiresAt, &extReq, &passcode, &whitelistIPs, &accessMode, &warnSent, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			r.ExpiresAt = &expiresAt.Time
		}
		r.ExtensionRequested = extReq == 1
		r.Passcode = passcode.String
		r.WhitelistIPs = whitelistIPs.String
		r.AccessMode = accessMode.String
		if r.AccessMode == "" {
			r.AccessMode = "or"
		}
		r.ExpiryWarningSent = warnSent
		list = append(list, &r)
	}
	return list, nil
}

// DeleteExpiredSubdomainReservations removes reservations that expired before the cutoff.
func (repo *SQLiteSubdomainRepo) DeleteExpiredSubdomainReservations(cutoff time.Time) error {
	_, err := repo.conn.Exec(`
		DELETE FROM subdomain_reservations
		WHERE expires_at IS NOT NULL AND expires_at < ?
	`, cutoff)
	return err
}

// CreateSubdomainACL registers a new ACL permission in the database.
func (repo *SQLiteSubdomainRepo) CreateSubdomainACL(acl *SubdomainACL) error {
	if acl.CreatedAt.IsZero() {
		acl.CreatedAt = time.Now().UTC()
	}

	res, err := repo.conn.Exec(`
		INSERT INTO subdomain_acl (subdomain, domain, identity, name, email, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, acl.Subdomain, acl.Domain, acl.Identity, acl.Name, acl.Email, acl.ExpiresAt, acl.CreatedAt)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	acl.ID = id
	return nil
}

// GetSubdomainACL retrieves a subdomain ACL permission by its ID.
func (repo *SQLiteSubdomainRepo) GetSubdomainACL(id int64) (*SubdomainACL, error) {
	var acl SubdomainACL
	var expiresAt sql.NullTime

	err := repo.conn.QueryRow(`
		SELECT id, subdomain, domain, identity, name, email, expires_at, created_at
		FROM subdomain_acl
		WHERE id = ?
	`, id).Scan(&acl.ID, &acl.Subdomain, &acl.Domain, &acl.Identity, &acl.Name, &acl.Email, &expiresAt, &acl.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if expiresAt.Valid {
		acl.ExpiresAt = &expiresAt.Time
	} else {
		acl.ExpiresAt = nil
	}

	return &acl, nil
}

// GetSubdomainACLByName retrieves a subdomain ACL permission by its unique keys.
func (repo *SQLiteSubdomainRepo) GetSubdomainACLByName(subdomain, domain, identity string) (*SubdomainACL, error) {
	var acl SubdomainACL
	var expiresAt sql.NullTime

	err := repo.conn.QueryRow(`
		SELECT id, subdomain, domain, identity, name, email, expires_at, created_at
		FROM subdomain_acl
		WHERE subdomain = ? AND domain = ? AND identity = ?
	`, subdomain, domain, identity).Scan(&acl.ID, &acl.Subdomain, &acl.Domain, &acl.Identity, &acl.Name, &acl.Email, &expiresAt, &acl.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if expiresAt.Valid {
		acl.ExpiresAt = &expiresAt.Time
	} else {
		acl.ExpiresAt = nil
	}

	return &acl, nil
}

// ListSubdomainACL lists all permissions configured for a subdomain.
func (repo *SQLiteSubdomainRepo) ListSubdomainACL(subdomain, domain string) ([]*SubdomainACL, error) {
	rows, err := repo.conn.Query(`
		SELECT id, subdomain, domain, identity, name, email, expires_at, created_at
		FROM subdomain_acl
		WHERE subdomain = ? AND domain = ?
		ORDER BY created_at DESC
	`, subdomain, domain)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []*SubdomainACL
	for rows.Next() {
		var acl SubdomainACL
		var expiresAt sql.NullTime
		if err := rows.Scan(&acl.ID, &acl.Subdomain, &acl.Domain, &acl.Identity, &acl.Name, &acl.Email, &expiresAt, &acl.CreatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			acl.ExpiresAt = &expiresAt.Time
		}
		list = append(list, &acl)
	}
	return list, nil
}

// DeleteSubdomainACL removes a subdomain ACL record.
func (repo *SQLiteSubdomainRepo) DeleteSubdomainACL(id int64) error {
	_, err := repo.conn.Exec("DELETE FROM subdomain_acl WHERE id = ?", id)
	return err
}

// DeleteExpiredSubdomainACLs removes expired ACL records.
func (repo *SQLiteSubdomainRepo) DeleteExpiredSubdomainACLs(cutoff time.Time) error {
	_, err := repo.conn.Exec(`
		DELETE FROM subdomain_acl
		WHERE expires_at IS NOT NULL AND expires_at < ?
	`, cutoff)
	return err
}
