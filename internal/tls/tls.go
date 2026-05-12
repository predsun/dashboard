// Package tls wires either built-in autocert (Let's Encrypt) or a fixed
// cert/key pair into the server. Plain HTTP is also supported and is the
// default — most users will run behind Caddy or nginx, which handle TLS.
package tls

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/predsun/dashboard/internal/config"

	"golang.org/x/crypto/acme/autocert"
)

// Configure returns a *tls.Config (or nil if TLS disabled) plus an optional
// HTTP handler that must be served on :80 for autocert HTTP-01 challenges.
// When the second return value is non-nil, the caller is responsible for
// running a second http.Server on :80.
func Configure(cfg config.TLSConfig, dataDir string) (*tls.Config, http.Handler, error) {
	if !cfg.Enabled {
		return nil, nil, nil
	}
	switch cfg.Mode {
	case "autocert":
		return configureAutocert(cfg, dataDir)
	case "file":
		return configureFile(cfg)
	default:
		return nil, nil, fmt.Errorf("unsupported tls.mode %q", cfg.Mode)
	}
}

func configureAutocert(cfg config.TLSConfig, dataDir string) (*tls.Config, http.Handler, error) {
	cacheDir := cfg.ACMECache
	if cacheDir == "" {
		cacheDir = filepath.Join(dataDir, "autocert")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating autocert cache: %w", err)
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.ACMEHosts...),
		Cache:      autocert.DirCache(cacheDir),
		Email:      cfg.ACMEEmail,
	}
	tlsCfg := m.TLSConfig()
	tlsCfg.MinVersion = tls.VersionTLS12
	return tlsCfg, m.HTTPHandler(nil), nil
}

func configureFile(cfg config.TLSConfig) (*tls.Config, http.Handler, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("loading tls cert/key: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil, nil
}
