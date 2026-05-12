package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// Categories serves /api/categories CRUD. The dashboard's tile editor calls
// these to let users create / rename / delete buckets without leaving the
// page.
type Categories struct {
	DB     *sql.DB
	Logger *slog.Logger
}

type categoryPayload struct {
	Name      *string `json:"name,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
}

const maxCategoryNameLen = 60

func (h Categories) List(w http.ResponseWriter, r *http.Request) {
	cats, err := (store.Categories{DB: h.DB}).List(r.Context())
	if err != nil {
		h.Logger.Error("list categories", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list categories"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

func (h Categories) Create(w http.ResponseWriter, r *http.Request) {
	var p categoryPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	name, err := validCategoryName(p.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	c := &models.Category{Name: name}
	if p.SortOrder != nil {
		c.SortOrder = *p.SortOrder
	}
	if err := (store.Categories{DB: h.DB}).Create(r.Context(), c); err != nil {
		// UNIQUE(name) violation is the common case; surface a 409 instead of 500.
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "category name already exists"})
			return
		}
		h.Logger.Error("create category", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create category"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"category": c})
}

func (h Categories) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	var p categoryPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	cats := store.Categories{DB: h.DB}
	c, err := cats.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "category not found"})
		return
	}
	if p.Name != nil {
		n, err := validCategoryName(p.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		c.Name = n
	}
	if p.SortOrder != nil {
		c.SortOrder = *p.SortOrder
	}
	// No update method on the Categories store yet — inline the SQL.
	_, err = h.DB.ExecContext(r.Context(),
		`UPDATE categories SET name=?, sort_order=? WHERE id=?`,
		c.Name, c.SortOrder, c.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "category name already exists"})
			return
		}
		h.Logger.Error("update category", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "update category"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"category": c})
}

func (h Categories) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	// FK is ON DELETE SET NULL, so existing apps land in "uncategorized".
	if err := (store.Categories{DB: h.DB}).Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "category not found"})
			return
		}
		h.Logger.Error("delete category", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "delete category"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validCategoryName(p *string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("name is required")
	}
	name := strings.TrimSpace(*p)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > maxCategoryNameLen {
		return "", fmt.Errorf("name too long (max %d)", maxCategoryNameLen)
	}
	return name, nil
}

// isUniqueViolation sniffs the modernc.org/sqlite error text. The driver
// doesn't expose typed constraint errors directly; matching the substring is
// the conventional workaround.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
