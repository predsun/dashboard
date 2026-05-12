#!/usr/bin/env bash
#
# dashboard installer. Re-runnable: invoke again with --version vX.Y.Z to
# upgrade. Aborts on checksum mismatch — never installs an unverified binary.
#
# Usage:
#   curl -fsSL https://github.com/predsun/dashboard/releases/latest/download/install.sh | sudo bash
#   sudo bash install.sh --version v1.2.0
#   sudo bash install.sh --prefix /opt/bin --data-dir /srv/dashboard
#   sudo bash install.sh --skip-cosign     # not recommended
#
# Requires: bash, curl, install, useradd, sha256sum. cosign optional.

set -euo pipefail

REPO="${DASHBOARD_REPO:-predsun/dashboard}"
VERSION="${DASHBOARD_VERSION:-latest}"
PREFIX="${DASHBOARD_PREFIX:-/usr/local/bin}"
DATA_DIR="${DASHBOARD_DATA_DIR:-/var/lib/dashboard}"
SYSTEMD_DIR="${DASHBOARD_SYSTEMD_DIR:-/etc/systemd/system}"
USER_NAME="dashboard"
SKIP_COSIGN=0

err() { echo "error: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# --- Argument parsing -------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="${2:?--version requires a value}"; shift 2 ;;
    --version=*)   VERSION="${1#*=}"; shift ;;
    --prefix)      PREFIX="${2:?--prefix requires a value}"; shift 2 ;;
    --prefix=*)    PREFIX="${1#*=}"; shift ;;
    --data-dir)    DATA_DIR="${2:?--data-dir requires a value}"; shift 2 ;;
    --data-dir=*)  DATA_DIR="${1#*=}"; shift ;;
    --skip-cosign) SKIP_COSIGN=1; shift ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
    *) err "unknown argument: $1" ;;
  esac
done

# --- Preflight --------------------------------------------------------------
[[ $EUID -eq 0 ]] || err "must be run as root (try: sudo bash $0)"

for cmd in curl sha256sum install useradd systemctl; do
  command -v "$cmd" >/dev/null 2>&1 || err "missing required command: $cmd"
done

# --- Architecture detection -------------------------------------------------
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) err "unsupported architecture: $ARCH_RAW" ;;
esac
OS="linux"

# --- Resolve version --------------------------------------------------------
if [[ "$VERSION" == "latest" ]]; then
  info "resolving latest release for ${REPO}"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [[ -n "$VERSION" ]] || err "could not resolve latest release"
fi
info "installing dashboard ${VERSION} (${OS}/${ARCH})"

# --- Download into a temp dir we always clean up ----------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

BINARY="dashboard-${OS}-${ARCH}"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

info "downloading ${BINARY}"
curl -fsSL -o "$TMP/$BINARY" "${BASE_URL}/${BINARY}"

info "downloading SHA256SUMS"
curl -fsSL -o "$TMP/SHA256SUMS" "${BASE_URL}/SHA256SUMS"

if [[ $SKIP_COSIGN -eq 0 ]]; then
  if command -v cosign >/dev/null 2>&1; then
    info "downloading cosign signature"
    curl -fsSL -o "$TMP/SHA256SUMS.sig" "${BASE_URL}/SHA256SUMS.sig"
    # Keyless verification: trusts the GitHub Actions OIDC identity that
    # signed the release. Identity values match release.yml.
    info "verifying cosign signature (keyless)"
    cosign verify-blob \
      --certificate-identity-regexp "^https://github.com/${REPO}/" \
      --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
      --signature "$TMP/SHA256SUMS.sig" \
      "$TMP/SHA256SUMS" \
      || err "cosign signature verification failed"
  else
    echo "warning: cosign not installed — signature not verified." >&2
    echo "         install cosign or rerun with --skip-cosign to acknowledge." >&2
    err "aborting; install cosign or pass --skip-cosign"
  fi
else
  echo "warning: --skip-cosign passed; signature not verified" >&2
fi

# --- SHA-256 verification (always, even with --skip-cosign) -----------------
info "verifying SHA256"
EXPECTED="$(grep " ${BINARY}\$" "$TMP/SHA256SUMS" | awk '{print $1}')"
[[ -n "$EXPECTED" ]] || err "no checksum entry for ${BINARY} in SHA256SUMS"

ACTUAL="$(sha256sum "$TMP/$BINARY" | awk '{print $1}')"
if [[ "$ACTUAL" != "$EXPECTED" ]]; then
  err "SHA256 mismatch! expected=${EXPECTED} actual=${ACTUAL}"
fi
info "SHA256 OK: ${ACTUAL}"

# --- System user ------------------------------------------------------------
if ! getent passwd "$USER_NAME" >/dev/null 2>&1; then
  info "creating system user '${USER_NAME}'"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
else
  info "system user '${USER_NAME}' already exists"
fi

# --- Data directory ---------------------------------------------------------
if [[ ! -d "$DATA_DIR" ]]; then
  info "creating data dir ${DATA_DIR}"
  install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$DATA_DIR"
else
  info "data dir ${DATA_DIR} already exists; leaving ownership alone"
fi

# --- Binary -----------------------------------------------------------------
DEST="${PREFIX}/dashboard"
info "installing binary to ${DEST}"
# If the service is already running, systemd holds the binary open; install
# atomically and bounce after.
install -m 0755 -o root -g root "$TMP/$BINARY" "$DEST"

# --- systemd unit -----------------------------------------------------------
UNIT_SRC_URL="${BASE_URL}/dashboard.service"
UNIT_DEST="${SYSTEMD_DIR}/dashboard.service"
info "installing systemd unit ${UNIT_DEST}"
curl -fsSL -o "$TMP/dashboard.service" "$UNIT_SRC_URL"

# Patch ReadWritePaths if the operator chose a non-default data dir.
if [[ "$DATA_DIR" != "/var/lib/dashboard" ]]; then
  sed -i "s|ReadWritePaths=/var/lib/dashboard|ReadWritePaths=${DATA_DIR}|" "$TMP/dashboard.service"
fi

install -m 0644 -o root -g root "$TMP/dashboard.service" "$UNIT_DEST"

info "reloading systemd"
systemctl daemon-reload

if systemctl is-enabled --quiet dashboard.service 2>/dev/null; then
  info "dashboard.service already enabled"
else
  info "enabling dashboard.service"
  systemctl enable dashboard.service
fi

if systemctl is-active --quiet dashboard.service 2>/dev/null; then
  info "restarting dashboard.service"
  systemctl restart dashboard.service
else
  info "service is not running yet — run: systemctl start dashboard"
fi

cat <<EOF

dashboard ${VERSION} installed.

Next steps:
  systemctl status dashboard          # check it's running
  journalctl -u dashboard -f          # tail logs
  ${DEST} --version                   # confirm binary version

Then visit http://<your-host>:8080/setup to create the admin account.

For HTTPS, terminate TLS with Caddy or nginx — examples are in the repo's
deploy/ directory.
EOF
