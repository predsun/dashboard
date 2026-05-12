# Installing dashboard

dashboard ships as a single static Linux binary. The recommended path is:

1. Run the one-line installer (verifies a cosign-signed SHA256 before installing)
2. Terminate TLS with Caddy or nginx (or use the built-in autocert mode)
3. Visit `/setup` to create your admin account

No Docker. No PHP. No Node.js at runtime.

---

## Quick install

```sh
curl -fsSL https://github.com/predsun/dashboard/releases/latest/download/install.sh | sudo bash
```

The script:

1. Detects your CPU architecture (amd64 / arm64); aborts on anything else.
2. Resolves `latest` to a specific tag via the GitHub API.
3. Downloads the binary, `SHA256SUMS`, and `SHA256SUMS.bundle`.
4. **Verifies the cosign signature on `SHA256SUMS`** (keyless, against the project's GitHub Actions OIDC identity). Aborts if `cosign` isn't installed 鈥?pass `--skip-cosign` only if you've accepted the risk.
5. **Verifies the binary's SHA256 against `SHA256SUMS`**. Aborts on mismatch.
6. Creates a `dashboard` system user (`useradd --system --no-create-home --shell /usr/sbin/nologin`) if absent.
7. Installs the binary to `/usr/local/bin/dashboard` (root-owned, `0755`).
8. Creates `/var/lib/dashboard` (`0750`, `dashboard:dashboard`) if absent 鈥?preserves ownership on re-runs.
9. Installs `/etc/systemd/system/dashboard.service`, runs `daemon-reload`, enables the unit.
10. Restarts the service if already running.

### Install a specific version

```sh
sudo bash install.sh --version v1.2.0
```

### Use non-default paths

```sh
sudo bash install.sh --prefix /opt/bin --data-dir /srv/dashboard
```

The systemd unit's `ReadWritePaths=` is rewritten to match.

### Install cosign (recommended)

```sh
# Ubuntu / Debian
sudo apt-get install -y cosign
# Or download the binary:
curl -fsSL -o /usr/local/bin/cosign https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-amd64
sudo chmod +x /usr/local/bin/cosign
```

---

## First-run setup

```sh
sudo systemctl start dashboard
sudo systemctl status dashboard
```

If status is green, visit `http://<your-host>:8080/setup` and create the admin account. Until that's done, every other URL redirects to `/setup`.

The default port is `8080`. To change it, edit `/etc/dashboard.env` (root-owned, `0600`):

```sh
DASHBOARD_LISTEN_ADDR=:9090
```

Then `systemctl restart dashboard`.

---

## Putting TLS in front

You almost certainly want a reverse proxy. The binary listens on plain HTTP by default; the reverse proxy terminates TLS and forwards.

### Caddy (recommended 鈥?handles certs automatically)

Drop `deploy/Caddyfile.example` at `/etc/caddy/Caddyfile`, replace the hostname, and:

```sh
sudo systemctl reload caddy
```

Caddy will obtain a Let's Encrypt cert on first request.

### nginx

Use `deploy/nginx.conf.example` as a starting point. You'll need to issue certs yourself (certbot, acme.sh).

### Trusted proxies

When behind a proxy, set `DASHBOARD_TRUSTED_PROXIES=127.0.0.1/32` (or whichever CIDR your proxy lives in) in `/etc/dashboard.env`. The default is empty, which means `X-Forwarded-*` headers are ignored entirely.

### Built-in TLS (no proxy)

If you don't want a reverse proxy:

```sh
# /etc/dashboard.env
DASHBOARD_TLS=true
DASHBOARD_TLS_MODE=autocert
DASHBOARD_ACME_EMAIL=you@example.com
DASHBOARD_ACME_HOSTS=apps.example.com
DASHBOARD_LISTEN_ADDR=:443
```

You'll also need to grant the binary permission to bind to ports below 1024. The systemd unit ships with `CapabilityBoundingSet=` empty 鈥?edit `/etc/systemd/system/dashboard.service` and add:

```ini
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
```

Then `systemctl daemon-reload && systemctl restart dashboard`. The unit must also listen on `:80` for the ACME HTTP-01 challenge (the binary spawns that listener automatically).

---

## Hardened systemd unit

The unit ships with most of the sandbox flags systemd offers. Run `systemd-analyze security dashboard.service` to see the score; it should land in the "OK" (鈮?2.0) band.

```
ProtectSystem=strict           # everything read-only except ReadWritePaths
ProtectHome=true               # /home, /root, /run/user invisible
PrivateTmp=true                # private /tmp namespace
PrivateDevices=true            # no access to /dev
NoNewPrivileges=true           # setuid binaries can't elevate
CapabilityBoundingSet=         # all caps dropped (or CAP_NET_BIND_SERVICE)
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true    # blocks JIT-style attacks
SystemCallArchitectures=native
SystemCallFilter=@system-service
ProtectKernelTunables/Modules/Logs/ControlGroups/Clock/Hostname=true
ProtectProc=invisible          # other processes hidden
```

If something breaks after edits, `journalctl -u dashboard | grep -i denied` usually points at the offender.

---

## Backup

Two ways. Use both for belt-and-suspenders.

### 1. JSON export (apps, categories, settings)

While the service is running, sign in and visit `/api/export` (or hit the export button in the UI). The downloaded JSON excludes secrets (session/CSRF keys) by default. Add `?include_secrets=1` for a self-contained restore that won't invalidate existing sessions.

Restore with `POST /api/import` (uploads the JSON via the UI). Imports add to the current dashboard rather than replacing it 鈥?categories merge by name, apps land at the end of their respective groups.

### 2. SQLite file backup

The full state lives in `/var/lib/dashboard/dashboard.db` plus the `uploads/` subdirectory. Snapshot the whole `dashboard` data dir:

```sh
sudo systemctl stop dashboard
sudo tar -czf dashboard-$(date +%Y%m%d).tar.gz -C /var/lib dashboard
sudo systemctl start dashboard
```

A stopped service guarantees the SQLite WAL is checkpointed. If you can't stop the service, run the official online backup instead:

```sh
sudo -u dashboard sqlite3 /var/lib/dashboard/dashboard.db ".backup '/tmp/dashboard-$(date +%Y%m%d).db'"
```

Restore: replace `/var/lib/dashboard` with the unpacked archive and start the service.

---

## Upgrade

Re-run the installer. The script is idempotent.

```sh
curl -fsSL https://github.com/predsun/dashboard/releases/latest/download/install.sh | sudo bash
```

Or with a pinned version:

```sh
sudo bash install.sh --version v1.3.0
```

Migrations run on the next start. The unit is restarted automatically if it was already running.

Rollback: re-run with the previous tag.

---

## Uninstall

```sh
sudo systemctl stop dashboard
sudo systemctl disable dashboard
sudo rm /etc/systemd/system/dashboard.service
sudo rm /usr/local/bin/dashboard
sudo systemctl daemon-reload

# Optional 鈥?irreversible:
sudo rm -rf /var/lib/dashboard
sudo userdel dashboard
```

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `systemctl status` shows `Permission denied` writing to data dir | `/var/lib/dashboard` is owned by something other than `dashboard:dashboard`. Fix with `chown -R dashboard:dashboard /var/lib/dashboard`. |
| Browser hangs on first `/setup` request | The service hasn't started, or you're behind a proxy that doesn't pass `X-Forwarded-Proto`. Check `journalctl -u dashboard -n 50`. |
| Icon upload returns 415 | The uploaded file isn't a supported image type. MIME is detected from content, not the filename. |
| All requests redirect to `/setup` | Expected when no admin exists. Visit `/setup` directly. |
| `cosign verify-blob` fails on install | The `--certificate-identity-regexp` and OIDC issuer must match the release workflow. Open an issue if you suspect a real mismatch; do not bypass with `--skip-cosign`. |
