package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Storing collected diagnostic bundles (#1894, part 3b of #1763).
//
// This is the first user data the gateway keeps on the owner's behalf. #1696 deliberately created
// no store so that the moment would be a decision, and these are the decisions:
//
//   - SQLite rather than files on disk, so a GDPR erasure is a DELETE the schema enforces
//     (ON DELETE CASCADE) rather than an unlink somebody has to remember. An orphaned file is
//     what makes "we deleted it" untrue without anyone noticing.
//   - Withdrawing consent deletes what has already been collected, not just what would be
//     collected next. #1763: "we stop collecting new ones is usually not sufficient", and the
//     portal already promises withdrawal takes effect immediately.
//   - A stated retention period, because PRIVACY.md now names one. "Only as long as necessary"
//     cannot be contradicted by anything and is therefore not a commitment; "at most 30 days" is
//     checkable, and obliges the sweep below to actually run.

// DiagnosticsRetentionDays is how long a collected bundle is kept.
//
// Named here rather than configured, because PRIVACY.md states it to users. A per-deployment
// value would make the published policy wrong for every deployment that changed it, which is a
// worse failure than an operator wanting a different number.
const DiagnosticsRetentionDays = 30

// SQLiteDiagnosticsRepo stores collected bundles.
type SQLiteDiagnosticsRepo struct {
	conn *sql.DB
}

// NewSQLiteDiagnosticsRepo returns a repository over the given connection.
func NewSQLiteDiagnosticsRepo(conn *sql.DB) *SQLiteDiagnosticsRepo {
	return &SQLiteDiagnosticsRepo{conn: conn}
}

// DiagnosticsBundle is one collected log file.
type DiagnosticsBundle struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	RequestedBy string    `json:"requested_by"`
	Kind        string    `json:"kind"`
	Bytes       int       `json:"bytes"`
	Truncated   bool      `json:"truncated"`
	Dropped     int       `json:"dropped_lines"`
	CollectedAt time.Time `json:"collected_at"`
	// Content is omitted from listings: a list is metadata, and shipping every byte of every
	// bundle to render a table would be both slow and a wider exposure than the page needs.
	Content []byte `json:"-"`
}

// StoreDiagnosticsBundle records one uploaded bundle.
func (repo *SQLiteDiagnosticsRepo) StoreDiagnosticsBundle(b *DiagnosticsBundle) error {
	if b == nil || b.ID == "" || b.UserID == "" {
		return fmt.Errorf("diagnostics bundle: id and user are required")
	}
	truncated := 0
	if b.Truncated {
		truncated = 1
	}
	_, err := repo.conn.Exec(`
		INSERT INTO diagnostics_bundles (id, user_id, requested_by, kind, content, bytes, truncated, dropped_lines)
		VALUES (?,?,?,?,?,?,?,?)`,
		b.ID, b.UserID, b.RequestedBy, b.Kind, b.Content, b.Bytes, truncated, b.Dropped)
	if err != nil {
		return fmt.Errorf("storing diagnostics bundle: %w", err)
	}
	return nil
}

// ListDiagnosticsBundles returns the metadata for a user's bundles, newest first, without their
// contents.
func (repo *SQLiteDiagnosticsRepo) ListDiagnosticsBundles(userID string) ([]DiagnosticsBundle, error) {
	rows, err := repo.conn.Query(`
		SELECT id, user_id, requested_by, kind, bytes, truncated, dropped_lines, collected_at
		FROM diagnostics_bundles WHERE user_id = ? ORDER BY collected_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing diagnostics bundles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []DiagnosticsBundle
	for rows.Next() {
		var b DiagnosticsBundle
		var truncated int
		if err := rows.Scan(&b.ID, &b.UserID, &b.RequestedBy, &b.Kind, &b.Bytes,
			&truncated, &b.Dropped, &b.CollectedAt); err != nil {
			return nil, fmt.Errorf("scanning diagnostics bundle: %w", err)
		}
		b.Truncated = truncated == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetDiagnosticsBundle returns one bundle including its content, or nil when it does not exist.
func (repo *SQLiteDiagnosticsRepo) GetDiagnosticsBundle(id string) (*DiagnosticsBundle, error) {
	var b DiagnosticsBundle
	var truncated int
	err := repo.conn.QueryRow(`
		SELECT id, user_id, requested_by, kind, content, bytes, truncated, dropped_lines, collected_at
		FROM diagnostics_bundles WHERE id = ?`, id).
		Scan(&b.ID, &b.UserID, &b.RequestedBy, &b.Kind, &b.Content, &b.Bytes,
			&truncated, &b.Dropped, &b.CollectedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading diagnostics bundle: %w", err)
	}
	b.Truncated = truncated == 1
	return &b, nil
}

// DeleteDiagnosticsBundlesForUser removes everything collected from one user and reports how many
// rows went.
//
// Called when consent is withdrawn. The count is returned so the withdrawal can be audited with
// what it actually destroyed -- "consent withdrawn" and "consent withdrawn, 3 bundles deleted"
// are different records, and only the second one shows the erasure happened.
func (repo *SQLiteDiagnosticsRepo) DeleteDiagnosticsBundlesForUser(userID string) (int64, error) {
	res, err := repo.conn.Exec(`DELETE FROM diagnostics_bundles WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("deleting diagnostics bundles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // the delete succeeded; not knowing the count is not a failure
	}
	return n, nil
}

// PruneDiagnosticsBundles deletes everything older than the retention period and reports how many
// rows went, so the sweep can say what it did rather than running silently.
func (repo *SQLiteDiagnosticsRepo) PruneDiagnosticsBundles() (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -DiagnosticsRetentionDays)
	res, err := repo.conn.Exec(`DELETE FROM diagnostics_bundles WHERE collected_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning diagnostics bundles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
