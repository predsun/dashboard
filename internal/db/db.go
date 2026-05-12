// Package db opens the SQLite database with the pragmas the rest of the app
// expects (WAL journal, foreign keys, busy timeout) and exposes the migration
// runner. The data layer in internal/store assumes these pragmas are set.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

// Open opens (or creates) the SQLite database at path and applies the runtime
// pragmas. The returned *sql.DB is safe for concurrent use.
//
// We keep MaxOpenConns at 1 for writes via a separate write-serialization layer
// in store/, since SQLite serializes writes anyway. Reads are concurrent under
// WAL, so we leave MaxOpenConns to Go's default for reads. In practice on this
// workload the default is fine; if we hit contention we'll split the pool.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return conn, nil
}
