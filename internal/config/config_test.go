package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func emptyEnv(string) (string, bool) { return "", false }

func envFromMap(m map[string]string) LookupEnv {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func newFS() *flag.FlagSet {
	return flag.NewFlagSet("test", flag.ContinueOnError)
}

func TestDefaultsApply(t *testing.T) {
	cfg, err := Load(newFS(), nil, emptyEnv)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("listen default: got %q", cfg.ListenAddr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("log_level default: got %q", cfg.LogLevel)
	}
	if cfg.MaxIconBytes != 2<<20 {
		t.Errorf("icon bytes default: got %d", cfg.MaxIconBytes)
	}
	if cfg.Health.Interval != 60*time.Second {
		t.Errorf("health interval default: got %v", cfg.Health.Interval)
	}
	if cfg.DataDir == "" {
		t.Error("data_dir should fall back to a non-empty path")
	}
}

func TestEnvOverridesDefaults(t *testing.T) {
	env := envFromMap(map[string]string{
		"DASHBOARD_LISTEN_ADDR": ":9999",
		"DASHBOARD_LOG_LEVEL":   "debug",
		"DASHBOARD_DATA_DIR":    "/tmp/dash-env",
	})
	cfg, err := Load(newFS(), nil, env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("env listen: got %q", cfg.ListenAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("env log_level: got %q", cfg.LogLevel)
	}
	if cfg.DataDir != "/tmp/dash-env" {
		t.Errorf("env data_dir: got %q", cfg.DataDir)
	}
}

func TestFlagBeatsEnv(t *testing.T) {
	env := envFromMap(map[string]string{
		"DASHBOARD_LISTEN_ADDR": ":9999",
	})
	cfg, err := Load(newFS(), []string{"--listen", ":7777"}, env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":7777" {
		t.Errorf("flag should beat env: got %q", cfg.ListenAddr)
	}
}

func TestFlagDefaultDoesNotBeatEnv(t *testing.T) {
	// Regression: if a flag is *registered* with a default that happens to
	// match the built-in default, the env value should still apply because
	// the flag was not explicitly set.
	env := envFromMap(map[string]string{
		"DASHBOARD_LOG_LEVEL": "warn",
	})
	cfg, err := Load(newFS(), nil, env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("env should beat unset flag: got %q", cfg.LogLevel)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
listen_addr = ":5555"
log_level   = "debug"
max_icon_bytes = 4096
[health]
interval = "10s"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(newFS(), []string{"--config", path, "--data-dir", dir}, emptyEnv)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":5555" {
		t.Errorf("file listen: got %q", cfg.ListenAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("file log_level: got %q", cfg.LogLevel)
	}
	if cfg.MaxIconBytes != 4096 {
		t.Errorf("file icon bytes: got %d", cfg.MaxIconBytes)
	}
	if cfg.Health.Interval != 10*time.Second {
		t.Errorf("file health.interval: got %v", cfg.Health.Interval)
	}
}

func TestEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`listen_addr = ":5555"`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := envFromMap(map[string]string{"DASHBOARD_LISTEN_ADDR": ":6666"})
	cfg, err := Load(newFS(), []string{"--config", path, "--data-dir", dir}, env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":6666" {
		t.Errorf("env should beat file: got %q", cfg.ListenAddr)
	}
}

func TestFlagBeatsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`listen_addr = ":5555"`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(newFS(), []string{"--config", path, "--data-dir", dir, "--listen", ":4444"}, emptyEnv)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddr != ":4444" {
		t.Errorf("flag should beat file: got %q", cfg.ListenAddr)
	}
}

func TestTrustedProxiesParsed(t *testing.T) {
	cfg, err := Load(newFS(), []string{"--trusted-proxies", "10.0.0.0/8, 172.16.0.0/12"}, emptyEnv)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("expected 2 CIDRs, got %d", len(cfg.TrustedProxies))
	}
	if cfg.TrustedProxies[0].String() != "10.0.0.0/8" {
		t.Errorf("first CIDR: got %s", cfg.TrustedProxies[0])
	}
}

func TestInvalidCIDRRejected(t *testing.T) {
	_, err := Load(newFS(), []string{"--trusted-proxies", "not-a-cidr"}, emptyEnv)
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestTLSValidation(t *testing.T) {
	// autocert without hosts is invalid.
	_, err := Load(newFS(), []string{"--tls", "--tls-mode", "autocert"}, emptyEnv)
	if err == nil {
		t.Fatal("expected error when autocert has no hosts")
	}

	// file mode without cert/key paths is invalid.
	_, err = Load(newFS(), []string{"--tls", "--tls-mode", "file"}, emptyEnv)
	if err == nil {
		t.Fatal("expected error when file mode lacks cert/key")
	}

	// autocert with hosts is valid.
	cfg, err := Load(newFS(), []string{"--tls", "--tls-mode", "autocert", "--acme-hosts", "example.com"}, emptyEnv)
	if err != nil {
		t.Fatalf("autocert with hosts should be valid: %v", err)
	}
	if !cfg.TLSEnabled || len(cfg.TLS.ACMEHosts) != 1 {
		t.Errorf("unexpected TLS state: %+v", cfg.TLS)
	}
}
