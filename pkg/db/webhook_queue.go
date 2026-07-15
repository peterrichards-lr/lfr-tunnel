package db

import (
	"database/sql"
	"time"
)

type sqliteWebhookQueueRepo struct {
	conn *sql.DB
}

func NewSQLiteWebhookQueueRepo(conn *sql.DB) *sqliteWebhookQueueRepo {
	return &sqliteWebhookQueueRepo{conn: conn}
}

func (r *sqliteWebhookQueueRepo) EnqueueWebhookMessage(title, description, color, factsJSON string) error {
	query := `INSERT INTO webhook_queue (title, description, color, facts) VALUES (?, ?, ?, ?)`
	_, err := r.conn.Exec(query, title, description, color, factsJSON)
	return err
}

func (r *sqliteWebhookQueueRepo) DequeueWebhookMessages(limit int) ([]*QueuedWebhookMessage, error) {
	query := `SELECT id, title, description, color, facts, created_at FROM webhook_queue ORDER BY id ASC LIMIT ?`
	rows, err := r.conn.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var msgs []*QueuedWebhookMessage
	for rows.Next() {
		var msg QueuedWebhookMessage
		var createdAtStr string
		if err := rows.Scan(&msg.ID, &msg.Title, &msg.Description, &msg.Color, &msg.Facts, &createdAtStr); err != nil {
			return nil, err
		}
		// Parse date
		if t, err := time.Parse("2006-01-02T15:04:05Z", createdAtStr); err == nil {
			msg.CreatedAt = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
			msg.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			msg.CreatedAt = t
		} else {
			msg.CreatedAt = time.Now()
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

func (r *sqliteWebhookQueueRepo) DeleteWebhookMessages(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(`DELETE FROM webhook_queue WHERE id = ?`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
