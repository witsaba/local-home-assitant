#!/bin/bash
# =============================================================================
# 04-postgres-init.sh - Initialize witsaba database in PostgreSQL
# =============================================================================
# Creates the witsaba database and user roles needed by the services.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# =============================================================================
# Find and set up Homebrew
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

echo ""
echo "=============================================="
echo "  Step 4: PostgreSQL Database Setup"
echo "=============================================="
echo ""

PG_DATA_DIR="$HOME/.witsaba/postgres/data"

# Start PostgreSQL if not running
log_info "Starting PostgreSQL..."
pg_ctl -D "$PG_DATA_DIR" status &>/dev/null || pg_ctl -D "$PG_DATA_DIR" start 2>/dev/null || true
sleep 2

# Configuration
DB_NAME="witsaba"
DB_USER="pg-worker"
DB_ADMIN="pg-admin"
MESSAGING_USER="pg-messaging-core"
WORKER_PASSWORD="changeme-worker"
MESSAGING_PASSWORD="changeme-messaging"

log_info "Creating database: $DB_NAME"

# Create database
psql -h localhost -U "$USER" -d postgres -c "CREATE DATABASE $DB_NAME" 2>/dev/null || log_info "Database may already exist"

# Create roles
log_info "Creating roles..."
psql -h localhost -U "$USER" -d postgres << SQL_EOF
DO \$\$ BEGIN
    CREATE ROLE $DB_USER WITH LOGIN PASSWORD '$WORKER_PASSWORD';
EXCEPTION WHEN duplicate_object THEN NULL;
END \$\$;
DO \$\$ BEGIN
    CREATE ROLE $DB_ADMIN WITH LOGIN SUPERUSER PASSWORD '$DB_ADMIN';
EXCEPTION WHEN duplicate_object THEN NULL;
END \$\$;
DO \$\$ BEGIN
    CREATE ROLE $MESSAGING_USER WITH LOGIN PASSWORD '$MESSAGING_PASSWORD';
EXCEPTION WHEN duplicate_object THEN NULL;
END \$\$;
SQL_EOF

# Create schema and table
log_info "Creating schema and tables..."
psql -h localhost -U "$USER" -d "$DB_NAME" << 'SQL_EOF'
CREATE SCHEMA IF NOT EXISTS witsaba;
GRANT ALL ON SCHEMA witsaba TO $DB_ADMIN;
GRANT USAGE ON SCHEMA witsaba TO pg-worker, pg-messaging-core;

CREATE TABLE IF NOT EXISTS witsaba.devices (
    mac             TEXT PRIMARY KEY,
    name            TEXT,
    fw              TEXT,
    chip            TEXT,
    last_source_ip  INET,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL
);

GRANT SELECT, INSERT, UPDATE, DELETE ON witsaba.devices TO pg-worker;
GRANT SELECT ON witsaba.devices TO pg-messaging-core;
SQL_EOF

# Save config
mkdir -p "$HOME/.witsaba"
cat > "$HOME/.witsaba/.env" << ENV_EOF
POSTGRES_DB=witsaba
PG_USER=pg-worker
PG_PASSWORD=$WORKER_PASSWORD
PG_HOST=127.0.0.1
PG_PORT=5432
PG_MESSAGING_CORE_PASSWORD=$MESSAGING_PASSWORD
ENV_EOF

log_ok "Database setup complete!"
log_ok "Config saved to ~/.witsaba/.env"
