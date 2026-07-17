package server

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// BackupDatabase clones the active SQLite database to a secure backups folder.
func (s *Server) BackupDatabase() error {
	if s.db == nil {
		return fmt.Errorf("database not configured")
	}

	backupsDir := filepath.Join(filepath.Dir(s.cfg.DBPath), "backups")
	if err := os.MkdirAll(backupsDir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %v", err)
	}

	// Generate date-stamped filename: lfr-tunnel_backup_2026-06-18.db
	timeStamp := time.Now().Format("2006-01-02_15-04-05")
	backupPath := filepath.Join(backupsDir, fmt.Sprintf("lfr-tunnel_backup_%s.db", timeStamp))

	// Safely clone the database online thread-safely!
	_, err := s.db.GetConnection().Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		return fmt.Errorf("failed to execute SQLite hot online backup: %v", err)
	}

	slog.Info(fmt.Sprintf("[Server] SQLite hot online database backup completed successfully: %s", backupPath))
	return nil
}

// startDatabaseBackupScheduler triggers daily automated background backups.
func (s *Server) startDatabaseBackupScheduler() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		// Execute initial database backup on server startup
		if err := s.BackupDatabase(); err != nil {
			slog.Info(fmt.Sprintf("[Warning] Initial database startup backup failed: %v", err))
		}

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.BackupDatabase(); err != nil {
					slog.Info(fmt.Sprintf("[Error] Scheduled daily database backup failed: %v", err))
				}
			}
		}
	}()
}
