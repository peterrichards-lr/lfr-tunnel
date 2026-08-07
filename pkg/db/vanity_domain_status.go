package db

import (
	"database/sql"
	"fmt"
)

type SQLiteVanityDomainStatusRepo struct {
	conn *sql.DB
}

func NewSQLiteVanityDomainStatusRepo(conn *sql.DB) *SQLiteVanityDomainStatusRepo {
	return &SQLiteVanityDomainStatusRepo{conn: conn}
}

// validVanityDomainStages maps the stage names callers use to the column that tracks them.
// Deliberately excludes "requested" -- that one only ever gets set via
// StartVanityDomainAttempt, which also resets everything else for a fresh attempt.
var validVanityDomainStages = map[string]string{
	"nginx_config": "nginx_config_at",
	"cert_issued":  "cert_issued_at",
	"live":         "live_at",
}

const vanityDomainStatusColumns = "full_host, user_id, requested_at, nginx_config_at, cert_issued_at, live_at, failed_stage, error_message, updated_at"

// StartVanityDomainAttempt records a fresh "add" attempt: sets requested_at to now and
// clears every later stage/failure field, so a retry doesn't show stale state left over
// from a previous failed attempt.
func (repo *SQLiteVanityDomainStatusRepo) StartVanityDomainAttempt(fullHost, userID string) error {
	query := `
		INSERT INTO vanity_domain_status (full_host, user_id, requested_at, nginx_config_at, cert_issued_at, live_at, failed_stage, error_message, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, NULL, NULL, NULL, NULL, NULL, CURRENT_TIMESTAMP)
		ON CONFLICT(full_host) DO UPDATE SET
			user_id = excluded.user_id,
			requested_at = CURRENT_TIMESTAMP,
			nginx_config_at = NULL,
			cert_issued_at = NULL,
			live_at = NULL,
			failed_stage = NULL,
			error_message = NULL,
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := repo.conn.Exec(query, fullHost, userID)
	return err
}

// MarkVanityDomainStage sets the given stage's timestamp to now. stage must be one of
// "nginx_config", "cert_issued", or "live" -- see validVanityDomainStages.
func (repo *SQLiteVanityDomainStatusRepo) MarkVanityDomainStage(fullHost, stage string) error {
	column, ok := validVanityDomainStages[stage]
	if !ok {
		return fmt.Errorf("unknown vanity domain stage %q", stage)
	}
	query := fmt.Sprintf("UPDATE vanity_domain_status SET %s = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE full_host = ?", column)
	_, err := repo.conn.Exec(query, fullHost)
	return err
}

// MarkVanityDomainFailed records which stage failed and why, without touching whichever
// earlier stage timestamps already succeeded -- so the portal can still show, e.g., a green
// tick for "nginx config written" alongside a red cross for "certificate issued".
func (repo *SQLiteVanityDomainStatusRepo) MarkVanityDomainFailed(fullHost, failedStage, errorMessage string) error {
	query := "UPDATE vanity_domain_status SET failed_stage = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP WHERE full_host = ?"
	_, err := repo.conn.Exec(query, failedStage, errorMessage, fullHost)
	return err
}

func (repo *SQLiteVanityDomainStatusRepo) GetVanityDomainStatus(fullHost string) (*VanityDomainStatus, error) {
	query := "SELECT " + vanityDomainStatusColumns + " FROM vanity_domain_status WHERE full_host = ?"
	s, err := scanVanityDomainStatus(repo.conn.QueryRow(query, fullHost))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

func (repo *SQLiteVanityDomainStatusRepo) ListVanityDomainStatusForUser(userID string) ([]*VanityDomainStatus, error) {
	query := "SELECT " + vanityDomainStatusColumns + " FROM vanity_domain_status WHERE user_id = ? ORDER BY requested_at DESC"
	return repo.listVanityDomainStatus(query, userID)
}

func (repo *SQLiteVanityDomainStatusRepo) ListAllVanityDomainStatus() ([]*VanityDomainStatus, error) {
	query := "SELECT " + vanityDomainStatusColumns + " FROM vanity_domain_status ORDER BY requested_at DESC"
	return repo.listVanityDomainStatus(query)
}

func (repo *SQLiteVanityDomainStatusRepo) listVanityDomainStatus(query string, args ...interface{}) ([]*VanityDomainStatus, error) {
	rows, err := repo.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*VanityDomainStatus
	for rows.Next() {
		s, err := scanVanityDomainStatus(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting GetVanityDomainStatus and
// listVanityDomainStatus share one scan implementation.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanVanityDomainStatus(scanner rowScanner) (*VanityDomainStatus, error) {
	var s VanityDomainStatus
	var requestedAt, nginxConfigAt, certIssuedAt, liveAt sql.NullTime
	var failedStage, errorMessage sql.NullString
	if err := scanner.Scan(&s.FullHost, &s.UserID, &requestedAt, &nginxConfigAt, &certIssuedAt, &liveAt, &failedStage, &errorMessage, &s.UpdatedAt); err != nil {
		return nil, err
	}
	if requestedAt.Valid {
		s.RequestedAt = &requestedAt.Time
	}
	if nginxConfigAt.Valid {
		s.NginxConfigAt = &nginxConfigAt.Time
	}
	if certIssuedAt.Valid {
		s.CertIssuedAt = &certIssuedAt.Time
	}
	if liveAt.Valid {
		s.LiveAt = &liveAt.Time
	}
	if failedStage.Valid {
		s.FailedStage = failedStage.String
	}
	if errorMessage.Valid {
		s.ErrorMessage = errorMessage.String
	}
	return &s, nil
}
