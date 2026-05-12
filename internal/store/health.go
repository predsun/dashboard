package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/predsun/dashboard/internal/models"
)

type Health struct{ DB DBTX }

func (s Health) Get(ctx context.Context, appID int64) (*models.HealthStatus, error) {
	h := &models.HealthStatus{}
	var (
		last sql.NullInt64
		next sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT app_id, status, last_checked_at, consecutive_failures, next_check_at FROM health_status WHERE app_id=?`, appID,
	).Scan(&h.AppID, &h.Status, &last, &h.ConsecutiveFailures, &next)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if last.Valid {
		v := last.Int64
		h.LastCheckedAt = &v
	}
	if next.Valid {
		v := next.Int64
		h.NextCheckAt = &v
	}
	return h, nil
}

// Upsert writes the latest probe result. The caller computes next_check_at
// (typically `now + base * 2^failures`, capped).
func (s Health) Upsert(ctx context.Context, h *models.HealthStatus) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO health_status (app_id, status, last_checked_at, consecutive_failures, next_check_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(app_id) DO UPDATE SET
			status               = excluded.status,
			last_checked_at      = excluded.last_checked_at,
			consecutive_failures = excluded.consecutive_failures,
			next_check_at        = excluded.next_check_at`,
		h.AppID, h.Status, h.LastCheckedAt, h.ConsecutiveFailures, h.NextCheckAt,
	)
	if err != nil {
		return fmt.Errorf("upsert health: %w", err)
	}
	return nil
}

// AllByAppID returns a map keyed by app_id, useful when rendering the dashboard
// to colocate status with each tile in O(N+M) instead of N+1 queries.
func (s Health) AllByAppID(ctx context.Context) (map[int64]*models.HealthStatus, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT app_id, status, last_checked_at, consecutive_failures, next_check_at FROM health_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*models.HealthStatus{}
	for rows.Next() {
		h := &models.HealthStatus{}
		var last, next sql.NullInt64
		if err := rows.Scan(&h.AppID, &h.Status, &last, &h.ConsecutiveFailures, &next); err != nil {
			return nil, err
		}
		if last.Valid {
			v := last.Int64
			h.LastCheckedAt = &v
		}
		if next.Valid {
			v := next.Int64
			h.NextCheckAt = &v
		}
		out[h.AppID] = h
	}
	return out, rows.Err()
}
