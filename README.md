# dashboard

A lightweight self-hosted dashboard for organizing the web apps running on your
personal VPS. Inspired by [Heimdall](https://heimdall.site), but shipped as a
**single static Go binary** — no Docker, no PHP, no Node.js at runtime.

`scp` one file, drop a systemd unit next to it, done.

## Features

- Grid of application tiles with icons, names, URLs, descriptions
- Add / edit / delete / reorder apps via drag-and-drop (order persisted)
- Custom icon uploads (PNG / SVG, MIME-sniffed) plus a bundled Simple Icons subset
- Categories and tags
- Fuzzy search across apps, focusable with `/`
- Per-app background health checks with exponential backoff
- Light / dark / auto theme (respects `prefers-color-scheme`)
- Custom background images or gradient presets
- Single-user auth: bcrypt + secure session cookies + CSRF + login rate limiting
- JSON import / export for backups
- First-run setup wizard
- Optional built-in Let's Encrypt via `autocert`, or plain HTTP behind a reverse proxy

## Footprint

- Binary: **under 25 MB**, statically linked (no CGO, no `libsqlite3`)
- RAM: under 50 MB idle
- Cold start: under 1 second
- Runtime dependencies: **none** (no system packages required beyond a base Debian/Ubuntu install)
- External calls: none (no telemetry, no CDN)

## Quickstart (local dev)

```sh
git clone https://github.com/predsun/dashboard
cd dashboard
make build           # downloads Tailwind CLI on first run, then compiles
./bin/dashboard      # listens on :8080, redirects to /setup on first run
```

Visit <http://localhost:8080>, create an admin user, and start adding apps.

## Production install (VPS)

See [INSTALL.md](INSTALL.md). Short version:

```sh
curl -fsSL https://github.com/predsun/dashboard/releases/latest/download/install.sh | sudo bash
```

The install script:

1. Downloads the architecture-correct binary
2. **Verifies SHA256 checksums against a cosign-signed `SHA256SUMS`** — aborts on mismatch
3. Creates a `dashboard` system user with no shell and no home
4. Installs `/var/lib/dashboard` with `0750` ownership
5. Drops a hardened systemd unit
6. Prints next steps

## Configuration

Three sources, last-write wins (highest first):

1. Command-line flags (`./dashboard --listen :9000`)
2. Environment variables (`DASHBOARD_LISTEN_ADDR=:9000`)
3. `config.toml` in the data directory (see [config.example.toml](config.example.toml))

Sensible defaults mean `./dashboard` with no arguments works on first run.

## Module path

This repository uses the placeholder module path `github.com/predsun/dashboard`.
Before forking, run a find-and-replace to your own path:

```sh
grep -rl 'predsun/dashboard' . | xargs sed -i 's|predsun/dashboard|your-org/dashboard|g'
```

## Make targets

| Target | What it does |
|---|---|
| `make build` | Compile Tailwind, then `go build` for the host platform |
| `make build-all` | Cross-compile for linux/amd64, linux/arm64, darwin/arm64, windows/amd64 |
| `make run` | Build CSS once and `go run` |
| `make dev` | Tailwind `--watch` alongside `go run` |
| `make test` | `go test -race ./...` |
| `make lint` | `go vet` and `staticcheck` (if installed) |
| `make release` | `build-all` plus `SHA256SUMS` in `dist/` |
| `make clean` | Remove build artifacts |

## License

MIT — see [LICENSE](LICENSE).
