// Package config loads the dashboard configuration from (lowest precedence to
// highest) built-in defaults, a TOML file, DASHBOARD_* environment variables,
// and command-line flags. The precedence is implemented carefully: explicit
// flags always win, but a flag left at its default value loses to env/file.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ListenAddr      string        `toml:"listen_addr"`
	BaseURL         string        `toml:"base_url"`
	DataDir         string        `toml:"data_dir"`
	LogLevel        string        `toml:"log_level"`
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`

	// TrustedProxies is the parsed form of the CIDR list. The TOML key
	// `trusted_proxies` is a []string; we parse it once at Load time.
	TrustedProxiesRaw []string     `toml:"trusted_proxies"`
	TrustedProxies    []*net.IPNet `toml:"-"`

	MaxIconBytes       int64 `toml:"max_icon_bytes"`
	MaxBackgroundBytes int64 `toml:"max_background_bytes"`

	Health HealthConfig `toml:"health"`
	TLS    TLSConfig    `toml:"tls"`

	// TLSEnabled is a convenience alias for TLS.Enabled, used by main for log lines.
	TLSEnabled bool `toml:"-"`

	// ConfigPath is the resolved on-disk path that was loaded (empty if no file).
	ConfigPath string `toml:"-"`
}

type HealthConfig struct {
	Interval   time.Duration `toml:"interval"`
	Timeout    time.Duration `toml:"timeout"`
	MaxBackoff time.Duration `toml:"max_backoff"`
}

type TLSConfig struct {
	Enabled    bool     `toml:"enabled"`
	Mode       string   `toml:"mode"` // "autocert" | "file"
	ACMEEmail  string   `toml:"acme_email"`
	ACMEHosts  []string `toml:"acme_hosts"`
	CertFile   string   `toml:"cert_file"`
	KeyFile    string   `toml:"key_file"`
	ACMECache  string   `toml:"acme_cache"`
}

// DBPath returns the SQLite file path under DataDir.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "dashboard.db") }

// IconsDir returns the per-data-dir uploads location for icons.
func (c Config) IconsDir() string { return filepath.Join(c.DataDir, "uploads", "icons") }

// BackgroundsDir returns the uploads location for background images.
func (c Config) BackgroundsDir() string { return filepath.Join(c.DataDir, "uploads", "backgrounds") }

// Defaults returns the built-in config used when nothing else is set.
func Defaults() Config {
	return Config{
		ListenAddr:         ":8080",
		LogLevel:           "info",
		ShutdownTimeout:    10 * time.Second,
		MaxIconBytes:       2 << 20,  // 2 MiB
		MaxBackgroundBytes: 10 << 20, // 10 MiB
		Health: HealthConfig{
			Interval:   60 * time.Second,
			Timeout:    5 * time.Second,
			MaxBackoff: 30 * time.Minute,
		},
		TLS: TLSConfig{Mode: "autocert"},
	}
}

// LookupEnv matches os.LookupEnv. Pulled out as a parameter so tests can
// supply a stub without touching real env vars.
type LookupEnv func(key string) (value string, ok bool)

// Load applies precedence: defaults → config file → env → flags. The flagset
// must be the *flag.FlagSet to mutate; pass flag.CommandLine in main.
//
// We register flags on the flagset here so callers don't have to keep the
// flag definition in sync with the env keys. The flagset must not have been
// parsed yet by the caller.
func Load(fs *flag.FlagSet, args []string, env LookupEnv) (Config, error) {
	cfg := Defaults()

	// Stage A — register flags. Initial flag defaults equal cfg's defaults so
	// that an unset flag has the same value as a default-only config.
	var (
		fConfig          = fs.String("config", "", "path to config.toml (default: $DATA_DIR/config.toml)")
		fListen          = fs.String("listen", cfg.ListenAddr, "listen address, e.g. :8080")
		fBaseURL         = fs.String("base-url", cfg.BaseURL, "public URL when behind a reverse proxy")
		fDataDir         = fs.String("data-dir", "", "data directory (default: $XDG_DATA_HOME/dashboard)")
		fLogLevel        = fs.String("log-level", cfg.LogLevel, "debug | info | warn | error")
		fShutdown        = fs.Duration("shutdown-timeout", cfg.ShutdownTimeout, "graceful shutdown drain timeout")
		fTrustedProxies  = fs.String("trusted-proxies", "", "comma-separated CIDRs whose X-Forwarded-* headers are trusted")
		fMaxIcon         = fs.Int64("max-icon-bytes", cfg.MaxIconBytes, "max icon upload size in bytes")
		fMaxBg           = fs.Int64("max-background-bytes", cfg.MaxBackgroundBytes, "max background upload size in bytes")
		fHealthInterval  = fs.Duration("health-interval", cfg.Health.Interval, "default health-check interval")
		fHealthTimeout   = fs.Duration("health-timeout", cfg.Health.Timeout, "health-check HTTP timeout")
		fHealthMax       = fs.Duration("health-max-backoff", cfg.Health.MaxBackoff, "health-check max backoff between probes")
		fTLSEnabled      = fs.Bool("tls", cfg.TLS.Enabled, "enable built-in TLS termination")
		fTLSMode         = fs.String("tls-mode", cfg.TLS.Mode, "autocert | file")
		fACMEEmail       = fs.String("acme-email", cfg.TLS.ACMEEmail, "Let's Encrypt registration email")
		fACMEHosts       = fs.String("acme-hosts", "", "comma-separated hostnames to obtain ACME certs for")
		fCertFile        = fs.String("cert-file", cfg.TLS.CertFile, "TLS cert file (when --tls-mode=file)")
		fKeyFile         = fs.String("key-file", cfg.TLS.KeyFile, "TLS key file (when --tls-mode=file)")
	)
	_ = fConfig // referenced below

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	// Record which flags were explicitly set so env/file don't clobber them.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	// Stage B — resolve data dir (independent of file/env precedence because
	// it's also where we look for the config file).
	dataDir := resolveDataDir(explicit, *fDataDir, env)
	cfg.DataDir = dataDir

	// Stage C — load config file (path from --config, else $DATA_DIR/config.toml).
	cfgPath := *fConfig
	if cfgPath == "" {
		candidate := filepath.Join(dataDir, "config.toml")
		if _, err := os.Stat(candidate); err == nil {
			cfgPath = candidate
		}
	}
	if cfgPath != "" {
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			return cfg, fmt.Errorf("loading %s: %w", cfgPath, err)
		}
		cfg.ConfigPath = cfgPath
		// File may have re-set DataDir; honor it unless an explicit flag won.
		if !explicit["data-dir"] {
			if v, ok := env("DASHBOARD_DATA_DIR"); ok {
				cfg.DataDir = v
			} else if cfg.DataDir == "" {
				cfg.DataDir = dataDir
			}
		} else {
			cfg.DataDir = *fDataDir
		}
	}

	// Stage D — apply env vars (lose to explicit flags).
	applyEnv(&cfg, env, explicit)

	// Stage E — apply explicit flags (always win).
	if explicit["listen"] {
		cfg.ListenAddr = *fListen
	}
	if explicit["base-url"] {
		cfg.BaseURL = *fBaseURL
	}
	if explicit["data-dir"] {
		cfg.DataDir = *fDataDir
	}
	if explicit["log-level"] {
		cfg.LogLevel = *fLogLevel
	}
	if explicit["shutdown-timeout"] {
		cfg.ShutdownTimeout = *fShutdown
	}
	if explicit["trusted-proxies"] {
		cfg.TrustedProxiesRaw = splitCSV(*fTrustedProxies)
	}
	if explicit["max-icon-bytes"] {
		cfg.MaxIconBytes = *fMaxIcon
	}
	if explicit["max-background-bytes"] {
		cfg.MaxBackgroundBytes = *fMaxBg
	}
	if explicit["health-interval"] {
		cfg.Health.Interval = *fHealthInterval
	}
	if explicit["health-timeout"] {
		cfg.Health.Timeout = *fHealthTimeout
	}
	if explicit["health-max-backoff"] {
		cfg.Health.MaxBackoff = *fHealthMax
	}
	if explicit["tls"] {
		cfg.TLS.Enabled = *fTLSEnabled
	}
	if explicit["tls-mode"] {
		cfg.TLS.Mode = *fTLSMode
	}
	if explicit["acme-email"] {
		cfg.TLS.ACMEEmail = *fACMEEmail
	}
	if explicit["acme-hosts"] {
		cfg.TLS.ACMEHosts = splitCSV(*fACMEHosts)
	}
	if explicit["cert-file"] {
		cfg.TLS.CertFile = *fCertFile
	}
	if explicit["key-file"] {
		cfg.TLS.KeyFile = *fKeyFile
	}

	cfg.TLSEnabled = cfg.TLS.Enabled

	// Parse CIDRs once so handlers can compare RemoteAddr to net.IPNet directly.
	parsed, err := parseCIDRs(cfg.TrustedProxiesRaw)
	if err != nil {
		return cfg, fmt.Errorf("trusted_proxies: %w", err)
	}
	cfg.TrustedProxies = parsed

	if err := validate(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config, env LookupEnv, explicit map[string]bool) {
	setStr := func(key, flagName string, dst *string) {
		if explicit[flagName] {
			return
		}
		if v, ok := env(key); ok {
			*dst = v
		}
	}
	setInt64 := func(key, flagName string, dst *int64) {
		if explicit[flagName] {
			return
		}
		if v, ok := env(key); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				*dst = n
			}
		}
	}
	setDur := func(key, flagName string, dst *time.Duration) {
		if explicit[flagName] {
			return
		}
		if v, ok := env(key); ok {
			if d, err := time.ParseDuration(v); err == nil {
				*dst = d
			}
		}
	}
	setBool := func(key, flagName string, dst *bool) {
		if explicit[flagName] {
			return
		}
		if v, ok := env(key); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}

	setStr("DASHBOARD_LISTEN_ADDR", "listen", &cfg.ListenAddr)
	setStr("DASHBOARD_BASE_URL", "base-url", &cfg.BaseURL)
	setStr("DASHBOARD_DATA_DIR", "data-dir", &cfg.DataDir)
	setStr("DASHBOARD_LOG_LEVEL", "log-level", &cfg.LogLevel)
	setDur("DASHBOARD_SHUTDOWN_TIMEOUT", "shutdown-timeout", &cfg.ShutdownTimeout)
	if !explicit["trusted-proxies"] {
		if v, ok := env("DASHBOARD_TRUSTED_PROXIES"); ok {
			cfg.TrustedProxiesRaw = splitCSV(v)
		}
	}
	setInt64("DASHBOARD_MAX_ICON_BYTES", "max-icon-bytes", &cfg.MaxIconBytes)
	setInt64("DASHBOARD_MAX_BACKGROUND_BYTES", "max-background-bytes", &cfg.MaxBackgroundBytes)
	setDur("DASHBOARD_HEALTH_INTERVAL", "health-interval", &cfg.Health.Interval)
	setDur("DASHBOARD_HEALTH_TIMEOUT", "health-timeout", &cfg.Health.Timeout)
	setDur("DASHBOARD_HEALTH_MAX_BACKOFF", "health-max-backoff", &cfg.Health.MaxBackoff)
	setBool("DASHBOARD_TLS", "tls", &cfg.TLS.Enabled)
	setStr("DASHBOARD_TLS_MODE", "tls-mode", &cfg.TLS.Mode)
	setStr("DASHBOARD_ACME_EMAIL", "acme-email", &cfg.TLS.ACMEEmail)
	if !explicit["acme-hosts"] {
		if v, ok := env("DASHBOARD_ACME_HOSTS"); ok {
			cfg.TLS.ACMEHosts = splitCSV(v)
		}
	}
	setStr("DASHBOARD_CERT_FILE", "cert-file", &cfg.TLS.CertFile)
	setStr("DASHBOARD_KEY_FILE", "key-file", &cfg.TLS.KeyFile)
}

func resolveDataDir(explicit map[string]bool, flagVal string, env LookupEnv) string {
	if explicit["data-dir"] && flagVal != "" {
		return flagVal
	}
	if v, ok := env("DASHBOARD_DATA_DIR"); ok && v != "" {
		return v
	}
	if v, ok := env("XDG_DATA_HOME"); ok && v != "" {
		return filepath.Join(v, "dashboard")
	}
	if home, ok := env("HOME"); ok && home != "" {
		return filepath.Join(home, ".local", "share", "dashboard")
	}
	// Windows fallback for `make dev` on the author's machine.
	if runtime.GOOS == "windows" {
		if v, ok := env("APPDATA"); ok && v != "" {
			return filepath.Join(v, "dashboard")
		}
	}
	return "./data"
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseCIDRs(in []string) ([]*net.IPNet, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*net.IPNet, 0, len(in))
	for _, c := range in {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", c, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func validate(cfg *Config) error {
	if cfg.ListenAddr == "" {
		return errors.New("listen_addr is empty")
	}
	if cfg.DataDir == "" {
		return errors.New("data_dir is empty")
	}
	if cfg.ShutdownTimeout <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	if cfg.MaxIconBytes <= 0 || cfg.MaxBackgroundBytes <= 0 {
		return errors.New("upload size limits must be positive")
	}
	if cfg.Health.Interval <= 0 || cfg.Health.Timeout <= 0 || cfg.Health.MaxBackoff <= 0 {
		return errors.New("health durations must be positive")
	}
	if cfg.TLS.Enabled {
		switch cfg.TLS.Mode {
		case "autocert":
			if len(cfg.TLS.ACMEHosts) == 0 {
				return errors.New("tls.acme_hosts is required when tls.mode=autocert")
			}
		case "file":
			if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
				return errors.New("tls.cert_file and tls.key_file are required when tls.mode=file")
			}
		default:
			return fmt.Errorf("tls.mode must be autocert or file, got %q", cfg.TLS.Mode)
		}
	}
	return nil
}
