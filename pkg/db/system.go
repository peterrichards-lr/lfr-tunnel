package db

import (
	"database/sql"
)

type SQLiteSystemRepo struct {
	conn *sql.DB
}

func NewSQLiteSystemRepo(conn *sql.DB) *SQLiteSystemRepo {
	return &SQLiteSystemRepo{conn: conn}
}

// Close shuts down the database connection.
func (repo *SQLiteSystemRepo) Close() error {
	return repo.conn.Close()
}

// GetConnection returns the underlying sql.DB connection instance for advanced administrative operations.
func (repo *SQLiteSystemRepo) GetConnection() *sql.DB {
	return repo.conn
}
