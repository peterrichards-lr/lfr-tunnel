package db

import (
	"database/sql"
	"errors"
)

type SQLiteSettingsRepo struct {
	conn *sql.DB
}

func NewSQLiteSettingsRepo(conn *sql.DB) *SQLiteSettingsRepo {
	return &SQLiteSettingsRepo{conn: conn}
}

// GetAdminSetting retrieves a setting value by key. Returns empty string if not found.
func (repo *SQLiteSettingsRepo) GetAdminSetting(key string) (string, error) {
	var value string
	err := repo.conn.QueryRow("SELECT value FROM admin_settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// GetAdminSettingOptional retrieves a setting value by key, returning a boolean indicating if it exists.
func (repo *SQLiteSettingsRepo) GetAdminSettingOptional(key string) (string, bool, error) {
	var value string
	err := repo.conn.QueryRow("SELECT value FROM admin_settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SetAdminSetting updates or inserts a setting.
func (repo *SQLiteSettingsRepo) SetAdminSetting(key, value string) error {
	_, err := repo.conn.Exec(`
		INSERT INTO admin_settings (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}
