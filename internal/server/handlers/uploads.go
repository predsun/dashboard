package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/predsun/dashboard/internal/uploads"
)

// Uploads serves the /api/uploads/icon and /api/uploads/background endpoints.
// The upload limit is enforced both by http.MaxBytesReader (to cap memory) and
// by uploads.Save (to double-check after the multipart parser).
type Uploads struct {
	DB             *sql.DB
	Logger         *slog.Logger
	IconsDir       string
	BackgroundsDir string
	MaxIconBytes   int64
	MaxBgBytes     int64
}

func (h Uploads) Icon(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, uploads.KindIcon, h.IconsDir, h.MaxIconBytes)
}

func (h Uploads) Background(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, uploads.KindBackground, h.BackgroundsDir, h.MaxBgBytes)
}

func (h Uploads) handle(w http.ResponseWriter, r *http.Request, kind uploads.Kind, dir string, maxBytes int64) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<14)) // +16 KiB headroom for multipart framing
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "upload too large"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing file"})
		return
	}
	defer file.Close()

	name, err := uploads.Save(file, header, kind, dir, maxBytes)
	if err != nil {
		switch {
		case errors.Is(err, uploads.ErrTooLarge):
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "upload too large"})
		case errors.Is(err, uploads.ErrUnsupportedMIME):
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": err.Error()})
		case errors.Is(err, uploads.ErrEmpty):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "empty upload"})
		default:
			h.Logger.Error("upload", "err", err, "kind", kind)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "upload failed"})
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"filename": name})
}
