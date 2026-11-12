package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/settings"
)

// SettingsUnitOfWork implements settings.UnitOfWork on top of SQLite.
type SettingsUnitOfWork struct {
	db *sql.DB
}

// NewSettingsUnitOfWork builds a settings unit of work.
func NewSettingsUnitOfWork(db *sql.DB) *SettingsUnitOfWork {
	return &SettingsUnitOfWork{db: db}
}

// Do runs fn inside a transaction.
func (u *SettingsUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx settings.Transaction) error) error {
	return runInTransaction(ctx, u.db, func(tx *sql.Tx) error {
		return fn(ctx, taskTransaction{tx: tx})
	})
}

// settingsRepository is the SQLite implementation of settings.Repository.
type settingsRepository struct {
	tx *sql.Tx
}

// Get returns one value and whether it exists.
func (r *settingsRepository) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var value string
	err := r.tx.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return json.RawMessage(value), true, nil
}

// List returns every setting.
func (r *settingsRepository) List(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT key, value FROM app_settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	values := make(map[string]json.RawMessage)
	for rows.Next() {
		var (
			key   string
			value string
		)
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		values[key] = json.RawMessage(value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan settings: %w", err)
	}
	return values, nil
}

// Put inserts or replaces a value.
func (r *settingsRepository) Put(ctx context.Context, key string, value json.RawMessage, at time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(value), unixSeconds(at))
	if err != nil {
		return fmt.Errorf("put setting %q: %w", key, err)
	}
	return nil
}
