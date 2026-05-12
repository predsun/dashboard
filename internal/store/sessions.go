package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/predsun/dashboard/internal/models"
)

type Sessions struct{ DB DBTX }

func (s Sessions) Create(ctx context.Context, id string, userID int64, ttl time.Duration) (*models.Session, error) {
	sess := &models.Session{
		ID:        id,
		UserID:    userID,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.ExpiresAt, sess.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return sess, nil
}

func (s Sessions) Get(ctx context.Context, id string) (*models.Session, error) {
	sess := &models.Session{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at, created_at FROM sessions WHERE id=? AND expires_at > ?`,
		id, time.Now().Unix(),
	).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return sess, nil
}

func (s Sessions) Delete(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

// PurgeExpired removes sessions past their expires_at. Called opportunistically;
// the index on expires_at keeps it cheap.
func (s Sessions) PurgeExpired(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
