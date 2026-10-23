package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"wisp/daemon/internal/api"
)

// SetSetting stores a raw JSON value.
func (s *Store) SetSetting(ctx context.Context, key, jsonValue string) error {
	if key == "" {
		return api.InvalidParams("empty setting key")
	}
	if !json.Valid([]byte(jsonValue)) {
		return api.InvalidParams("setting %s: value is not valid JSON", key)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, jsonValue)
		if err != nil {
			return fmt.Errorf("store: set setting: %w", err)
		}
		return nil
	})
}

// AllSettings returns every stored setting as raw JSON.
func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return allSettings(ctx, s.r)
}

func allSettings(ctx context.Context, q querier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("store: settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
