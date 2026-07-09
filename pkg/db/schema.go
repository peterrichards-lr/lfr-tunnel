package db

import (
	"strings"
)

// initSchema applies the core tables and runs inline migrations
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
		language_preference TEXT NOT NULL DEFAULT 'en',
		rate_limit INTEGER DEFAULT 0,
		max_reservations INTEGER DEFAULT NULL,
		max_active_tunnels INTEGER DEFAULT NULL,
		onboarding_status TEXT NOT NULL DEFAULT 'pending',
		onboarding_last_step TEXT DEFAULT '',
		onboarding_reruns INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS subdomain_reservations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		subdomain TEXT NOT NULL,
		domain TEXT NOT NULL,
		expires_at DATETIME,
		extension_requested INTEGER DEFAULT 0,
		passcode TEXT DEFAULT '',
		whitelist_ips TEXT DEFAULT '',
		access_mode TEXT DEFAULT 'or',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_subdomain_domain ON subdomain_reservations(subdomain, domain);

	CREATE TABLE IF NOT EXISTS subdomain_acl (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subdomain TEXT NOT NULL,
		domain TEXT NOT NULL,
		identity TEXT NOT NULL,
		name TEXT DEFAULT '',
		email TEXT DEFAULT '',
		expires_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_subdomain_domain_identity ON subdomain_acl(subdomain, domain, identity);


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
		recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		node_id TEXT DEFAULT 'control'
	);

	CREATE TABLE IF NOT EXISTS admin_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS gateway_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		start_time DATETIME NOT NULL,
		end_time DATETIME
	);

	CREATE TABLE IF NOT EXISTS guest_invitations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT UNIQUE NOT NULL,
		subdomain TEXT NOT NULL,
		domain TEXT NOT NULL,
		name TEXT NOT NULL,
		email TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		created_by TEXT NOT NULL,
		claimed_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Create schema_version table if not exists
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return err
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var currentVersion int
	err = tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		_, err := tx.Exec(m.query)
		if err != nil {
			// If column already exists (e.g. legacy DB that had columns added ad-hoc),
			// we skip error but still record the version to prevent future attempts.
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return err
			}
		}

		_, err = tx.Exec("INSERT INTO schema_version (version) VALUES (?)", m.version)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

type migration struct {
	version int
	query   string
}

var migrations = []migration{
	{1, "ALTER TABLE users ADD COLUMN verification_token TEXT"},
	{2, "ALTER TABLE users ADD COLUMN last_client_version TEXT"},
	{3, "ALTER TABLE users ADD COLUMN last_client_os TEXT"},
	{4, "ALTER TABLE users ADD COLUMN rate_limit INTEGER DEFAULT 0"},
	{5, "ALTER TABLE users ADD COLUMN max_reservations INTEGER DEFAULT NULL"},
	{6, "ALTER TABLE users ADD COLUMN max_active_tunnels INTEGER DEFAULT NULL"},
	{7, "ALTER TABLE subdomain_reservations ADD COLUMN extension_requested INTEGER DEFAULT 0"},
	{8, "ALTER TABLE subdomain_reservations ADD COLUMN passcode TEXT DEFAULT ''"},
	{9, "ALTER TABLE subdomain_reservations ADD COLUMN whitelist_ips TEXT DEFAULT ''"},
	{10, "ALTER TABLE subdomain_reservations ADD COLUMN access_mode TEXT DEFAULT 'or'"},
	{11, "ALTER TABLE subdomain_reservations ADD COLUMN expiry_warning_sent INTEGER DEFAULT 0"},
	{12, "ALTER TABLE tunnel_metrics ADD COLUMN node_id TEXT DEFAULT 'control'"},
	{13, "ALTER TABLE users ADD COLUMN onboarding_status TEXT NOT NULL DEFAULT 'pending'"},
	{14, "ALTER TABLE users ADD COLUMN onboarding_last_step TEXT DEFAULT ''"},
	{15, "ALTER TABLE users ADD COLUMN onboarding_reruns INTEGER NOT NULL DEFAULT 0"},
}
