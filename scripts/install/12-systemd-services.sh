#!/bin/bash
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
export HOMEBREW_PREFIX="$(dirname "$(dirname "$BREW_BIN")")"
export PATH="$HOMEBREW_PREFIX/bin:$PATH"

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; NC='\033[0m'
log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err()  { echo -e "${RED}[x]${NC} $1" >&2; }

echo ""
echo "=============================================="
echo "  Step 12: systemd user units"
echo "=============================================="
echo ""

ENV_FILE="$INSTALL_DIR/witsaba.env"
[ -f "$ENV_FILE" ] || { log_err "$ENV_FILE missing. Run 01-postgresql.sh first."; exit 1; }

set -a; . "$ENV_FILE"; set +a

PG_BIN_DIR="${PG_BIN_DIR:-$HOMEBREW_PREFIX/opt/postgresql@16/bin}"
PG_CTL="$PG_BIN_DIR/pg_ctl"

# Resolve the Node toolchain. $HOMEBREW_PREFIX/bin/node is NOT the right answer:
# Homebrew's versioned node formulae are keg-only, so brew/bin/node is whatever
# else happens to be installed. Resolve through opt/node@22 (or whatever
# 03-node.sh recorded) and prefer the paths witaba.env already pinned, so the
# unit runs the same interpreter the build used.
setup_node_env
if [ -n "${NODE_BIN_OVERRIDE:-}" ]; then
    NODE_BIN="$NODE_BIN_OVERRIDE"
fi
PNPM_BIN="$(pnpm_bin)"

if [ ! -x "$PNPM_BIN" ]; then
    log_err "pnpm not found at $PNPM_BIN. Run 03-node.sh first."
    exit 1
fi
if [ ! -x "$PG_CTL" ]; then
    log_err "pg_ctl not found at $PG_CTL. Run 01-postgresql.sh first."
    exit 1
fi
log_ok "pnpm   : $PNPM_BIN ($("$PNPM_BIN" --version 2>/dev/null || echo '?'))"
log_ok "pg_ctl : $PG_CTL"

for bin in "$PG_CTL"; do
    [ -x "$bin" ] || { log_err "Required binary not executable: $bin"; exit 1; }
done

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
ExecStart=$PG_CTL -D $PG_DATA_DIR -l $PG_LOG_DIR/pg_ctl.log -w -t 30 start
ExecStop=$PG_CTL -D $PG_DATA_DIR -m fast -w -t 30 stop
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
# Hardening: the binary needs no privileges and writes nothing to disk.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$INSTALL_DIR

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
# 4. web_ui
#
# The Qwik scaffold has no SSR adapter, so `vite preview` is a dev-server
# process. It stays in PATH and runs as the same user, but it is the heaviest
# single process in the stack -- hence the lowest heap cap.
# -----------------------------------------------------------------------------
cat > "$SYSTEMD_DIR/witsaba-web-ui.service" << EOF
[Unit]
Description=Witsaba web UI (Qwik via vite preview)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR/frontend
EnvironmentFile=$ENV_FILE
Environment=NODE_ENV=production
Environment=HOST=0.0.0.0
Environment=PORT=4173
# V8 heap ceiling. 1GB box with ~350MB already spoken for: keep V8 honest
# instead of letting it grow to the machine's memory limit.
Environment=NODE_OPTIONS=--max-old-space-size=192
# pnpm is a shim that resolves 'node' from PATH. Without this the unit can
# silently start under whatever node happens to be in brew/bin, which is not
# the keg-only node@22 the build used. Note the shebang inside a unit file
# cannot interpolate \$, so this is expanded by the generating script.
Environment=PATH=$(dirname "$NODE_BIN"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=$PNPM_BIN preview --host 0.0.0.0 --port 4173
Restart=always
RestartSec=5
TimeoutStopSec=15
MemoryMax=300M
MemoryAccounting=yes
NoNewPrivileges=yes

[Install]
WantedBy=default.target
EOF

# -----------------------------------------------------------------------------
# 5. Enable
# -----------------------------------------------------------------------------
log_info "Reloading user manager..."
systemctl --user daemon-reload

log_info "Enabling units..."
for unit in witsaba-postgres witsaba-postgres-ready witsaba-messaging-core witsaba-workers witsaba-web-ui; do
    systemctl --user enable "$unit.service" >/dev/null 2>&1 || true
done

echo ""
log_ok "Units written to $SYSTEMD_DIR and enabled"
echo ""
echo "  witsaba-postgres.service          PostgreSQL 16 (oneshot, RemainAfterExit)"
echo "  witsaba-postgres-ready.service     readiness gate for the two Go services"
echo "  witsaba-messaging-core.service    NATS :4222, WS :8080, API :8081"
echo "  witsaba-workers.service           LAN discovery every 60s"
echo "  witsaba-web-ui.service            Qwik on :4173"
echo ""
echo "Start everything:"
echo "  systemctl --user start witsaba-postgres witsaba-postgres-ready witsaba-messaging-core witsaba-workers witsaba-web-ui"
echo ""
echo "Inspect:"
echo "  systemctl --user status witsaba-messaging-core"
echo "  journalctl --user -u witsaba-messaging-core -f"
echo "  journalctl --user -u witsaba-workers -f"
echo "  journalctl --user -u witsaba-web-ui -f"
echo ""
log_warn "Unit files contain no secrets: passwords live in $ENV_FILE (mode 600)."
