package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// ImportExport serves the JSON backup endpoints. Exports never include the
// session_key / csrf_key / password_hash by default; the caller can opt in
// with ?include_secrets=1 if they explicitly want a self-contained restore.
type ImportExport struct {
	DB     *sql.DB
	Logger *slog.Logger
}

// exportEnvelope is the on-disk shape. Schema version lets future imports
// know what they're looking at and lets us refuse incompatible bundles.
type exportEnvelope struct {
	SchemaVersion int                 `json:"schema_version"`
	Apps          []*models.App       `json:"apps"`
	Categories    []*models.Category  `json:"categories"`
	Settings      map[string]string   `json:"settings"`
}

const currentExportSchema = 1

// excludedSettingKeys are skipped from exports unless ?include_secrets=1.
var excludedSettingKeys = map[string]bool{
	models.SettingSessionKey: true,
	models.SettingCSRFKey:    true,
}

func (h ImportExport) Export(w http.ResponseWriter, r *http.Request) {
	includeSecrets := r.URL.Query().Get("include_secrets") == "1"

	apps, err := (store.Apps{DB: h.DB}).List(r.Context())
	if err != nil {
		h.fail(w, "list apps", err)
		return
	}
	cats, err := (store.Categories{DB: h.DB}).List(r.Context())
	if err != nil {
		h.fail(w, "list categories", err)
		return
	}
	all, err := (store.Settings{DB: h.DB}).All(r.Context())
	if err != nil {
		h.fail(w, "list settings", err)
		return
	}
	if !includeSecrets {
		for k := range excludedSettingKeys {
			delete(all, k)
		}
	}

	envelope := exportEnvelope{
		SchemaVersion: currentExportSchema,
		Apps:          apps,
		Categories:    cats,
		Settings:      all,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="dashboard-export.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(envelope)
}

// Import reads an export envelope and replays it on top of the current DB.
// We don't wipe-and-replace by default; instead we recreate categories by
// name (re-using existing ones if matched) and create apps with a fresh
// sort_order range so an import doesn't visually shuffle existing tiles.
func (h ImportExport) Import(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50 MiB cap
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read body"})
		return
	}
	var env exportEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if env.SchemaVersion != currentExportSchema {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "unsupported schema version",
		})
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		h.fail(w, "begin", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	cats := store.Categories{DB: tx}
	apps := store.Apps{DB: tx}

	// Rebuild a name -> id lookup over existing categories. Imported
	// categories with a matching name re-use the local id; others are created.
	existing, err := cats.List(r.Context())
	if err != nil {
		h.fail(w, "list cats", err)
		return
	}
	idByName := map[string]int64{}
	for _, c := range existing {
		idByName[c.Name] = c.ID
	}

	// Map import's category_id -> our local category_id.
	importedIDToLocalID := map[int64]int64{}
	for _, c := range env.Categories {
		if local, ok := idByName[c.Name]; ok {
			importedIDToLocalID[c.ID] = local
			continue
		}
		newCat := &models.Category{Name: c.Name, SortOrder: c.SortOrder}
		if err := cats.Create(r.Context(), newCat); err != nil {
			h.fail(w, "create cat", err)
			return
		}
		idByName[c.Name] = newCat.ID
		importedIDToLocalID[c.ID] = newCat.ID
	}

	// Compute the next sort order: append imports to the end of the existing list.
	currentApps, err := apps.List(r.Context())
	if err != nil {
		h.fail(w, "list apps", err)
		return
	}
	nextSort := len(currentApps)

	created := 0
	for _, a := range env.Apps {
		newApp := *a
		newApp.ID = 0 // let DB assign
		newApp.SortOrder = nextSort
		nextSort++
		if a.CategoryID != nil {
			if local, ok := importedIDToLocalID[*a.CategoryID]; ok {
				newApp.CategoryID = &local
			} else {
				newApp.CategoryID = nil
			}
		}
		if err := apps.Create(r.Context(), &newApp); err != nil {
			h.fail(w, "create app", err)
			return
		}
		created++
	}

	if err := tx.Commit(); err != nil {
		h.fail(w, "commit", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported_apps":       created,
		"imported_categories": len(env.Categories),
	})
}

func (h ImportExport) fail(w http.ResponseWriter, op string, err error) {
	h.Logger.Error("import/export", "op", op, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": op + " failed"})
}
