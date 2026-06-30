package db

import (
	"database/sql"
	"time"
)

type SQLiteAuditRepo struct {
	conn *sql.DB
}

func NewSQLiteAuditRepo(conn *sql.DB) *SQLiteAuditRepo {
	return &SQLiteAuditRepo{conn: conn}
}

// WriteAuditEntry appends a new entry to the admin_audit_log table.
func (repo *SQLiteAuditRepo) WriteAuditEntry(e *AuditEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	query := `INSERT INTO admin_audit_log (actor_id, action, target_type, target_id, details, ip_address, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	res, err := repo.conn.Exec(query, e.ActorID, e.Action, e.TargetType, e.TargetID, e.Details, e.IPAddress, e.CreatedAt)
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
func (repo *SQLiteAuditRepo) ListAuditEntries(f AuditFilter) ([]*AuditEntry, error) {
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

	rows, err := repo.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
