// Package server wires the HTTP server, middleware chain, handlers, and
// graceful shutdown. main.go owns the lifecycle; this package owns the wiring.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/predsun/dashboard/internal/auth"
	"github.com/predsun/dashboard/internal/config"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/server/handlers"
	tlswire "github.com/predsun/dashboard/internal/tls"
	"github.com/predsun/dashboard/web"
)

// Server bundles the dependencies handlers need at runtime.
type Server struct {
	cfg      config.Config
	db       *sql.DB
	logger   *slog.Logger
	sessions *auth.SessionManager
	limiter  *auth.LoginLimiter

	health     handlers.Health
	setup      handlers.Setup
	auth       handlers.Auth
	apps       handlers.Apps
	categories handlers.Categories
	uploads    handlers.Uploads
	ie         handlers.ImportExport
	dashboard  handlers.Dashboard

	httpSrv *http.Server
	acmeSrv *http.Server // optional ACME HTTP-01 challenge listener on :80
}

// New builds a Server but does not bind any sockets. Call ListenAndServe to
// start serving.
func New(cfg config.Config, db *sql.DB, logger *slog.Logger) (*Server, error) {
	// Ensure persistent secrets exist (no-op on subsequent boots).
	if _, _, err := auth.EnsurePersistentKeys(context.Background(), db); err != nil {
		return nil, fmt.Errorf("ensure keys: %w", err)
	}

	renderer, err := NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("renderer: %w", err)
	}

	s := &Server{
		cfg:      cfg,
		db:       db,
		logger:   logger,
		sessions: auth.NewSessionManager(db, cfg.TLS.Enabled),
		limiter:  auth.NewLoginLimiter(5, 15*time.Minute),
		health:   handlers.Health{DB: db},
	}

	sessionFn := func(r *http.Request) *models.Session { return Session(r.Context()) }
	ipFn := func(r *http.Request) string { return ClientIP(r.Context()) }

	s.setup = handlers.Setup{
		DB:           db,
		Render:       renderer,
		IssueSession: s.sessions.Issue,
	}
	s.auth = handlers.Auth{
		DB:            db,
		Render:        renderer,
		IssueSession:  s.sessions.Issue,
		RevokeSession: s.sessions.Revoke,
		SessionFrom:   sessionFn,
		ClientIPFrom:  ipFn,
		Limiter:       s.limiter,
		Logger:        logger.With("component", "auth"),
	}
	s.apps = handlers.Apps{
		DB:     db,
		Logger: logger.With("component", "apps"),
	}
	s.categories = handlers.Categories{
		DB:     db,
		Logger: logger.With("component", "categories"),
	}
	s.uploads = handlers.Uploads{
		DB:             db,
		Logger:         logger.With("component", "uploads"),
		IconsDir:       cfg.IconsDir(),
		BackgroundsDir: cfg.BackgroundsDir(),
		MaxIconBytes:   cfg.MaxIconBytes,
		MaxBgBytes:     cfg.MaxBackgroundBytes,
	}
	s.ie = handlers.ImportExport{
		DB:     db,
		Logger: logger.With("component", "import-export"),
	}
	s.dashboard = handlers.Dashboard{
		DB:     db,
		Render: renderer,
		Logger: logger.With("component", "dashboard"),
	}

	// Sub-FS for /static/ so we don't expose the embed root directly.
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, fmt.Errorf("static subfs: %w", err)
	}

	handler := s.routes(staticFS)

	s.httpSrv = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// TLS wiring (optional). Errors here surface to main so misconfigured
	// production deploys fail loudly at startup instead of silently serving HTTP.
	tlsCfg, acmeHandler, err := tlswire.Configure(cfg.TLS, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		s.httpSrv.TLSConfig = tlsCfg
	}
	if acmeHandler != nil {
		s.acmeSrv = &http.Server{
			Addr:              ":80",
			Handler:           acmeHandler,
			ReadHeaderTimeout: 10 * time.Second,
		}
	}

	return s, nil
}

// ListenAndServe binds the configured socket(s) and serves. Returns
// http.ErrServerClosed on graceful shutdown.
func (s *Server) ListenAndServe() error {
	if s.acmeSrv != nil {
		go func() {
			if err := s.acmeSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.logger.Error("acme http server error", "err", err)
			}
		}()
	}
	if s.cfg.TLS.Enabled {
		return s.httpSrv.ListenAndServeTLS("", "")
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops both servers.
func (s *Server) Shutdown(ctx context.Context) error {
	var firstErr error
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		firstErr = err
	}
	if s.acmeSrv != nil {
		if err := s.acmeSrv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
