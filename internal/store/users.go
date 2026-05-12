package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/predsun/dashboard/internal/models"
)

type Users struct{ DB DBTX }

func (s Users) Create(ctx context.Context, username, passwordHash string) (*models.User, error) {
	u := &models.User{
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().Unix(),
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		u.Username, u.PasswordHash, u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	u.ID = id
	return u, nil
}

func (s Users) ByUsername(ctx context.Context, username string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username=?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

func (s Users) ByID(ctx context.Context, id int64) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE id=?`, id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

func (s Users) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s Users) UpdatePassword(ctx context.Context, id int64, hash string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
