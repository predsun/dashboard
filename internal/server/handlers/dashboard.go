package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sort"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// Dashboard renders the main grid view: apps grouped by category, with health
// status fetched in one query and joined client-side.
type Dashboard struct {
	DB     *sql.DB
	Render SetupRenderer
	Logger *slog.Logger
}

// tileVM is the per-tile view model the _tile partial expects.
type tileVM struct {
	App    *models.App
	Status *models.HealthStatus
}

type groupVM struct {
	ID   int64
	Name string
	Apps []tileVM
}

type dashboardVM struct {
	Title    string
	AppCount int
	Groups   []groupVM
}

const uncategorizedID = int64(0)

func (h Dashboard) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	apps, err := (store.Apps{DB: h.DB}).List(ctx)
	if err != nil {
		h.Logger.Error("list apps", "err", err)
		http.Error(w, "list apps", http.StatusInternalServerError)
		return
	}
	cats, err := (store.Categories{DB: h.DB}).List(ctx)
	if err != nil {
		h.Logger.Error("list categories", "err", err)
		http.Error(w, "list categories", http.StatusInternalServerError)
		return
	}
	statuses, err := (store.Health{DB: h.DB}).AllByAppID(ctx)
	if err != nil {
		// Health is best-effort; a failure here shouldn't block the dashboard.
		h.Logger.Warn("list health", "err", err)
		statuses = nil
	}

	vm := dashboardVM{
		Title:    "Apps",
		AppCount: len(apps),
		Groups:   buildGroups(apps, cats, statuses),
	}
	h.Render.Render(w, r, http.StatusOK, "dashboard", vm)
}

// buildGroups stable-sorts apps into category buckets, preserving the
// per-app sort_order inside each bucket.
func buildGroups(apps []*models.App, cats []*models.Category, statuses map[int64]*models.HealthStatus) []groupVM {
	byCat := map[int64][]*models.App{}
	for _, a := range apps {
		id := uncategorizedID
		if a.CategoryID != nil {
			id = *a.CategoryID
		}
		byCat[id] = append(byCat[id], a)
	}
	for _, list := range byCat {
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].SortOrder < list[j].SortOrder
		})
	}

	groups := make([]groupVM, 0, len(cats)+1)
	for _, c := range cats {
		list := byCat[c.ID]
		if len(list) == 0 {
			continue
		}
		groups = append(groups, groupVM{
			ID:   c.ID,
			Name: c.Name,
			Apps: toTiles(list, statuses),
		})
	}
	if len(byCat[uncategorizedID]) > 0 {
		groups = append(groups, groupVM{
			ID:   uncategorizedID,
			Name: "Uncategorized",
			Apps: toTiles(byCat[uncategorizedID], statuses),
		})
	}
	return groups
}

func toTiles(list []*models.App, statuses map[int64]*models.HealthStatus) []tileVM {
	out := make([]tileVM, 0, len(list))
	for _, a := range list {
		out = append(out, tileVM{App: a, Status: statuses[a.ID]})
	}
	return out
}
