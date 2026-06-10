package db

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
)

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	FirstName     string    `json:"first_name"`
	LastName      string    `json:"last_name"`
	Role          string    `json:"role"`   // "admin", "user"
	Status        string    `json:"status"` // "pending", "approved", "revoked"
	ApprovalToken string    `json:"-"`
	ClaimToken    string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Enable foreign key constraints and set busy timeout in SQLite
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
	);`

	_, err := db.conn.Exec(schema)
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

	query := `INSERT INTO users (id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.conn.Exec(query, u.ID, u.Email, u.FirstName, u.LastName, u.Role, u.Status, u.ApprovalToken, u.ClaimToken, u.CreatedAt, u.UpdatedAt)
	return err
}

// GetUser fetches a user by their ID.
func (db *DB) GetUser(id string) (*User, error) {
	query := `SELECT id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at
	          FROM users WHERE id = ?`
	row := db.conn.QueryRow(query, id)

	var u User
	var approvalToken, claimToken sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &approvalToken, &claimToken, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if approvalToken.Valid {
		u.ApprovalToken = approvalToken.String
	}
	if claimToken.Valid {
		u.ClaimToken = claimToken.String
	}
	return &u, nil
}

// GetUserByEmail fetches a user by their email address.
func (db *DB) GetUserByEmail(email string) (*User, error) {
	query := `SELECT id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at
	          FROM users WHERE email = ?`
	row := db.conn.QueryRow(query, email)

	var u User
	var approvalToken, claimToken sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &approvalToken, &claimToken, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if approvalToken.Valid {
		u.ApprovalToken = approvalToken.String
	}
	if claimToken.Valid {
		u.ClaimToken = claimToken.String
	}
	return &u, nil
}

// GetUserByApprovalToken fetches a user by their approval token.
func (db *DB) GetUserByApprovalToken(token string) (*User, error) {
	query := `SELECT id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at
	          FROM users WHERE approval_token = ?`
	row := db.conn.QueryRow(query, token)

	var u User
	var approvalToken, claimToken sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &approvalToken, &claimToken, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if approvalToken.Valid {
		u.ApprovalToken = approvalToken.String
	}
	if claimToken.Valid {
		u.ClaimToken = claimToken.String
	}
	return &u, nil
}

// GetUserByClaimToken fetches a user by their claim token.
func (db *DB) GetUserByClaimToken(token string) (*User, error) {
	query := `SELECT id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at
	          FROM users WHERE claim_token = ?`
	row := db.conn.QueryRow(query, token)

	var u User
	var approvalToken, claimToken sql.NullString
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &approvalToken, &claimToken, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if approvalToken.Valid {
		u.ApprovalToken = approvalToken.String
	}
	if claimToken.Valid {
		u.ClaimToken = claimToken.String
	}
	return &u, nil
}

// UpdateUser updates an existing user profile.
func (db *DB) UpdateUser(u *User) error {
	u.UpdatedAt = time.Now().UTC()
	var approvalTokenVal interface{}
	if u.ApprovalToken != "" {
		approvalTokenVal = u.ApprovalToken
	}
	var claimTokenVal interface{}
	if u.ClaimToken != "" {
		claimTokenVal = u.ClaimToken
	}

	query := `UPDATE users SET email = ?, first_name = ?, last_name = ?, role = ?, status = ?, approval_token = ?, claim_token = ?, updated_at = ?
	          WHERE id = ?`
	res, err := db.conn.Exec(query, u.Email, u.FirstName, u.LastName, u.Role, u.Status, approvalTokenVal, claimTokenVal, u.UpdatedAt, u.ID)
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
	query := `SELECT id, email, first_name, last_name, role, status, approval_token, claim_token, created_at, updated_at FROM users`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		var u User
		var approvalToken, claimToken sql.NullString
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Status, &approvalToken, &claimToken, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		if approvalToken.Valid {
			u.ApprovalToken = approvalToken.String
		}
		if claimToken.Valid {
			u.ClaimToken = claimToken.String
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

// ListPATs returns all PATs belonging to a specific user.
func (db *DB) ListPATs(userID string) ([]*PersonalAccessToken, error) {
	query := `SELECT id, user_id, token_hash, token_prefix, name, expires_at, revoked_at, last_used_at, created_at
	          FROM personal_access_tokens WHERE user_id = ?`
	rows, err := db.conn.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
