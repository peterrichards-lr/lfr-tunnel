package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// AuditEntry records an administrative or lifecycle action for audit purposes.
type AuditEntry struct {
	ID         int64     `json:"id"`
	ActorID    string    `json:"actor_id"`    // email of the admin who triggered the action
	Action     string    `json:"action"`      // e.g. "user.role_changed", "token.revoked"
	TargetType string    `json:"target_type"` // "user", "token", "lease"
	TargetID   string    `json:"target_id"`   // email, PAT ID, or subdomain
	Details    string    `json:"details"`     // JSON-encoded before/after state
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditFilter controls optional filtering for ListAuditEntries.
type AuditFilter struct {
	ActorID  string
	Action   string
	TargetID string
	Limit    int
	Offset   int
}

var (
	ErrNotFound = errors.New("not found")
)

type User struct {
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	FirstName         string    `json:"first_name"`
	LastName          string    `json:"last_name"`
	Role              string    `json:"role"`   // "admin", "user"
	Status            string    `json:"status"` // "unverified", "pending", "approved", "revoked"
	VerificationToken string    `json:"-"`
	ApprovalToken     string    `json:"-"`
	ClaimToken        string    `json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type PersonalAccessToken struct {
	ID          int64      `json:"id"`
	UserID      string     `json:"user_id"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	Name        string     `json:"name"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type DB struct {
	conn *sql.DB
}

// Open initializes and returns a DB instance.
func Open(dsn string) (*DB, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	} else {
		dsn += "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Some PRAGMAs can also be executed here as a fallback
	if _, err := conn.Exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;"); err != nil {
		conn.Close()
		return nil, err
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, err
	}

	return db, nil
}

// Close shuts down the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		first_name TEXT,
		last_name TEXT,
		role TEXT NOT NULL DEFAULT 'user',
		status TEXT NOT NULL DEFAULT 'pending',
		approval_token TEXT,
		claim_token TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS personal_access_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		token_prefix TEXT NOT NULL,
		name TEXT NOT NULL,
		expires_at DATETIME,
		revoked_at DATETIME,
		last_used_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS tunnel_audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		subdomain_prefix TEXT NOT NULL,
		ports TEXT NOT NULL,
		remote_ip TEXT NOT NULL,
		connected_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		disconnected_at DATETIME,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
	);

	CREATE TABLE IF NOT EXISTS admin_audit_log (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		actor_id   TEXT    NOT NULL,
		action     TEXT    NOT NULL,
		target_type TEXT   NOT NULL,
		target_id  TEXT    NOT NULL,
		details    TEXT,
		ip_address TEXT,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_audit_actor  ON admin_audit_log(actor_id);
	CREATE INDEX IF NOT EXISTS idx_audit_action ON admin_audit_log(action);
	CREATE INDEX IF NOT EXISTS idx_audit_target ON admin_audit_log(target_id);

	CREATE TABLE IF NOT EXISTS ip_blacklist (
		ip_address TEXT PRIMARY KEY,
		reason     TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS admin_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Migrations
	_, _ = db.conn.Exec("ALTER TABLE users ADD COLUMN verification_token TEXT")

	return nil
}

// GetAdminSetting retrieves a setting value by key. Returns empty string if not found.
func (db *DB) GetAdminSetting(key string) (string, error) {
	var value string
	err := db.conn.QueryRow("SELECT value FROM admin_settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// SetAdminSetting updates or inserts a setting.
func (db *DB) SetAdminSetting(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO admin_settings (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// CreateUser inserts a new user record.
func (db *DB) CreateUser(u *User) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = time.Now().UTC()
	}

	_, err := db.conn.Exec(`
		INSERT INTO users (id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.ID, u.Email, u.FirstName, u.LastName, u.Role, u.Status, u.VerificationToken, u.ApprovalToken, u.ClaimToken, u.CreatedAt, u.UpdatedAt)
	return err
}

// fetchUserByQuery is a DRY helper for executing a single user fetch query.
func (db *DB) fetchUserByQuery(query string, arg interface{}) (*User, error) {
	var u User
	var vt, at, ct sql.NullString
	err := db.conn.QueryRow(query, arg).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &vt, &at, &ct, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.VerificationToken = vt.String
	u.ApprovalToken = at.String
	u.ClaimToken = ct.String
	return &u, nil
}

// GetUser fetches a user by their ID.
func (db *DB) GetUser(id string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users WHERE id = ?`, id)
}

// GetUserByEmail fetches a user by their email address.
func (db *DB) GetUserByEmail(email string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users WHERE email = ?`, email)
}

// GetUserByVerificationToken finds a user by their verification token.
func (db *DB) GetUserByVerificationToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users WHERE verification_token = ?`, token)
}

// GetUserByApprovalToken fetches a user by their approval token.
func (db *DB) GetUserByApprovalToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users WHERE approval_token = ?`, token)
}

// GetUserByClaimToken fetches a user by their claim token.
func (db *DB) GetUserByClaimToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users WHERE claim_token = ?`, token)
}

// UpdateUser updates an existing user profile.
func (db *DB) UpdateUser(u *User) error {
	u.UpdatedAt = time.Now().UTC()
	var vtVal interface{}
	if u.VerificationToken != "" {
		vtVal = u.VerificationToken
	}
	var approvalTokenVal interface{}
	if u.ApprovalToken != "" {
		approvalTokenVal = u.ApprovalToken
	}
	var claimTokenVal interface{}
	if u.ClaimToken != "" {
		claimTokenVal = u.ClaimToken
	}

	query := `UPDATE users SET email = ?, first_name = ?, last_name = ?, role = ?, status = ?, verification_token = ?, approval_token = ?, claim_token = ?, updated_at = ?
	          WHERE id = ?`
	res, err := db.conn.Exec(query, u.Email, u.FirstName, u.LastName, u.Role, u.Status, vtVal, approvalTokenVal, claimTokenVal, u.UpdatedAt, u.ID)
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

// ListUsers lists all registered users.
func (db *DB) ListUsers() ([]*User, error) {
	query := `SELECT id, email, first_name, last_name, role, status, verification_token, approval_token, claim_token, created_at, updated_at FROM users`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var users []*User
	for rows.Next() {
		var u User
		var vt, at, ct sql.NullString
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &vt, &at, &ct, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.VerificationToken = vt.String
		u.ApprovalToken = at.String
		u.ClaimToken = ct.String
		users = append(users, &u)
	}
	return users, nil
}

// CreatePAT generates a personal access token entry in the database.
func (db *DB) CreatePAT(pat *PersonalAccessToken) error {
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
	res, err := db.conn.Exec(query, pat.UserID, pat.TokenHash, pat.TokenPrefix, pat.Name, expiresVal, revokedVal, lastUsedVal, pat.CreatedAt)
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
func (db *DB) GetPATByHash(hash string) (*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE token_hash = ?`
	row := db.conn.QueryRow(query, hash)

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
func (db *DB) ListPATs(userID string) ([]*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE user_id = ?`
	rows, err := db.conn.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

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
func (db *DB) RevokePAT(patID int64) error {
	now := time.Now().UTC()
	query := `UPDATE personal_access_tokens SET revoked_at = ? WHERE id = ?`
	res, err := db.conn.Exec(query, now, patID)
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
func (db *DB) UpdatePATUsed(patID int64) error {
	now := time.Now().UTC()
	query := `UPDATE personal_access_tokens SET last_used_at = ? WHERE id = ?`
	res, err := db.conn.Exec(query, now, patID)
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
func (db *DB) ListAllPATs() ([]*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens ORDER BY created_at DESC`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

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

// CountAdmins returns the number of users with role="admin" and status="approved".
func (db *DB) CountAdmins() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'approved'`).Scan(&count)
	return count, err
}

// WriteAuditEntry appends a new entry to the admin_audit_log table.
func (db *DB) WriteAuditEntry(e *AuditEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	query := `INSERT INTO admin_audit_log (actor_id, action, target_type, target_id, details, ip_address, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	res, err := db.conn.Exec(query, e.ActorID, e.Action, e.TargetType, e.TargetID, e.Details, e.IPAddress, e.CreatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	e.ID = id
	return nil
}

// ListAuditEntries returns audit log entries with optional filtering and pagination.
func (db *DB) ListAuditEntries(f AuditFilter) ([]*AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}

	query := `SELECT id, actor_id, action, target_type, target_id, details, ip_address, created_at
	          FROM admin_audit_log WHERE 1=1`
	args := []interface{}{}

	if f.ActorID != "" {
		query += " AND actor_id = ?"
		args = append(args, f.ActorID)
	}
	if f.Action != "" {
		query += " AND action = ?"
		args = append(args, f.Action)
	}
	if f.TargetID != "" {
		query += " AND target_id = ?"
		args = append(args, f.TargetID)
	}

	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var details, ip sql.NullString
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.TargetType, &e.TargetID, &details, &ip, &e.CreatedAt); err != nil {
			return nil, err
		}
		if details.Valid {
			e.Details = details.String
		}
		if ip.Valid {
			e.IPAddress = ip.String
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// AddBlacklistIP adds an IP to the database blacklist.
func (db *DB) AddBlacklistIP(ip, reason string) error {
	query := "INSERT OR IGNORE INTO ip_blacklist (ip_address, reason) VALUES (?, ?)"
	_, err := db.conn.Exec(query, ip, reason)
	return err
}

// RemoveBlacklistIP removes an IP from the database blacklist.
func (db *DB) RemoveBlacklistIP(ip string) error {
	query := "DELETE FROM ip_blacklist WHERE ip_address = ?"
	_, err := db.conn.Exec(query, ip)
	return err
}

// IsBlacklisted checks if an IP is currently blacklisted.
func (db *DB) IsBlacklisted(ip string) (bool, error) {
	query := "SELECT 1 FROM ip_blacklist WHERE ip_address = ?"
	var dummy int
	err := db.conn.QueryRow(query, ip).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// BlacklistEntry represents a blacklisted IP.
type BlacklistEntry struct {
	IPAddress string    `json:"ip_address"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// ListBlacklistedIPs returns all blacklisted IPs.
func (db *DB) ListBlacklistedIPs() ([]*BlacklistEntry, error) {
	query := "SELECT ip_address, reason, created_at FROM ip_blacklist ORDER BY created_at DESC"
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
