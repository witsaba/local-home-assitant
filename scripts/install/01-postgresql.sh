#!/bin/bash
# =============================================================================
# 01-postgresql.sh - Install PostgreSQL 16 via Homebrew (user space)
# =============================================================================
# Target: Raspberry Pi 1GB RAM, Ubuntu 24.04 arm64.
# Data lives in ~/.witsaba/postgres/data (NOT the Homebrew default), so the
# whole database is removable without touching the brew prefix.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# -----------------------------------------------------------------------------
# Homebrew discovery. Each script runs in its own process, so PATH exported by
# a previous script is never inherited. Every script must do this itself.
# -----------------------------------------------------------------------------
BREW_BIN=""
for candidate in \
    "/home/linuxbrew/.linuxbrew/bin/brew" \
    "$HOME/.linuxbrew/bin/brew" \
    "$HOME/.brew/bin/brew"; do
    [ -f "$candidate" ] && BREW_BIN="$candidate" && break
done

if [ -z "$BREW_BIN" ]; then
    echo "[x] Homebrew not found. Run 00-brew.sh first." >&2
    exit 1
fi

export HOMEBREW_PREFIX="$(dirname "$(dirname "$BREW_BIN")")"
export PATH="$HOMEBREW_PREFIX/bin:$PATH"

# -----------------------------------------------------------------------------
# Locale. Raspberry Pi OS / Ubuntu Server minimal images ship only C, C.utf8
# and POSIX. PostgreSQL's initdb fails with "invalid locale name
# en_US.UTF-8" when that locale was never generated. We force the C.UTF-8
# locale that always exists, and initdb gets an explicit --locale below.
# -----------------------------------------------------------------------------
export LANG="C.UTF-8"
export LC_ALL="C.UTF-8"
export LC_CTYPE="C.UTF-8"

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; NC='\033[0m'
log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err()  { echo -e "${RED}[x]${NC} $1" >&2; }

PG_VERSION="16"
PG_PREFIX="$HOMEBREW_PREFIX/opt/postgresql@${PG_VERSION}"
PG_DATA_DIR="$HOME/.witsaba/postgres/data"
PG_LOG_DIR="$HOME/.witsaba/postgres/logs"
INSTALL_DIR="$HOME/.witsaba"

echo ""
echo "=============================================="
echo "  Step 1: PostgreSQL ${PG_VERSION} (user space)"
echo "=============================================="
echo ""

# -----------------------------------------------------------------------------
# 1. Install the formula
# -----------------------------------------------------------------------------
if [ -d "$PG_PREFIX" ]; then
    log_ok "postgresql@${PG_VERSION} already installed at $PG_PREFIX"
else
    log_info "Installing postgresql@${PG_VERSION} via Homebrew..."
    # A failing postinstall (initdb with a missing locale) makes brew exit
    # non-zero even though the formula itself poured fine. Do not let that
    # abort the script: we run our own initdb against our own data dir.
    "$BREW_BIN" install "postgresql@${PG_VERSION}" || \
        log_warn "brew postinstall reported a failure; continuing with our own initdb"
fi

if [ ! -x "$PG_PREFIX/bin/initdb" ]; then
    log_err "postgresql@${PG_VERSION} binaries not found at $PG_PREFIX/bin"
    exit 1
fi
log_ok "Binaries: $PG_PREFIX/bin"

INITDB="$PG_PREFIX/bin/initdb"
PG_CTL="$PG_PREFIX/bin/pg_ctl"
PSQL="$PG_PREFIX/bin/psql"

# -----------------------------------------------------------------------------
# 2. Data directory
# -----------------------------------------------------------------------------
mkdir -p "$PG_DATA_DIR" "$PG_LOG_DIR"

if [ -f "$PG_DATA_DIR/PG_VERSION" ]; then
    log_ok "Cluster already initialised at $PG_DATA_DIR"
else
    log_info "Initialising cluster (this takes a few seconds)..."
    if ! "$INITDB" -D "$PG_DATA_DIR" -U "$(id -un)" \
            --locale=C.UTF-8 --encoding=UTF8 \
            --auth-local=trust --auth-host=scram-sha-256; then
        log_warn "initdb with --locale=C.UTF-8 failed; retrying with --no-locale"
        rm -rf "${PG_DATA_DIR:?}/"* 2>/dev/null || true
        "$INITDB" -D "$PG_DATA_DIR" -U "$(id -un)" \
            --no-locale --encoding=UTF8 \
            --auth-local=trust --auth-host=scram-sha-256
    fi
    log_ok "Cluster initialised"
fi

# -----------------------------------------------------------------------------
# 3. postgresql.conf tuned for 1GB RAM
# -----------------------------------------------------------------------------
log_info "Writing postgresql.conf (1GB RAM profile)..."
cat > "$PG_DATA_DIR/postgresql.conf" << 'PGCONF_EOF'
# ---- Witsaba PostgreSQL 16 :: 1GB RAM Raspberry Pi profile -----------------
listen_addresses = '127.0.0.1'
port = 5432
unix_socket_directories = '/tmp'

# ---- Connections -----------------------------------------------------------
# 20 x work_mem(4MB) = 80MB worst case. Keep this multiplication small.
max_connections = 20
superuser_reserved_connections = 3

# ---- Memory ----------------------------------------------------------------
shared_buffers = 128MB
# Planner hint only. Roughly the OS page cache you expect to be available.
effective_cache_size = 256MB
# Per sort/hash node, multiplied by workers * max_connections. Keep tiny.
work_mem = 4MB
# Per maintenance operation. Bounded by autovacuum_max_workers.
maintenance_work_mem = 64MB
# Parallel workers multiply work_mem again. Disabled on a 1GB box.
max_worker_processes = 2
max_parallel_workers = 0
max_parallel_workers_per_gather = 0
max_parallel_maintenance_workers = 0
# Parallel GUC allocation, not query workers. Safe to keep on.
dynamic_shared_memory_type = posix
huge_pages = off

# ---- WAL / durability ------------------------------------------------------
wal_buffers = 16MB
min_wal_size = 80MB
max_wal_size = 512MB
checkpoint_completion_target = 0.9
# SD cards dislike constant fsync. Coalesce into one write.
synchronous_commit = off
fsync = on
full_page_writes = on

# ---- Auth ------------------------------------------------------------------
# 04-postgres-init.sh creates roles AFTER this file is written, so the setting
# here is what hashes those passwords. It must stay scram-sha-256 or the
# scram-sha-256 pg_hba rules below will never authenticate.
password_encryption = 'scram-sha-256'

# ---- Autovacuum ------------------------------------------------------------
autovacuum = on
autovacuum_max_workers = 2
autovacuum_naptime = 60s

# ---- Logging ---------------------------------------------------------------
logging_collector = on
log_directory = 'log'
log_filename = 'postgresql-%Y-%m-%d.log'
log_rotation_age = 1d
log_rotation_size = 50MB
log_truncate_on_rotation = on
log_min_duration_statement = 500
log_line_prefix = '%m [%p] %q%u@%d '
log_timezone = 'UTC'

# ---- Locale / datetime -----------------------------------------------------
datestyle = 'iso, mdy'
timezone = 'UTC'
lc_messages = 'C'
lc_monetary = 'C'
lc_numeric = 'C'
lc_time = 'C'
default_text_search_config = 'pg_catalog.english'

# ---- Misc ------------------------------------------------------------------
shared_preload_libraries = ''
max_locks_per_transaction = 64
track_io_timing = off
PGCONF_EOF

# -----------------------------------------------------------------------------
# 4. pg_hba.conf
# -----------------------------------------------------------------------------
log_info "Writing pg_hba.conf..."
cat > "$PG_DATA_DIR/pg_hba.conf" << 'PGHBA_EOF'
# TYPE  DATABASE  USER  ADDRESS          METHOD
# Admin over the unix socket is passwordless: it is the cluster superuser and
# only reachable by processes already running as this user.
local   all       all                   trust
# TCP loopback only. listen_addresses is 127.0.0.1, so nothing from the LAN
# can reach this. Services authenticate with their own role password.
host    all       all   127.0.0.1/32     scram-sha-256
host    all       all   ::1/128          scram-sha-256
PGHBA_EOF

# -----------------------------------------------------------------------------
# 5. Control helpers (used by the systemd unit and by hand)
# -----------------------------------------------------------------------------
cat > "$INSTALL_DIR/postgres/start.sh" << EOF
#!/bin/bash
exec "$PG_CTL" -D "$PG_DATA_DIR" -l "$PG_LOG_DIR/pg_ctl.log" -w start
EOF

cat > "$INSTALL_DIR/postgres/stop.sh" << EOF
#!/bin/bash
exec "$PG_CTL" -D "$PG_DATA_DIR" -m fast -w stop
EOF

cat > "$INSTALL_DIR/postgres/status.sh" << EOF
#!/bin/bash
exec "$PG_CTL" -D "$PG_DATA_DIR" status
EOF

chmod +x "$INSTALL_DIR/postgres/"*.sh

# -----------------------------------------------------------------------------
# 6. Shared runtime config, consumed by 04/10/11/12
# -----------------------------------------------------------------------------
cat > "$INSTALL_DIR/witsaba.env" << ENV_EOF
# Witsaba runtime environment.
# Generated by 01-postgresql.sh -- edit by hand to change passwords.
# Consumed two ways, so the format must stay strict KEY=VALUE:
#   1. shell:  set -a; . ~/.witsaba/witsaba.env; set +a
#   2. systemd: EnvironmentFile=%h/.witsaba/witsaba.env
# No 'export' prefix, no quotes, comments start with #.

# --- Local tool paths (used by the installer and the postgres unit) ---
PG_BIN_DIR=$PG_PREFIX/bin
PG_DATA_DIR=$PG_DATA_DIR
PG_LOG_DIR=$PG_LOG_DIR

# --- workers service reads PG_* ---
PG_HOST=127.0.0.1
PG_PORT=5432
PG_DATABASE=witsaba
PG_USER=pg-worker
PG_WORKER_PASSWORD=changeme-worker

# --- messaging-core reads MESSAGING_CORE_PG_* ---
MESSAGING_CORE_PG_HOST=127.0.0.1
MESSAGING_CORE_PG_PORT=5432
MESSAGING_CORE_PG_DATABASE=witsaba
MESSAGING_CORE_PG_USER=pg-messaging-core
MESSAGING_CORE_PG_PASSWORD=changeme-messaging

# --- shared by both services ---
NATS_HOST=127.0.0.1
NATS_PORT=4222
STREAM_PORT=8080
API_PORT=8081
LOG_LEVEL=info
DISCOVERY_INTERVAL_SECONDS=60
DISCOVERY_WORKER_POOL_SIZE=32
DISCOVERY_PROBE_TIMEOUT_MS=1500

# --- migration bookkeeping (admin role is NOLOGIN, used only for GRANTs) ---
PG_ADMIN_USER=pg-admin
ENV_EOF

chmod 600 "$INSTALL_DIR/witsaba.env"
log_info "Env file written (mode 600, contains passwords)"

echo ""
log_ok "PostgreSQL ${PG_VERSION} ready"
log_info "Data dir : $PG_DATA_DIR"
log_info "Binaries : $PG_PREFIX/bin"
log_info "Env file : $INSTALL_DIR/witsaba.env"
log_info ""
log_info "Next: ./04-postgres-init.sh"
