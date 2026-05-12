// Package handlers contains the HTTP handler implementations registered by
// internal/server/routes.go. Handlers are grouped by aggregate, one file
// per resource.
package handlers

import (
	"database/sql"
	"net/http"
)

// Health serves /healthz (liveness) and /readyz (readiness). Liveness is a
// pure-stdlib check that the process is alive; readiness pings the DB so a
// load balancer can drain the node when the database is unhappy.
type Health struct {
	DB *sql.DB
}

func (h Health) Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (h Health) Readiness(w http.ResponseWriter, r *http.Request) {
	if err := h.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "db not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready\n"))
}
