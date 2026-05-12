// Package store wraps raw SQL access to each aggregate. One file per
// aggregate. Stores accept a DBTX so the same code path works inside or
// outside a transaction.
package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a single-row lookup matches zero rows.
var ErrNotFound = errors.New("not found")

// DBTX is the subset of database/sql used by all stores. Both *sql.DB and
// *sql.Tx satisfy it.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
