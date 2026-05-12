package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/predsun/dashboard/internal/models"
)

type Apps struct{ DB DBTX }

func (s Apps) Create(ctx context.Context, a *models.App) error {
	now := time.Now().Unix()
	a.CreatedAt = now
	a.UpdatedAt = now
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO apps (name, url, description, icon_path, category_id, sort_order, health_check_enabled, health_check_url, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.URL, a.Description, a.IconPath, a.CategoryID, a.SortOrder, boolToInt(a.HealthCheckEnabled), a.HealthCheckURL, a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert app: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("app last id: %w", err)
	}
	a.ID = id
	return nil
}

func (s Apps) Update(ctx context.Context, a *models.App) error {
	a.UpdatedAt = time.Now().Unix()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE apps SET name=?, url=?, description=?, icon_path=?, category_id=?, sort_order=?, health_check_enabled=?, health_check_url=?, updated_at=?
		 WHERE id=?`,
		a.Name, a.URL, a.Description, a.IconPath, a.CategoryID, a.SortOrder, boolToInt(a.HealthCheckEnabled), a.HealthCheckURL, a.UpdatedAt, a.ID,
	)
	if err != nil {
		return fmt.Errorf("update app: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s Apps) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM apps WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete app: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s Apps) Get(ctx context.Context, id int64) (*models.App, error) {
	row := s.DB.QueryRowContext(ctx, appSelect+` WHERE id=?`, id)
	a, err := scanApp(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

func (s Apps) List(ctx context.Context) ([]*models.App, error) {
	rows, err := s.DB.QueryContext(ctx, appSelect+` ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()
	var out []*models.App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReorderGroup is one category's slice of app IDs in their new visual order.
// CategoryID may be nil to represent "uncategorized".
type ReorderGroup struct {
	CategoryID *int64
	IDs        []int64
}

// Reorder rewrites sort_order across all groups in a single transaction and
// updates category_id where the dragged tile crossed into a new bucket. The
// dashboard never observes a half-committed shuffle.
func (s Apps) Reorder(ctx context.Context, groups []ReorderGroup) error {
	db, ok := s.DB.(*sql.DB)
	if !ok {
		// Already inside a transaction — caller controls atomicity.
		return reorderExec(ctx, s.DB, groups)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder: %w", err)
	}
	if err := reorderExec(ctx, tx, groups); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func reorderExec(ctx context.Context, db DBTX, groups []ReorderGroup) error {
	now := time.Now().Unix()
	for _, g := range groups {
		for i, id := range g.IDs {
			if _, err := db.ExecContext(ctx,
				`UPDATE apps SET category_id=?, sort_order=?, updated_at=? WHERE id=?`,
				g.CategoryID, i, now, id,
			); err != nil {
				return fmt.Errorf("reorder app %d: %w", id, err)
			}
		}
	}
	return nil
}

// AppsDueForHealthCheck returns apps where health_check_enabled=1 and the
// next_check_at watermark has passed (or the row does not exist yet).
func (s Apps) DueForHealthCheck(ctx context.Context, now int64) ([]*models.App, error) {
	rows, err := s.DB.QueryContext(ctx, appSelect+`
		LEFT JOIN health_status h ON h.app_id = apps.id
		WHERE apps.health_check_enabled = 1
		  AND (h.next_check_at IS NULL OR h.next_check_at <= ?)
		ORDER BY apps.id`, now)
	if err != nil {
		return nil, fmt.Errorf("due health checks: %w", err)
	}
	defer rows.Close()
	var out []*models.App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const appSelect = `SELECT apps.id, apps.name, apps.url, apps.description, apps.icon_path, apps.category_id, apps.sort_order, apps.health_check_enabled, apps.health_check_url, apps.created_at, apps.updated_at FROM apps`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanApp(r rowScanner) (*models.App, error) {
	a := &models.App{}
	var (
		catID    sql.NullInt64
		hcEnabled int
	)
	if err := r.Scan(&a.ID, &a.Name, &a.URL, &a.Description, &a.IconPath, &catID, &a.SortOrder, &hcEnabled, &a.HealthCheckURL, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if catID.Valid {
		v := catID.Int64
		a.CategoryID = &v
	}
	a.HealthCheckEnabled = hcEnabled != 0
	return a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
