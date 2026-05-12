// Command dashboard boots the self-hosted dashboard server: loads config,
// opens the SQLite database, runs migrations, starts the background health
// worker, and serves HTTP(S) until SIGTERM/SIGINT.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/predsun/dashboard/internal/config"
	"github.com/predsun/dashboard/internal/db"
	"github.com/predsun/dashboard/internal/health"
	"github.com/predsun/dashboard/internal/server"
	"github.com/predsun/dashboard/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// `--migrate-only` and `--version` are short-circuit flags. They use the
	// same flagset as the rest of config so users only see one --help screen.
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	migrateOnly := fs.Bool("migrate-only", false, "run database migrations and exit")
	showVersion := fs.Bool("version", false, "print version and exit")

	cfg, err := config.Load(fs, os.Args[1:], os.LookupEnv)
	if err != nil {
		// flag.Parse already printed usage on ErrHelp; don't double-error.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("loading config: %w", err)
	}

	if *showVersion {
		fmt.Printf("dashboard %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return nil
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info("starting",
		"version", version.Version,
		"commit", version.Commit,
		"data_dir", cfg.DataDir,
		"config", cfg.ConfigPath,
	)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}
	for _, sub := range []string{"uploads/icons", "uploads/backgrounds"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, sub), 0o750); err != nil {
			return fmt.Errorf("creating %s: %w", sub, err)
		}
	}

	conn, err := db.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer conn.Close()

	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	if *migrateOnly {
		logger.Info("migrations complete, --migrate-only set, exiting")
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	worker := health.NewWorker(conn, logger.With("component", "health"))
	worker.Configure(cfg.Health)
	go worker.Run(ctx)

	srv, err := server.New(cfg, conn, logger)
	if err != nil {
		return fmt.Errorf("building server: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "tls", cfg.TLSEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "err", err)
	}
	logger.Info("bye")
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
