package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB

	UserRepository
	PATRepository
	SubdomainRepository
	AuditRepository
	MetricRepository
	MagicLinkRepository
	BlacklistRepository
	GuestInviteRepository
	SettingsRepository
	SystemRepository
}

func Open(dsn string) (*DB, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	} else {
		dsn += "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite DB: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec("PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;"); err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to execute pragma: %w", err)
	}

	d := &DB{
		conn:                  conn,
		UserRepository:        NewSQLiteUserRepo(conn),
		PATRepository:         NewSQLitePATRepo(conn),
		SubdomainRepository:   NewSQLiteSubdomainRepo(conn),
		AuditRepository:       NewSQLiteAuditRepo(conn),
		MetricRepository:      NewSQLiteMetricRepo(conn),
		MagicLinkRepository:   NewSQLiteMagicLinkRepo(conn),
		BlacklistRepository:   NewSQLiteBlacklistRepo(conn),
		GuestInviteRepository: NewSQLiteInviteRepo(conn),
		SettingsRepository:    NewSQLiteSettingsRepo(conn),
		SystemRepository:      NewSQLiteSystemRepo(conn),
	}

	if err := d.initSchema(); err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return d, nil
}
