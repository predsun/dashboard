package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type Settings struct{ DB DBTX }

// Get returns the value for key. Missing keys return ("", ErrNotFound) so
// callers can distinguish "absent" from "set to empty string".
func (s Settings) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

// GetOrDefault is the common case during config-style reads.
func (s Settings) GetOrDefault(ctx context.Context, key, def string) (string, error) {
	v, err := s.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return def, nil
	}
	return v, err
}

func (s Settings) Set(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("upsert setting %s: %w", key, err)
	}
	return nil
}

func (s Settings) All(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
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
