#!/bin/bash
# =============================================================================
# 01-postgresql.sh - Install PostgreSQL via Homebrew
# =============================================================================
# Installs PostgreSQL 16 optimized for 1GB RAM Raspberry Pi.
# No root privileges required - uses Homebrew in user space.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$(dirname "$SCRIPT_DIR")"

# Source shared brew helpers
source "$SCRIPT_DIR/_brew-helpers.sh"

# Setup brew PATH
if ! setup_brew_path; then
    log_err "Homebrew is not installed!"
    log_err "Run 00-brew.sh first."
    exit 1
fi

echo ""
echo "=============================================="
echo "  Step 1: PostgreSQL Installation"
echo "=============================================="
echo ""

# Setup brew PATH (handles zsh -> bash -> brew chain)
if ! command -v brew &> /dev/null; then
    # Try common Homebrew locations
    BREW_PATHS=(
        "/home/linuxbrew/.linuxbrew/bin/brew"
        "$HOME/.linuxbrew/bin/brew"
        "$HOME/.brew/bin/brew"
    )
    for brew_path in "${BREW_PATHS[@]}"; do
        if [ -f "$brew_path" ]; then
            export PATH="$(dirname "$brew_path"):$PATH"
            break
        fi
    done
fi

# Source brew environment
if command -v brew &> /dev/null; then
    eval "$(brew --env 2>/dev/null)" 2>/dev/null || true
else
    log_err "Homebrew is not installed!"
    log_err "Run 00-brew.sh first."
    exit 1
fi

# Install PostgreSQL 16
log_info "Installing PostgreSQL 16 via Homebrew..."
brew install postgresql@16 --quiet

# Create data directory in user space
PG_DATA_DIR="$HOME/.witsaba/postgres/data"
PG_LOG_DIR="$HOME/.witsaba/postgres/logs"
PG_SOCKET_DIR="$HOME/.witsaba/postgres/socket"

log_info "Creating PostgreSQL directories..."
mkdir -p "$PG_DATA_DIR" "$PG_LOG_DIR" "$PG_SOCKET_DIR"

# Initialize database if not exists
if [ ! -d "$PG_DATA_DIR/base" ]; then
    log_info "Initializing PostgreSQL database..."
    initdb -D "$PG_DATA_DIR" -U "$USER" --no-locale --encoding=UTF8 2>/dev/null || true
fi

# Create optimized postgresql.conf for 1GB RAM
log_info "Configuring PostgreSQL for 1GB RAM..."
cat > "$PG_DATA_DIR/postgresql.conf" << 'PGCONF_EOF'
# Witsaba PostgreSQL Configuration - Optimized for 1GB RAM

# Connection
listen_addresses = 'localhost'
port = 5432
max_connections = 20

# Memory (optimized for 1GB RAM)
shared_buffers = 128MB
effective_cache_size = 256MB
maintenance_work_mem = 64MB
work_mem = 16MB

# Parallel queries (limited for ARM)
max_worker_processes = 2
max_parallel_workers_per_gather = 1
max_parallel_workers = 2
max_parallel_maintenance_workers = 1

# Storage
fsync = on
full_page_writes = on
wal_buffers = 16MB

# Logging
log_destination = 'stderr'
logging_collector = on
log_directory = 'logs'
log_filename = 'postgresql-%Y-%m-%d_%H%M%S.log'
log_rotation_age = 1d
log_rotation_size = 100MB
log_line_prefix = '%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h '

# Locale
datestyle = 'iso, mdy'
timezone = 'UTC'
lc_messages = 'C'
lc_monetary = 'C'
lc_numeric = 'C'
lc_time = 'C'
default_text_search_config = 'pg_catalog.english'

# Disable unnecessary features
autovacuum = on
pgCONF_EOF

# Create pg_hba.conf (local trust + localhost)
cat > "$PG_DATA_DIR/pg_hba.conf" << 'PGHBA_EOF'
# TYPE  DATABASE        USER            ADDRESS                 METHOD
local   all            all                                     trust
host    all            all             127.0.0.1/32            trust
host    all            all             ::1/128                 trust
PGHBA_EOF

log_ok "PostgreSQL configured!"
log_info "Data directory: $PG_DATA_DIR"
log_info "Log directory: $PG_LOG_DIR"
log_info ""

# Create helper scripts
cat > "$HOME/.witsaba/postgres/start.sh" << 'START_EOF'
#!/bin/bash
PG_DATA_DIR="$HOME/.witsaba/postgres/data"
pg_ctl -D "$PG_DATA_DIR" -l "$HOME/.witsaba/postgres/logs/postgresql.log" start
START_EOF
chmod +x "$HOME/.witsaba/postgres/start.sh"

cat > "$HOME/.witsaba/postgres/stop.sh" << 'STOP_EOF'
#!/bin/bash
PG_DATA_DIR="$HOME/.witsaba/postgres/data"
pg_ctl -D "$PG_DATA_DIR" stop -m fast
STOP_EOF
chmod +x "$HOME/.witsaba/postgres/stop.sh"

log_ok "Helper scripts created:"
log_info "  $HOME/.witsaba/postgres/start.sh"
log_info "  $HOME/.witsaba/postgres/stop.sh"
log_ok ""
log_ok "PostgreSQL installation complete!"
log_info "Next: Run 04-postgres-init.sh to create the witsaba database"
