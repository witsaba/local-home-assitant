#!/bin/bash
# =============================================================================
# 04-postgres-init.sh - Initialize witsaba database in PostgreSQL
# =============================================================================
# Creates the witsaba database and user roles needed by the services.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

echo ""
echo "=============================================="
echo "  Step 4: PostgreSQL Database Setup"
echo "=============================================="
echo ""

# Check if PostgreSQL is running
PG_DATA_DIR="$HOME/.witsaba/postgres/data"

if ! pg_ctl -D "$PG_DATA_DIR" status &> /dev/null; then
    log_info "Starting PostgreSQL..."
    "$HOME/.witsaba/postgres/start.sh"
    sleep 2
fi

# Configuration (same as docker-compose.env)
DB_NAME="${POSTGRES_DB:-witsaba}"
DB_USER="${PG_USER:-pg-worker}"
DB_ADMIN="${PG_ADMIN:-pg-admin}"
WORKER_PASSWORD="${PG_WORKER_PASSWORD:-changeme-worker}"
MESSAGING_PASSWORD="${PG_MESSAGING_PASSWORD:-changeme-messaging}"

log_info "Database: $DB_NAME"
log_info "Worker User: $DB_USER"
log_info ""

# Create database if not exists
log_info "Ensuring database '$DB_NAME' exists..."
psql -h localhost -U "$USER" -d postgres -c "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" | grep -q 1 || \
    psql -h localhost -U "$USER" -d postgres -c "CREATE DATABASE $DB_NAME" 2>/dev/null || \
    log_warn "Database may already exist"

# Create roles
log_info "Creating roles..."

# pg-admin role (owns the schema)
psql -h localhost -U "$USER" -d postgres << SQL_EOF || log_warn "pg-admin may already exist"
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$DB_ADMIN') THEN
        CREATE ROLE $DB_ADMIN WITH LOGIN PASSWORD '$DB_ADMIN';
    END IF;
END
\$\$;
SQL_EOF

# pg-worker role (discovery job writes devices here)
psql -h localhost -U "$USER" -d postgres << SQL_EOF || log_warn "pg-worker may already exist"
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$DB_USER') THEN
        CREATE ROLE $DB_USER WITH LOGIN PASSWORD '$WORKER_PASSWORD';
    END IF;
END
\$\$;
SQL_EOF

# pg-messaging-core role (SELECT only)
MESSAGING_USER="${PG_MESSAGING_USER:-pg-messaging-core}"
psql -h localhost -U "$USER" -d postgres << SQL_EOF || log_warn "pg-messaging-core may already exist"
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$MESSAGING_USER') THEN
        CREATE ROLE $MESSAGING_USER WITH LOGIN PASSWORD '$MESSAGING_PASSWORD';
    END IF;
END
\$\$;
SQL_EOF

# Grant privileges
log_info "Granting privileges..."
psql -h localhost -U "$USER" -d "$DB_NAME" << SQL_EOF
-- Grant schema usage
GRANT USAGE ON SCHEMA witsaba TO $DB_USER, $MESSAGING_USER;

-- Grant schema create for admin
GRANT CREATE ON SCHEMA witsaba TO $DB_ADMIN;

-- Switch to witsaba schema
CREATE SCHEMA IF NOT EXISTS witsaba;
GRANT ALL ON SCHEMA witsaba TO $DB_ADMIN;
GRANT USAGE ON SCHEMA witsaba TO $DB_USER, $MESSAGING_USER;
SQL_EOF

# Create devices table (if not exists)
log_info "Creating devices table..."
psql -h localhost -U "$USER" -d "$DB_NAME" << 'SQL_EOF'
CREATE TABLE IF NOT EXISTS witsaba.devices (
    mac             TEXT        PRIMARY KEY,
    name            TEXT,
    fw              TEXT,
    chip            TEXT,
    last_source_ip  INET,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL
);

-- Grant permissions to roles
GRANT SELECT, INSERT, UPDATE, DELETE ON witsaba.devices TO pg-worker;
GRANT SELECT ON witsaba.devices TO pg-messaging-core;
GRANT ALL ON witsaba.devices TO pg-admin;
SQL_EOF

# Show results
echo ""
log_ok "Database setup complete!"
echo ""
echo "Connection info:"
echo "  Host: localhost"
echo "  Port: 5432"
echo "  Database: $DB_NAME"
echo "  Schema: witsaba"
echo ""
echo "Users created:"
echo "  pg-admin:       Full access (for schema management)"
echo "  pg-worker:     Read/write devices (for discovery)"
echo "  pg-messaging-core: Read-only (for messaging-core)"
echo ""

# Save connection info
mkdir -p "$HOME/.witsaba"
cat > "$HOME/.witsaba/.env" << ENV_EOF
# Witsaba PostgreSQL Configuration
POSTGRES_DB=witsaba
PG_USER=pg-worker
PG_PASSWORD=$WORKER_PASSWORD
PG_HOST=127.0.0.1
PG_PORT=5432
PG_MESSAGING_CORE_PASSWORD=$MESSAGING_PASSWORD
ENV_EOF

log_ok "Connection info saved to ~/.witsaba/.env"
log_info "Next: Run 10-build-go.sh to build the Go services"
