package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// Apps serves /api/apps and friends. Responses are JSON; mutation requests
// have already been CSRF-validated by the middleware chain.
type Apps struct {
	DB     *sql.DB
	Logger *slog.Logger
}

// appPayload is the request body shape for create / update. Pointers let us
// distinguish "absent" from "set to empty / zero".
type appPayload struct {
	Name               *string `json:"name,omitempty"`
	URL                *string `json:"url,omitempty"`
	Description        *string `json:"description,omitempty"`
	IconPath           *string `json:"icon_path,omitempty"`
	CategoryID         *int64  `json:"category_id,omitempty"`
	SortOrder          *int    `json:"sort_order,omitempty"`
	HealthCheckEnabled *bool   `json:"health_check_enabled,omitempty"`
	HealthCheckURL     *string `json:"health_check_url,omitempty"`
}

const (
	maxNameLen        = 100
	maxURLLen         = 2048
	maxDescriptionLen = 500
)

func (h Apps) List(w http.ResponseWriter, r *http.Request) {
	apps, err := (store.Apps{DB: h.DB}).List(r.Context())
	if err != nil {
		h.fail(w, http.StatusInternalServerError, "list apps")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (h Apps) Create(w http.ResponseWriter, r *http.Request) {
	p, err := readPayload(r)
	if err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Name == nil || p.URL == nil {
		h.fail(w, http.StatusBadRequest, "name and url are required")
		return
	}
	app := &models.App{}
	if err := applyPayload(app, p, true); err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := (store.Apps{DB: h.DB}).Create(r.Context(), app); err != nil {
		h.Logger.Error("create app", "err", err)
		h.fail(w, http.StatusInternalServerError, "create app")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"app": app})
}

func (h Apps) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	p, err := readPayload(r)
	if err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	apps := store.Apps{DB: h.DB}
	app, err := apps.Get(r.Context(), id)
	if err != nil {
		h.fail(w, http.StatusNotFound, "app not found")
		return
	}
	if err := applyPayload(app, p, false); err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := apps.Update(r.Context(), app); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.fail(w, http.StatusNotFound, "app not found")
			return
		}
		h.Logger.Error("update app", "err", err)
		h.fail(w, http.StatusInternalServerError, "update app")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": app})
}

func (h Apps) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	if err := (store.Apps{DB: h.DB}).Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.fail(w, http.StatusNotFound, "app not found")
			return
		}
		h.Logger.Error("delete app", "err", err)
		h.fail(w, http.StatusInternalServerError, "delete app")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reorderGroupPayload struct {
	CategoryID *int64  `json:"category_id"`
	IDs        []int64 `json:"ids"`
}

type reorderPayload struct {
	Groups []reorderGroupPayload `json:"groups"`
}

func (h Apps) Reorder(w http.ResponseWriter, r *http.Request) {
	var p reorderPayload
	if err := decodeJSON(r, &p); err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(p.Groups) == 0 {
		h.fail(w, http.StatusBadRequest, "groups must be non-empty")
		return
	}
	// Reject empty-id groups; SortableJS shouldn't send them, but a malformed
	// client could leave dangling apps if we silently accepted.
	groups := make([]store.ReorderGroup, 0, len(p.Groups))
	for _, g := range p.Groups {
		if len(g.IDs) == 0 {
			continue
		}
		groups = append(groups, store.ReorderGroup{CategoryID: g.CategoryID, IDs: g.IDs})
	}
	if len(groups) == 0 {
		h.fail(w, http.StatusBadRequest, "at least one group must contain ids")
		return
	}
	if err := (store.Apps{DB: h.DB}).Reorder(r.Context(), groups); err != nil {
		h.Logger.Error("reorder", "err", err)
		h.fail(w, http.StatusInternalServerError, "reorder")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Apps) fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// applyPayload merges the payload into target. forCreate=true requires
// name/url to be present and validates them; forCreate=false treats absent
// fields as "no change".
func applyPayload(target *models.App, p appPayload, forCreate bool) error {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("name is required")
		}
		if utf8.RuneCountInString(name) > maxNameLen {
			return fmt.Errorf("name too long (max %d)", maxNameLen)
		}
		target.Name = name
	} else if forCreate {
		return fmt.Errorf("name is required")
	}

	if p.URL != nil {
		u := strings.TrimSpace(*p.URL)
		if err := validateURL(u); err != nil {
			return err
		}
		target.URL = u
	} else if forCreate {
		return fmt.Errorf("url is required")
	}

	if p.Description != nil {
		d := strings.TrimSpace(*p.Description)
		if utf8.RuneCountInString(d) > maxDescriptionLen {
			return fmt.Errorf("description too long (max %d)", maxDescriptionLen)
		}
		target.Description = d
	}
	if p.IconPath != nil {
		// Icon path is a hash-derived filename produced by /api/uploads/icon,
		// not user-controlled text. Reject anything containing path separators.
		ip := strings.TrimSpace(*p.IconPath)
		if strings.ContainsAny(ip, "/\\") || strings.Contains(ip, "..") {
			return fmt.Errorf("icon_path must be a bare filename")
		}
		target.IconPath = ip
	}
	if p.CategoryID != nil {
		// Treat 0 as "clear category"; otherwise FK enforces existence.
		if *p.CategoryID <= 0 {
			target.CategoryID = nil
		} else {
			v := *p.CategoryID
			target.CategoryID = &v
		}
	}
	if p.SortOrder != nil {
		target.SortOrder = *p.SortOrder
	}
	if p.HealthCheckEnabled != nil {
		target.HealthCheckEnabled = *p.HealthCheckEnabled
	}
	if p.HealthCheckURL != nil {
		hcu := strings.TrimSpace(*p.HealthCheckURL)
		if hcu != "" {
			if err := validateURL(hcu); err != nil {
				return fmt.Errorf("health_check_url: %w", err)
			}
		}
		target.HealthCheckURL = hcu
	}
	return nil
}

// validateURL rejects everything that isn't a fully-formed http(s) URL.
// Heimdall historically allowed javascript: URLs by mistake — we explicitly don't.
func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	if len(raw) > maxURLLen {
		return fmt.Errorf("url too long (max %d)", maxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("url must have a host")
	}
	return nil
}

func readPayload(r *http.Request) (appPayload, error) {
	var p appPayload
	if err := decodeJSON(r, &p); err != nil {
		return appPayload{}, err
	}
	return p, nil
}

const maxJSONBytes = 64 * 1024 // 64 KiB is plenty for a single app payload

func decodeJSON(r *http.Request, dst any) error {
	defer io.Copy(io.Discard, r.Body) //nolint:errcheck
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "json marshal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func pathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}
