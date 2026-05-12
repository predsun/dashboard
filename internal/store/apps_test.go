package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/predsun/dashboard/internal/db"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

func openTestDB(t *testing.T) *appsFixture {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &appsFixture{ctx: context.Background(), apps: store.Apps{DB: conn}}
}

type appsFixture struct {
	ctx  context.Context
	apps store.Apps
}

func TestAppsCRUD(t *testing.T) {
	f := openTestDB(t)

	a := &models.App{Name: "Grafana", URL: "https://grafana.example.com", SortOrder: 0}
	if err := f.apps.Create(f.ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("expected non-zero id after create")
	}

	got, err := f.apps.Get(f.ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Grafana" || got.URL != "https://grafana.example.com" {
		t.Fatalf("unexpected fetched app: %+v", got)
	}

	got.Name = "Grafana Prod"
	if err := f.apps.Update(f.ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, _ := f.apps.Get(f.ctx, a.ID)
	if again.Name != "Grafana Prod" {
		t.Fatalf("update did not persist: %+v", again)
	}

	if err := f.apps.Delete(f.ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.apps.Get(f.ctx, a.ID); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestAppsReorder(t *testing.T) {
	f := openTestDB(t)

	ids := []int64{}
	for _, name := range []string{"a", "b", "c"} {
		a := &models.App{Name: name, URL: "https://x"}
		if err := f.apps.Create(f.ctx, a); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		ids = append(ids, a.ID)
	}

	// Reverse the order in the same (uncategorized) group.
	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := f.apps.Reorder(f.ctx, []store.ReorderGroup{{CategoryID: nil, IDs: reversed}}); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	list, err := f.apps.List(f.ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 apps, got %d", len(list))
	}
	if list[0].ID != reversed[0] || list[1].ID != reversed[1] || list[2].ID != reversed[2] {
		t.Fatalf("reorder did not stick: %v", []int64{list[0].ID, list[1].ID, list[2].ID})
	}
}
