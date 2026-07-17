package db

import (
	"database/sql"
	"errors"
	"time"
)

type SQLiteUserRepo struct {
	conn *sql.DB
}

func NewSQLiteUserRepo(conn *sql.DB) *SQLiteUserRepo {
	return &SQLiteUserRepo{conn: conn}
}

// CreateUser inserts a new user record.
func (repo *SQLiteUserRepo) CreateUser(u *User) error {
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

	if u.LanguagePreference == "" {
		u.LanguagePreference = "en"
	}

	if u.OnboardingStatus == "" {
		u.OnboardingStatus = "pending"
	}

	_, err := repo.conn.Exec(`
		INSERT INTO users (id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.ID, u.Email, u.FirstName, u.LastName, u.PreferredName, u.Role, u.Status, u.VerificationToken, u.ApprovalToken, u.ClaimToken, u.Timezone, u.AuthMethod, u.ThemePreference, u.NotificationPrefs, u.CreatedAt, u.UpdatedAt, u.LastClientVersion, u.LastClientOS, u.TOTPSecret, totpEnabledVal, u.PolicyConsentAt, u.LanguagePreference, u.RateLimit, u.MaxReservations, u.MaxTunnels, u.OnboardingStatus, u.OnboardingLastStep, u.OnboardingReruns)
	return err
}

// fetchUserByQuery is a DRY helper for executing a single user fetch query.
func (repo *SQLiteUserRepo) fetchUserByQuery(query string, arg interface{}) (*User, error) {
	var u User
	var vt, at, ct sql.NullString
	var lastLogin sql.NullTime
	var lastClientVersion sql.NullString
	var lastClientOS sql.NullString
	var totpSecret sql.NullString
	var totpEnabled int
	var policyConsentAt sql.NullTime
	var langPref sql.NullString
	var rateLimitVal int
	var maxReservationsVal sql.NullInt64
	var maxActiveTunnelsVal sql.NullInt64
	err := repo.conn.QueryRow(query, arg).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.PreferredName, &u.Role, &u.Status, &vt, &at, &ct, &u.Timezone, &u.AuthMethod, &u.ThemePreference, &u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt, &lastLogin, &u.LastLoginIP, &lastClientVersion, &lastClientOS, &totpSecret, &totpEnabled, &policyConsentAt, &langPref, &rateLimitVal, &maxReservationsVal, &maxActiveTunnelsVal, &u.OnboardingStatus, &u.OnboardingLastStep, &u.OnboardingReruns,
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
	u.RateLimit = rateLimitVal
	if maxReservationsVal.Valid {
		val := int(maxReservationsVal.Int64)
		u.MaxReservations = &val
	} else {
		u.MaxReservations = nil
	}
	if maxActiveTunnelsVal.Valid {
		val := int(maxActiveTunnelsVal.Int64)
		u.MaxTunnels = &val
	} else {
		u.MaxTunnels = nil
	}
	if policyConsentAt.Valid {
		u.PolicyConsentAt = &policyConsentAt.Time
	}
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	if langPref.Valid && langPref.String != "" {
		u.LanguagePreference = langPref.String
	} else {
		u.LanguagePreference = "en"
	}
	return &u, nil
}

// GetUser fetches a user by their ID.
func (repo *SQLiteUserRepo) GetUser(id string) (*User, error) {
	return repo.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users WHERE id = ?`, id)
}

// GetUserByEmail fetches a user by their email address.
func (repo *SQLiteUserRepo) GetUserByEmail(email string) (*User, error) {
	return repo.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users WHERE email = ?`, email)
}

// GetUserByVerificationToken finds a user by their verification token.
func (repo *SQLiteUserRepo) GetUserByVerificationToken(token string) (*User, error) {
	return repo.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users WHERE verification_token = ?`, token)
}

// GetUserByApprovalToken fetches a user by their approval token.
func (repo *SQLiteUserRepo) GetUserByApprovalToken(token string) (*User, error) {
	return repo.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users WHERE approval_token = ?`, token)
}

// GetUserByClaimToken fetches a user by their claim token.
func (repo *SQLiteUserRepo) GetUserByClaimToken(token string) (*User, error) {
	return repo.fetchUserByQuery(`SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users WHERE claim_token = ?`, token)
}

// DeleteUser removes a user from the database.
func (repo *SQLiteUserRepo) DeleteUser(id string) error {
	query := "DELETE FROM users WHERE id = ?"
	_, err := repo.conn.Exec(query, id)
	return err
}

// UpdateUser updates an existing user profile.
func (repo *SQLiteUserRepo) UpdateUser(u *User) error {
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
			policy_consent_at = ?,
			language_preference = ?,
			rate_limit = ?,
			max_reservations = ?,
			max_active_tunnels = ?,
			onboarding_status = ?,
			onboarding_last_step = ?,
			onboarding_reruns = ?
	          WHERE id = ?`
	res, err := repo.conn.Exec(query, u.Email, u.FirstName, u.LastName, u.PreferredName, u.Role, u.Status, vtVal, approvalTokenVal, claimTokenVal, u.Timezone, u.AuthMethod, u.ThemePreference, u.NotificationPrefs, u.UpdatedAt, lastLoginVal, u.LastLoginIP, u.LastClientVersion, u.LastClientOS, u.TOTPSecret, totpEnabledVal, u.PolicyConsentAt, u.LanguagePreference, u.RateLimit, u.MaxReservations, u.MaxTunnels, u.OnboardingStatus, u.OnboardingLastStep, u.OnboardingReruns, u.ID)
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

// UpdateUserOnboarding updates a user's onboarding progress.
func (repo *SQLiteUserRepo) UpdateUserOnboarding(userID string, status string, lastStep string, incReruns bool) error {
	var query string
	if incReruns {
		query = `UPDATE users SET onboarding_status = ?, onboarding_last_step = ?, onboarding_reruns = onboarding_reruns + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	} else {
		query = `UPDATE users SET onboarding_status = ?, onboarding_last_step = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	}
	res, err := repo.conn.Exec(query, status, lastStep, userID)
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
func (repo *SQLiteUserRepo) ListUsers() ([]*User, error) {
	query := `SELECT id, email, first_name, last_name, preferred_name, role, status, verification_token, approval_token, claim_token, timezone, auth_method, theme_preference, notification_prefs, created_at, updated_at, last_login_at, last_login_ip, last_client_version, last_client_os, totp_secret, totp_enabled, policy_consent_at, language_preference, rate_limit, max_reservations, max_active_tunnels, onboarding_status, onboarding_last_step, onboarding_reruns FROM users`
	rows, err := repo.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
		var langPref sql.NullString
		var rateLimitVal int
		var maxReservationsVal sql.NullInt64
		var maxActiveTunnelsVal sql.NullInt64
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.PreferredName, &u.Role, &u.Status, &vt, &at, &ct, &u.Timezone, &u.AuthMethod, &u.ThemePreference, &u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt, &lastLogin, &u.LastLoginIP, &lastClientVersion, &lastClientOS, &totpSecret, &totpEnabled, &policyConsentAt, &langPref, &rateLimitVal, &maxReservationsVal, &maxActiveTunnelsVal, &u.OnboardingStatus, &u.OnboardingLastStep, &u.OnboardingReruns); err != nil {
			return nil, err
		}
		u.VerificationToken = vt.String
		u.LastClientVersion = lastClientVersion.String
		u.LastClientOS = lastClientOS.String
		u.ApprovalToken = at.String
		u.ClaimToken = ct.String
		u.TOTPSecret = totpSecret.String
		u.TOTPEnabled = totpEnabled == 1
		u.RateLimit = rateLimitVal
		if maxReservationsVal.Valid {
			val := int(maxReservationsVal.Int64)
			u.MaxReservations = &val
		} else {
			u.MaxReservations = nil
		}
		if maxActiveTunnelsVal.Valid {
			val := int(maxActiveTunnelsVal.Int64)
			u.MaxTunnels = &val
		} else {
			u.MaxTunnels = nil
		}
		if policyConsentAt.Valid {
			u.PolicyConsentAt = &policyConsentAt.Time
		}
		if lastLogin.Valid {
			u.LastLoginAt = &lastLogin.Time
		}
		if langPref.Valid && langPref.String != "" {
			u.LanguagePreference = langPref.String
		} else {
			u.LanguagePreference = "en"
		}
		users = append(users, &u)
	}
	return users, nil
}

// CountAdmins returns the number of users with role="admin" and status="approved".
func (repo *SQLiteUserRepo) CountAdmins() (int, error) {
	var count int
	err := repo.conn.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'approved'`).Scan(&count)
	return count, err
}

// AnonymizeUserData obfuscates all audit logs and metrics associated with a deleted user.
func (repo *SQLiteUserRepo) AnonymizeUserData(userID, anonymizedID string) error {
	tx, err := repo.conn.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() //nolint:errcheck
	}()

	if _, err := tx.Exec("UPDATE tunnel_metrics SET user_id = ? WHERE user_id = ?", anonymizedID, userID); err != nil {
		return err
	}

	if _, err := tx.Exec("UPDATE tunnel_audit_logs SET user_id = ? WHERE user_id = ?", anonymizedID, userID); err != nil {
		return err
	}

	if _, err := tx.Exec("UPDATE admin_audit_log SET actor_id = ? WHERE actor_id = ?", anonymizedID, userID); err != nil {
		return err
	}

	if _, err := tx.Exec("UPDATE admin_audit_log SET target_id = ? WHERE target_id = ?", anonymizedID, userID); err != nil {
		return err
	}

	return tx.Commit()
}
