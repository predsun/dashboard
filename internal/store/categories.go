package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/predsun/dashboard/internal/models"
)

type Categories struct{ DB DBTX }

func (s Categories) Create(ctx context.Context, c *models.Category) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO categories (name, sort_order) VALUES (?, ?)`, c.Name, c.SortOrder)
	if err != nil {
		return fmt.Errorf("insert category: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

func (s Categories) List(ctx context.Context) ([]*models.Category, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, sort_order FROM categories ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	var out []*models.Category
	for rows.Next() {
		c := &models.Category{}
		if err := rows.Scan(&c.ID, &c.Name, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s Categories) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM categories WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s Categories) Get(ctx context.Context, id int64) (*models.Category, error) {
	c := &models.Category{}
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, sort_order FROM categories WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.SortOrder)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}
