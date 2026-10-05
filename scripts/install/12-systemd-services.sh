#!/bin/bash
# shellcheck disable=SC1090  # witsaba.env is generated at install time
# =============================================================================
# 12-systemd-services.sh - Create systemd *user* units for the witsaba stack
# =============================================================================
# User units + loginctl linger: the services start at boot and keep running
# after logout without any root-owned unit. Memory limits are set because the
# target is a 1GB Raspberry Pi and the kernel OOM killer picks the largest
# process by default, which is not necessarily the one at fault.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$HOME/.witsaba"

# Homebrew discovery
BREW_BIN=""
for candidate in \
    "/home/linuxbrew/.linuxbrew/bin/brew" \
    "$HOME/.linuxbrew/bin/brew" \
    "$HOME/.brew/bin/brew"; do
    [ -f "$candidate" ] && BREW_BIN="$candidate" && break
done
[ -z "$BREW_BIN" ] && { echo "[x] Homebrew not found. Run 00-brew.sh first." >&2; exit 1; }
HOMEBREW_PREFIX="$(dirname "$(dirname "$BREW_BIN")")"
export HOMEBREW_PREFIX
export PATH="$HOMEBREW_PREFIX/bin:$PATH"

# Shared helpers (logging, available_mem_mb). Sourced before anything uses them.
. "$SCRIPT_DIR/_lib.sh"

echo ""
echo "=============================================="
echo "  Step 12: systemd user units"
echo "=============================================="
echo ""

ENV_FILE="$INSTALL_DIR/witsaba.env"
[ -f "$ENV_FILE" ] || { log_err "$ENV_FILE missing. Run 01-postgresql.sh first."; exit 1; }

set -a; . "$ENV_FILE"; set +a

# The gallery thumbnail cache lives inside the capture root and is written by
# messaging-core, so that unit needs write access to the directory. Resolved
# here with the same default both Go services use, so the unit, the API and
# the capture writer cannot disagree about the path.
GALLERY_ROOT_DIR="${GALLERY_ROOT_DIR:-${SURVEILLANCE_ROOT_DIR:-$INSTALL_DIR/cameras}}"
mkdir -p "$GALLERY_ROOT_DIR"

PG_BIN_DIR="${PG_BIN_DIR:-$HOMEBREW_PREFIX/opt/postgresql@16/bin}"
PG_CTL="$PG_BIN_DIR/pg_ctl"

# nginx serves the static frontend and proxies /api and /stream. No node
# runtime is involved anywhere in this path.
NGINX_BIN="$HOMEBREW_PREFIX/bin/nginx"
NGINX_PREFIX="$INSTALL_DIR/nginx"
NGINX_CONF="$NGINX_PREFIX/nginx.conf"

if [ ! -x "$PG_CTL" ]; then
    log_err "pg_ctl not found at $PG_CTL. Run 01-postgresql.sh first."
    exit 1
fi
if [ ! -x "$NGINX_BIN" ]; then
    log_err "nginx not found at $NGINX_BIN. Run 13-nginx.sh first."
    exit 1
fi
if [ ! -f "$NGINX_CONF" ]; then
    log_err "$NGINX_CONF missing. Run 13-nginx.sh first."
    exit 1
fi
log_ok "nginx  : $NGINX_BIN ($("$NGINX_BIN" -v 2>&1 | sed 's|.*nginx/||;s/ .*//'))"
log_ok "pg_ctl : $PG_CTL"

if [ ! -x "$PG_CTL" ]; then
    log_err "pg_ctl is not executable: $PG_CTL"
    exit 1
fi

SYSTEMD_DIR="$HOME/.config/systemd/user"
mkdir -p "$SYSTEMD_DIR"

# -----------------------------------------------------------------------------
# Linger gate. Without it the user manager is not started at boot and nothing
# we enable here will ever run unattended.
# -----------------------------------------------------------------------------
if [ "$(loginctl show-user "$(id -u)" -p Linger --value 2>/dev/null)" != "yes" ]; then
    log_warn "linger is NOT enabled for $(id -un)"
    log_warn "run:  sudo loginctl enable-linger $(id -un)"
    log_warn "units will be written and enabled, but will not start at boot yet"
fi

# -----------------------------------------------------------------------------
# 1. PostgreSQL
#
# Type=oneshot + RemainAfterExit: the start is a command that returns, the
# unit stays active. ExecStop runs the matching shutdown command.
# -----------------------------------------------------------------------------
cat > "$SYSTEMD_DIR/witsaba-postgres.service" << EOF
[Unit]
Description=Witsaba PostgreSQL 16 (Homebrew, user data dir)
Documentation=file://$INSTALL_DIR/witsaba.env
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
EnvironmentFile=$ENV_FILE
# The wrapper is idempotent. pg_ctl exits 1 with "another server might be
# running" when the cluster is already up, which happens routinely: the install
# scripts start it, and so does a manual start before the units are enabled.
# A unit that fails in that state would restart-loop and, because the two Go
# services Require= the readiness gate, take the whole stack down with it.
ExecStart=$INSTALL_DIR/postgres/start.sh
ExecStop=$INSTALL_DIR/postgres/stop.sh
# Startup is retried, but a stopped-on-purpose cluster should stay stopped.
Restart=on-failure
RestartSec=10
TimeoutStartSec=60
TimeoutStopSec=60
# Postgres is the largest resident memory here. Cap it so the kernel OOM
# killer takes the pressure off everything else.
MemoryMax=320M
MemoryAccounting=yes

[Install]
WantedBy=default.target
EOF

# -----------------------------------------------------------------------------
# 2. messaging-core
#
# Requires= postgres plus a readiness gate. Without the gate the Go service
# opens its pgx pool during startup and exits 2 on the first refused
# connection, then Restart=always would just hot-loop against a cold cluster.
# -----------------------------------------------------------------------------
cat > "$SYSTEMD_DIR/witsaba-postgres-ready.service" << EOF
[Unit]
Description=Wait for witsaba PostgreSQL to accept connections
Requires=witsaba-postgres.service
After=witsaba-postgres.service

[Service]
Type=oneshot
RemainAfterExit=yes
EnvironmentFile=$ENV_FILE
# Single ExecStart: a oneshot unit allows several, but the first pg_isready
# would fail cold and abort the unit. The loop does the waiting instead.
ExecStart=/bin/sh -c 'i=0; while [ \$i -lt 30 ]; do $PG_BIN_DIR/pg_isready -h \$PG_HOST -p \$PG_PORT -d \$PG_DATABASE && exit 0; i=\$((i+1)); sleep 1; done; echo "postgres not ready after 30s"; exit 1'
TimeoutStartSec=45

[Install]
WantedBy=default.target
EOF

cat > "$SYSTEMD_DIR/witsaba-messaging-core.service" << EOF
[Unit]
Description=Witsaba messaging-core (embedded NATS + camera WebSocket gateway)
After=network-online.target witsaba-postgres-ready.service
Wants=network-online.target
Requires=witsaba-postgres-ready.service
PartOf=witsaba-postgres-ready.service

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_DIR/bin/messaging-core
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5
# The service drains in-flight work on SIGTERM; give it 15s, then SIGKILL.
KillSignal=SIGTERM
TimeoutStopSec=15
MemoryMax=200M
MemoryAccounting=yes
# Hardening: the binary needs no privileges. Its only disk writes are the
# gallery thumbnail cache under the capture root, which is why that path
# appears in ReadWritePaths below.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
# ReadWritePaths overrides the read-only mounts from ProtectSystem=strict and
# ProtectHome=read-only, so naming the capture root here is what makes the
# thumbnail cache writable even though it lives under $HOME.
ReadWritePaths=$INSTALL_DIR $GALLERY_ROOT_DIR

[Install]
WantedBy=default.target
EOF

# -----------------------------------------------------------------------------
# 3. workers (device discovery)
# -----------------------------------------------------------------------------
cat > "$SYSTEMD_DIR/witsaba-workers.service" << EOF
[Unit]
Description=Witsaba workers (LAN device discovery)
After=network-online.target witsaba-postgres-ready.service
Wants=network-online.target
Requires=witsaba-postgres-ready.service
PartOf=witsaba-postgres-ready.service

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_DIR/bin/workers
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5
TimeoutStopSec=15
# The scanner holds a goroutine per target IP; the pool cap bounds it but a
# hard ceiling keeps a pathological subnet from taking the whole Pi down.
MemoryMax=200M
MemoryAccounting=yes
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$INSTALL_DIR

[Install]
WantedBy=default.target
EOF

# -----------------------------------------------------------------------------
# 4. nginx
#
# Serves the static frontend and reverse-proxies /api and /stream to
# messaging-core, so the browser makes one same-origin request and nothing
# needs CORS.
# -----------------------------------------------------------------------------
cat > "$SYSTEMD_DIR/witsaba-nginx.service" << EOF
[Unit]
Description=Witsaba web UI (nginx: static files + API/WebSocket proxy)
After=network-online.target witsaba-messaging-core.service
Wants=network-online.target
# Not Requires: if messaging-core is down, nginx should still serve the UI and
# show the 50x page, which explains the outage. Restarting nginx would not fix
# a backend that is not running.

[Service]
# nginx runs in the foreground (daemon off in the generated config) so systemd
# supervises the real process. No fork, no pid file to lose track of.
Type=simple
EnvironmentFile=$ENV_FILE
# Fail before starting if the config is broken, rather than crash-looping.
ExecStartPre=$NGINX_BIN -t -c $NGINX_CONF -p $NGINX_PREFIX
ExecStart=$NGINX_BIN -c $NGINX_CONF -p $NGINX_PREFIX
ExecReload=$NGINX_BIN -c $NGINX_CONF -p $NGINX_PREFIX -s reload
ExecStop=$NGINX_BIN -c $NGINX_CONF -p $NGINX_PREFIX -s quit
Restart=on-failure
RestartSec=5
TimeoutStopSec=15
# nginx with one worker and a LAN-sized connection cap needs very little.
MemoryMax=64M
MemoryAccounting=yes
# It only reads its own tree under $HOME and talks to loopback.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$NGINX_PREFIX
# No AmbientCapabilities/CapabilityBoundingSet here on purpose. Those directives
# are for system units: a user-unit process never holds capabilities, and
# systemd fails the unit with 218/CAPABILITIES ("Failed to drop capabilities:
# Operation not permitted") when it tries to drop a set the user manager is not
# privileged to drop. NoNewPrivileges above is the directive that does apply.

[Install]
WantedBy=default.target
EOF
# -----------------------------------------------------------------------------
# 5. Enable
# -----------------------------------------------------------------------------
log_info "Reloading user manager..."
systemctl --user daemon-reload

log_info "Enabling units..."
for unit in witsaba-postgres witsaba-postgres-ready witsaba-messaging-core witsaba-workers witsaba-nginx; do
    systemctl --user enable "$unit.service" >/dev/null 2>&1 || true
done

echo ""
log_ok "Units written to $SYSTEMD_DIR and enabled"
echo ""
echo "  witsaba-postgres.service          PostgreSQL 16 (oneshot, RemainAfterExit)"
echo "  witsaba-postgres-ready.service     readiness gate for the two Go services"
echo "  witsaba-messaging-core.service    NATS :4222, WS :8080, API :8081"
echo "  witsaba-workers.service           LAN discovery every 60s"
echo "  witsaba-nginx.service            static UI on :4173 + /api and /stream proxy"
echo ""
echo "Start everything:"
echo "  systemctl --user start witsaba-postgres witsaba-postgres-ready witsaba-messaging-core witsaba-workers witsaba-nginx"
echo ""
echo "Inspect:"
echo "  systemctl --user status witsaba-messaging-core"
echo "  journalctl --user -u witsaba-messaging-core -f"
echo "  journalctl --user -u witsaba-workers -f"
echo "  journalctl --user -u witsaba-nginx -f"
echo ""
log_warn "Unit files contain no secrets: passwords live in $ENV_FILE (mode 600)."
