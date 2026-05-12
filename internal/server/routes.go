package server

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// Middleware is a standard HTTP middleware: takes a handler, returns a wrapped one.
type Middleware func(http.Handler) http.Handler

// chain composes a series of middlewares around final, applied left-to-right
// in declaration order (so the first middleware sees the request first).
func chain(final http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		final = mws[i](final)
	}
	return final
}

// routes wires all handlers and applies the middleware chain. The order in
// `chain(...)` matters: TrustedProxy must populate ClientIP before the
// AccessLog records it; SetupGate must run before RequireAuth so the gate
// doesn't bounce a setup-page request into /login.
func (s *Server) routes(staticFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("GET /healthz", s.health.Liveness)
	mux.HandleFunc("GET /readyz", s.health.Readiness)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// User-uploaded files live under data_dir, not the embed.
	mux.Handle("GET /uploads/icons/", http.StripPrefix("/uploads/icons/", http.FileServer(http.Dir(s.cfg.IconsDir()))))
	mux.Handle("GET /uploads/backgrounds/", http.StripPrefix("/uploads/backgrounds/", http.FileServer(http.Dir(s.cfg.BackgroundsDir()))))

	mux.HandleFunc("GET /setup", s.setup.Get)
	mux.HandleFunc("POST /setup", s.setup.Post)

	mux.HandleFunc("GET /login", s.auth.LoginGet)
	mux.HandleFunc("POST /login", s.auth.LoginPost)
	mux.HandleFunc("POST /logout", s.auth.LogoutPost)

	// Authenticated dashboard.
	mux.Handle("GET /", chain(http.HandlerFunc(s.dashboard.Get), RequireAuth()))

	// API.
	authMW := RequireAuth()
	api := func(h http.Handler) http.Handler { return authMW(h) }

	mux.Handle("GET /api/apps", api(http.HandlerFunc(s.apps.List)))
	mux.Handle("POST /api/apps", api(http.HandlerFunc(s.apps.Create)))
	mux.Handle("PATCH /api/apps/{id}", api(http.HandlerFunc(s.apps.Update)))
	mux.Handle("DELETE /api/apps/{id}", api(http.HandlerFunc(s.apps.Delete)))
	mux.Handle("POST /api/apps/reorder", api(http.HandlerFunc(s.apps.Reorder)))

	mux.Handle("GET /api/categories", api(http.HandlerFunc(s.categories.List)))
	mux.Handle("POST /api/categories", api(http.HandlerFunc(s.categories.Create)))
	mux.Handle("PATCH /api/categories/{id}", api(http.HandlerFunc(s.categories.Update)))
	mux.Handle("DELETE /api/categories/{id}", api(http.HandlerFunc(s.categories.Delete)))

	mux.Handle("POST /api/uploads/icon", api(http.HandlerFunc(s.uploads.Icon)))
	mux.Handle("POST /api/uploads/background", api(http.HandlerFunc(s.uploads.Background)))

	mux.Handle("GET /api/export", api(http.HandlerFunc(s.ie.Export)))
	mux.Handle("POST /api/import", api(http.HandlerFunc(s.ie.Import)))

	return chain(mux,
		Recover(s.logger),
		TrustedProxy(s.cfg.TrustedProxies),
		AccessLog(s.logger),
		SecurityHeaders(s.cfg.TLS.Enabled),
		CSRFGate(s.cfg.TLS.Enabled),
		SessionLoader(s.sessions),
		SetupGate(s.setupComplete),
	)
}

// setupComplete checks the persisted flag. Errors are treated as
// "not complete" 鈥?the wizard will overwrite the row on success and any
// permanent DB problem will be surfaced at /readyz.
func (s *Server) setupComplete() bool {
	v, _ := (store.Settings{DB: s.db}).GetOrDefault(context.Background(), models.SettingSetupComplete, "0")
	return v == "1"
}
