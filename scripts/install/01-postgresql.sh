#!/bin/bash
# =============================================================================
# 01-postgresql.sh - Install PostgreSQL via Homebrew
# =============================================================================
# Installs PostgreSQL 16 optimized for 1GB RAM Raspberry Pi.
# No root privileges required - uses Homebrew in user space.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# =============================================================================
# Find and set up Homebrew (each script must do this - new shell process)
# =============================================================================
BREW_FOUND=false
BREW_PATHS=(
    "/home/linuxbrew/.linuxbrew/bin/brew"
    "$HOME/.linuxbrew/bin/brew"
    "$HOME/.brew/bin/brew"
)

for brew_path in "${BREW_PATHS[@]}"; do
    if [ -f "$brew_path" ]; then
        export HOMEBREW_PREFIX="$(dirname "$(dirname "$brew_path")")"
        export PATH="$(dirname "$brew_path"):$PATH"
        BREW_FOUND=true
        break
    fi
done

if [ "$BREW_FOUND" != true ]; then
    echo "[✗] Homebrew not found. Run 00-brew.sh first."
    exit 1
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

INSTALL_DIR="$HOME/.witsaba"

echo ""
echo "=============================================="
echo "  Step 1: PostgreSQL Installation"
echo "=============================================="
echo ""

# Install PostgreSQL 16
log_info "Installing PostgreSQL 16 via Homebrew..."
brew install postgresql@16 --quiet

# Create data directory
PG_DATA_DIR="$HOME/.witsaba/postgres/data"
PG_LOG_DIR="$HOME/.witsaba/postgres/logs"
mkdir -p "$PG_DATA_DIR" "$PG_LOG_DIR"

# Initialize database if needed
if [ ! -d "$PG_DATA_DIR/base" ]; then
    log_info "Initializing PostgreSQL database..."
    /home/linuxbrew/.linuxbrew/bin/initdb -D "$PG_DATA_DIR" --no-locale --encoding=UTF8 2>/dev/null || true
fi

# Create optimized postgresql.conf for 1GB RAM
log_info "Configuring PostgreSQL for 1GB RAM..."
cat > "$PG_DATA_DIR/postgresql.conf" << 'PGCONF_EOF'
# Witsaba PostgreSQL - Optimized for 1GB RAM
listen_addresses = 'localhost'
port = 5432
max_connections = 20
shared_buffers = 128MB
effective_cache_size = 256MB
maintenance_work_mem = 64MB
work_mem = 16MB
max_worker_processes = 2
max_parallel_workers_per_gather = 1
max_parallel_workers = 2
log_destination = 'stderr'
logging_collector = on
log_directory = 'logs'
log_filename = 'postgresql-%Y-%m-%d.log'
PGCONF_EOF

# Create pg_hba.conf
cat > "$PG_DATA_DIR/pg_hba.conf" << 'PGHBA_EOF'
local   all    all    trust
host    all    all    127.0.0.1/32    trust
PGHBA_EOF

log_ok "PostgreSQL configured at: $PG_DATA_DIR"
log_ok "Run 04-postgres-init.sh next to create the witsaba database"
