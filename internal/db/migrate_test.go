package db

import (
	"path/filepath"
	"testing"
)

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := Migrate(conn); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatalf("second migrate (should be a no-op): %v", err)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected at least one migration to be recorded")
	}

	// Sanity: core tables exist.
	for _, table := range []string{"users", "categories", "apps", "health_status", "settings", "sessions"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing: %v", table, err)
		}
	}
}
