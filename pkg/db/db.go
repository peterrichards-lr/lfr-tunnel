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

// TunnelMetric records bandwidth usage for a tunnel session.
type TunnelMetric struct {
	ID              int64     `json:"id"`
	UserID          string    `json:"user_id"`
	SubdomainPrefix string    `json:"subdomain_prefix"`
	FullHost        string    `json:"full_host"`
	BytesIn         int64     `json:"bytes_in"`
	BytesOut        int64     `json:"bytes_out"`
	ConnectedAt     time.Time `json:"connected_at"`
	RecordedAt      time.Time `json:"recorded_at"`
}

// AuditFilter controls optional filtering for ListAuditEntries.

// MagicLink represents a sent magic link in the database.
type MagicLink struct {
	ID        int        `json:"id"`
	Email     string     `json:"email"`
	TokenHash string     `json:"-"` // Not exposed in JSON
	ClientIP  string     `json:"client_ip"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
}

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
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	FirstName         string     `json:"first_name"`
	LastName          string     `json:"last_name"`
	PreferredName     string     `json:"preferred_name"`
	Role              string     `json:"role"`   // "admin", "user"
	Status            string     `json:"status"` // "unverified", "pending", "approved", "revoked"
	VerificationToken string     `json:"-"`
	ApprovalToken     string     `json:"-"`
	ClaimToken        string     `json:"-"`
	Timezone          string     `json:"timezone"`
	AuthMethod        string     `json:"auth_method"`
	ThemePreference   string     `json:"theme_preference"`
	NotificationPrefs string     `json:"notification_prefs"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	LastLoginIP       string     `json:"last_login_ip"`
	LastClientVersion string     `json:"last_client_version"`
	LastClientOS      string     `json:"last_client_os"`
	TOTPSecret        string     `json:"-"`
	TOTPEnabled       bool       `json:"totp_enabled"`
	PolicyConsentAt   *time.Time `json:"policy_consent_at,omitempty"`
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
		conn.Close() //nolint:errcheck
		return nil, err
	}

	// Migrations
	conn.Exec("ALTER TABLE users ADD COLUMN timezone TEXT DEFAULT 'UTC'")            //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN auth_method TEXT DEFAULT 'Magic Link'")  //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN preferred_name TEXT DEFAULT ''")         //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN theme_preference TEXT DEFAULT 'system'") //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN notification_prefs TEXT DEFAULT '{}'")   //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN last_login_at DATETIME")                 //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN last_login_ip TEXT DEFAULT ''")          //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN totp_secret TEXT DEFAULT ''")            //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN totp_enabled INTEGER DEFAULT 0")         //nolint:errcheck
	conn.Exec("ALTER TABLE users ADD COLUMN policy_consent_at DATETIME")             //nolint:errcheck

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close() //nolint:errcheck
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
		preferred_name TEXT DEFAULT '',
		role TEXT NOT NULL DEFAULT 'user',
		status TEXT NOT NULL DEFAULT 'pending',
		approval_token TEXT,
		claim_token TEXT,
		timezone TEXT DEFAULT 'UTC',
		auth_method TEXT DEFAULT 'Magic Link',
		theme_preference TEXT DEFAULT 'system',
		notification_prefs TEXT DEFAULT '{}',
		last_login_at DATETIME,
		last_login_ip TEXT DEFAULT '',
		totp_secret TEXT DEFAULT '',
		totp_enabled INTEGER DEFAULT 0,
		policy_consent_at DATETIME,
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

	
	CREATE TABLE IF NOT EXISTS admin_magic_links (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		client_ip TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		expires_at DATETIME NOT NULL,
		used_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_magic_email ON admin_magic_links(email);
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
		reason TEXT,
		banned_by TEXT,
		banned_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS tunnel_metrics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		subdomain_prefix TEXT NOT NULL,
		full_host TEXT NOT NULL,
		bytes_in INTEGER NOT NULL,
		bytes_out INTEGER NOT NULL,
		connected_at DATETIME NOT NULL,
		recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
	_, _ = db.conn.Exec("ALTER TABLE users ADD COLUMN last_client_version TEXT")
	_, _ = db.conn.Exec("ALTER TABLE users ADD COLUMN last_client_os TEXT")

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

// RecordTunnelMetric writes a single bandwidth metric to the database.
func (db *DB) RecordTunnelMetric(m *TunnelMetric) error {
	_, err := db.conn.Exec(`
		INSERT INTO tunnel_metrics (user_id, subdomain_prefix, full_host, bytes_in, bytes_out, connected_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, m.UserID, m.SubdomainPrefix, m.FullHost, m.BytesIn, m.BytesOut, m.ConnectedAt, m.RecordedAt)
	return err
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
	if u.Timezone == "" {
		u.Timezone = "UTC"
	}
	if u.AuthMethod == "" {
		u.AuthMethod = "Magic Link"
	}
	if u.ThemePreference == "" {
		u.ThemePreference = "system"
	}
	if u.NotificationPrefs == "" {
		u.NotificationPrefs = "{}"
	}

	totpEnabledVal := 0
	if u.TOTPEnabled {
		totpEnabledVal = 1
	}

	_, err := db.conn.Exec(`
		INSERT INTO users (id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.ID, u.Email, u.FirstName, u.LastName, u.PreferredName, u.Role, u.Status, u.VerificationToken, u.ApprovalToken, u.ClaimToken, u.Timezone, u.AuthMethod, u.ThemePreference, u.NotificationPrefs, u.CreatedAt, u.UpdatedAt, u.LastClientVersion, u.LastClientOS, u.TOTPSecret, totpEnabledVal, u.PolicyConsentAt)
	return err
}

// fetchUserByQuery is a DRY helper for executing a single user fetch query.
func (db *DB) fetchUserByQuery(query string, arg interface{}) (*User, error) {
	var u User
	var vt, at, ct sql.NullString
	var lastLogin sql.NullTime
	var lastClientVersion sql.NullString
	var lastClientOS sql.NullString
	var totpSecret sql.NullString
	var totpEnabled int
	var policyConsentAt sql.NullTime
	err := db.conn.QueryRow(query, arg).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.PreferredName, &u.Role, &u.Status, &vt, &at, &ct, &u.Timezone, &u.AuthMethod, &u.ThemePreference, &u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt, &lastLogin, &u.LastLoginIP, &lastClientVersion, &lastClientOS, &totpSecret, &totpEnabled, &policyConsentAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.VerificationToken = vt.String
	u.LastClientVersion = lastClientVersion.String
	u.LastClientOS = lastClientOS.String
	u.ApprovalToken = at.String
	u.ClaimToken = ct.String
	u.TOTPSecret = totpSecret.String
	u.TOTPEnabled = totpEnabled == 1
	if policyConsentAt.Valid {
		u.PolicyConsentAt = &policyConsentAt.Time
	}
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	return &u, nil
}

// GetUser fetches a user by their ID.
func (db *DB) GetUser(id string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users WHERE id = ?`, id)
}

// GetUserByEmail fetches a user by their email address.
func (db *DB) GetUserByEmail(email string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users WHERE email = ?`, email)
}

// GetUserByVerificationToken finds a user by their verification token.
func (db *DB) GetUserByVerificationToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users WHERE verification_token = ?`, token)
}

// GetUserByApprovalToken fetches a user by their approval token.
func (db *DB) GetUserByApprovalToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users WHERE approval_token = ?`, token)
}

// GetUserByClaimToken fetches a user by their claim token.
func (db *DB) GetUserByClaimToken(token string) (*User, error) {
	return db.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users WHERE claim_token = ?`, token)
}

// DeleteUser removes a user from the database.
func (db *DB) DeleteUser(id string) error {
	query := "DELETE FROM users WHERE id = ?"
	_, err := db.conn.Exec(query, id)
	return err
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

	var lastLoginVal interface{}
	if u.LastLoginAt != nil {
		lastLoginVal = *u.LastLoginAt
	}

	totpEnabledVal := 0
	if u.TOTPEnabled {
		totpEnabledVal = 1
	}

	query := `UPDATE users SET email = ?, first_name = ?, last_name = ?, preferred_name = ?, role = ?, status = ?, verification_token = ?, approval_token = ?, claim_token = ?, timezone = ?, auth_method = ?, theme_preference = ?, notification_prefs = ?, updated_at = ?, last_login_at = ?, last_login_ip = ?,
			last_client_version = ?,
			last_client_os = ?,
			totp_secret = ?,
			totp_enabled = ?,
			policy_consent_at = ?
	          WHERE id = ?`
	res, err := db.conn.Exec(query, u.Email, u.FirstName, u.LastName, u.PreferredName, u.Role, u.Status, vtVal, approvalTokenVal, claimTokenVal, u.Timezone, u.AuthMethod, u.ThemePreference, u.NotificationPrefs, u.UpdatedAt, lastLoginVal, u.LastLoginIP, u.LastClientVersion, u.LastClientOS, u.TOTPSecret, totpEnabledVal, u.PolicyConsentAt, u.ID)
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
	query := `SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at FROM users`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck

	var users []*User
	for rows.Next() {
		var u User
		var vt, at, ct sql.NullString
		var lastLogin sql.NullTime
		var lastClientVersion sql.NullString
		var lastClientOS sql.NullString
		var totpSecret sql.NullString
		var totpEnabled int
		var policyConsentAt sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.PreferredName, &u.Role, &u.Status, &vt, &at, &ct, &u.Timezone, &u.AuthMethod, &u.ThemePreference, &u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt, &lastLogin, &u.LastLoginIP, &lastClientVersion, &lastClientOS, &totpSecret, &totpEnabled, &policyConsentAt); err != nil {
			return nil, err
		}
		u.VerificationToken = vt.String
		u.LastClientVersion = lastClientVersion.String
		u.LastClientOS = lastClientOS.String
		u.ApprovalToken = at.String
		u.ClaimToken = ct.String
		u.TOTPSecret = totpSecret.String
		u.TOTPEnabled = totpEnabled == 1
		if policyConsentAt.Valid {
			u.PolicyConsentAt = &policyConsentAt.Time
		}
		if lastLogin.Valid {
			u.LastLoginAt = &lastLogin.Time
		}
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

// ListPATs returns all active (unrevoked) PATs belonging to a specific user.
func (db *DB) ListPATs(userID string) ([]*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE user_id = ? AND revoked_at IS NULL`
	rows, err := db.conn.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck

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
	defer func() { _ = rows.Close() }() //nolint:errcheck

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
	defer func() { _ = rows.Close() }() //nolint:errcheck

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
	defer func() { _ = rows.Close() }() //nolint:errcheck

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

// CreateMagicLink saves a new magic link to the database.
func (db *DB) CreateMagicLink(email, tokenHash, clientIP string, expiresAt time.Time) error {
	query := `INSERT INTO admin_magic_links (email, token_hash, client_ip, expires_at) VALUES (?, ?, ?, ?)`
	_, err := db.conn.Exec(query, email, tokenHash, clientIP, expiresAt)
	return err
}

// GetMagicLink retrieves a magic link by its token hash.
func (db *DB) GetMagicLink(tokenHash string) (*MagicLink, error) {
	query := `SELECT id, email, token_hash, client_ip, created_at, expires_at, used_at FROM admin_magic_links WHERE token_hash = ?`
	row := db.conn.QueryRow(query, tokenHash)

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
func (db *DB) PruneExpiredMagicLinks() error {
	_, err := db.conn.Exec("DELETE FROM admin_magic_links WHERE expires_at < CURRENT_TIMESTAMP")
	return err
}

// MarkMagicLinkUsed marks a magic link as used.
func (db *DB) MarkMagicLinkUsed(id int) error {
	query := `UPDATE admin_magic_links SET used_at = datetime('now') WHERE id = ?`
	_, err := db.conn.Exec(query, id)
	return err
}

// InvalidateOtherMagicLinks expires all other unused magic links for a given email.
func (db *DB) InvalidateOtherMagicLinks(email string, excludeID int) error {
	query := `UPDATE admin_magic_links SET expires_at = CURRENT_TIMESTAMP WHERE email = ? AND id != ? AND used_at IS NULL`
	_, err := db.conn.Exec(query, email, excludeID)
	return err
}

// ListMagicLinks returns all magic links, ordered by newest first.
func (db *DB) ListMagicLinks() ([]*MagicLink, error) {
	query := `SELECT id, email, client_ip, created_at, expires_at, used_at FROM admin_magic_links ORDER BY created_at DESC LIMIT 100`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck

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

// Analytics Types
type DailyBandwidth struct {
	Date     string `json:"date"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type UserBandwidth struct {
	Email    string `json:"email"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type TunnelBandwidth struct {
	FullHost string `json:"full_host"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type GlobalAnalytics struct {
	Daily    []DailyBandwidth `json:"daily"`
	TopUsers []UserBandwidth  `json:"top_users"`
}

type UserAnalytics struct {
	Daily   []DailyBandwidth  `json:"daily"`
	Tunnels []TunnelBandwidth `json:"tunnels"`
}

// GetGlobalAnalytics retrieves system-wide bandwidth stats for the last N days.
func (db *DB) GetGlobalAnalytics(days int) (*GlobalAnalytics, error) {
	timeLimit := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	dailyQuery := `
		SELECT strftime('%Y-%m-%d', recorded_at) as d, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE recorded_at >= ?
		GROUP BY d
		ORDER BY d ASC
	`
	rows, err := db.conn.Query(dailyQuery, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	daily := make([]DailyBandwidth, 0)
	for rows.Next() {
		var dbw DailyBandwidth
		if err := rows.Scan(&dbw.Date, &dbw.BytesIn, &dbw.BytesOut); err != nil {
			return nil, err
		}
		daily = append(daily, dbw)
	}

	topQuery := `
		SELECT COALESCE(u.email, m.user_id), SUM(m.bytes_in), SUM(m.bytes_out)
		FROM tunnel_metrics m
		LEFT JOIN users u ON m.user_id = u.id
		WHERE m.recorded_at >= ?
		GROUP BY m.user_id
		ORDER BY (SUM(m.bytes_in) + SUM(m.bytes_out)) DESC
		LIMIT 10
	`
	topRows, err := db.conn.Query(topQuery, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = topRows.Close() }()

	top := make([]UserBandwidth, 0)
	for topRows.Next() {
		var ub UserBandwidth
		if err := topRows.Scan(&ub.Email, &ub.BytesIn, &ub.BytesOut); err != nil {
			return nil, err
		}
		top = append(top, ub)
	}

	return &GlobalAnalytics{Daily: daily, TopUsers: top}, nil
}

// GetUserAnalytics retrieves bandwidth stats for a specific user for the last N days.
func (db *DB) GetUserAnalytics(userID string, days int) (*UserAnalytics, error) {
	timeLimit := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	dailyQuery := `
		SELECT strftime('%Y-%m-%d', recorded_at) as d, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE user_id = ? AND recorded_at >= ?
		GROUP BY d
		ORDER BY d ASC
	`
	rows, err := db.conn.Query(dailyQuery, userID, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	daily := make([]DailyBandwidth, 0)
	for rows.Next() {
		var dbw DailyBandwidth
		if err := rows.Scan(&dbw.Date, &dbw.BytesIn, &dbw.BytesOut); err != nil {
			return nil, err
		}
		daily = append(daily, dbw)
	}

	tunnelQuery := `
		SELECT full_host, SUM(bytes_in), SUM(bytes_out)
		FROM tunnel_metrics
		WHERE user_id = ? AND recorded_at >= ?
		GROUP BY full_host
		ORDER BY (SUM(bytes_in) + SUM(bytes_out)) DESC
	`
	tunnelRows, err := db.conn.Query(tunnelQuery, userID, timeLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tunnelRows.Close() }()

	tunnels := make([]TunnelBandwidth, 0)
	for tunnelRows.Next() {
		var tb TunnelBandwidth
		if err := tunnelRows.Scan(&tb.FullHost, &tb.BytesIn, &tb.BytesOut); err != nil {
			return nil, err
		}
		tunnels = append(tunnels, tb)
	}

	return &UserAnalytics{Daily: daily, Tunnels: tunnels}, nil
}

type ClientVersionStats struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Count   int    `json:"count"`
}

// GetClientVersionStats groups users by client version and OS.
func (db *DB) GetClientVersionStats() ([]ClientVersionStats, error) {
	rows, err := db.conn.Query(`
		SELECT 
			COALESCE(NULLIF(last_client_version, ''), 'Unknown'),
			COALESCE(NULLIF(last_client_os, ''), 'Unknown'),
			COUNT(*)
		FROM users
		WHERE last_client_version IS NOT NULL AND last_client_version != ''
		GROUP BY last_client_version, last_client_os
		ORDER BY last_client_version DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stats []ClientVersionStats
	for rows.Next() {
		var stat ClientVersionStats
		if err := rows.Scan(&stat.Version, &stat.OS, &stat.Count); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, nil
}
