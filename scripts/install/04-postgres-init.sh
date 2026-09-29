#!/bin/bash
# =============================================================================
# 04-postgres-init.sh - Create the witsaba database, roles and schema
# =============================================================================
# Idempotent: safe to re-run. Creates the least-privilege role model the Go
# services expect, matching services/postgres/init/*.sql from the compose stack.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$HOME/.witsaba"
PG_DATA_DIR="$INSTALL_DIR/postgres/data"

export LANG="C.UTF-8"
export LC_ALL="C.UTF-8"

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
echo "  Step 4: witsaba database bootstrap"
echo "=============================================="
echo ""

# Load shared config written by 01-postgresql.sh
if [ -f "$INSTALL_DIR/witsaba.env" ]; then
    set -a; . "$INSTALL_DIR/witsaba.env"; set +a
else
    log_err "$INSTALL_DIR/witsaba.env missing. Run 01-postgresql.sh first."
    exit 1
fi

SUPERUSER="$(id -un)"
PSQL="$HOMEBREW_PREFIX/opt/postgresql@16/bin/psql"

# -----------------------------------------------------------------------------
# 1. Start the cluster if it is not already accepting connections
# -----------------------------------------------------------------------------
if ! "$PSQL" -h "$PG_HOST" -p "$PG_PORT" -U "$SUPERUSER" -d postgres -c 'SELECT 1' >/dev/null 2>&1; then
    log_info "Starting PostgreSQL..."
    "$INSTALL_DIR/postgres/start.sh" || {
        log_err "Could not start PostgreSQL. Check $PG_LOG_DIR/pg_ctl.log"
        exit 1
    }
    sleep 1
fi
log_ok "PostgreSQL is accepting connections"

# Local socket + trust auth, so no password is needed for admin work.
ADMIN_PSQL=("$PSQL" -h /tmp -p "$PG_PORT" -U "$SUPERUSER" -d postgres)

# -----------------------------------------------------------------------------
# 2. Database
# -----------------------------------------------------------------------------
if "${ADMIN_PSQL[@]}" -tAc "SELECT 1 FROM pg_database WHERE datname='$PG_DATABASE'" | grep -q 1; then
    log_ok "Database '$PG_DATABASE' exists"
else
    log_info "Creating database '$PG_DATABASE'..."
    "${ADMIN_PSQL[@]}" -c "CREATE DATABASE $PG_DATABASE ENCODING 'UTF8' TEMPLATE template0"
    log_ok "Database created"
fi

# -----------------------------------------------------------------------------
# 3. Roles
#
# pg-admin       : owns the schema. NOLOGIN, so nothing can authenticate as it.
# pg-worker      : LOGIN. workers service. CRUD on devices.
# pg-messaging-core: LOGIN. messaging-core service. SELECT only.
# -----------------------------------------------------------------------------
DB_PSQL=("$PSQL" -h /tmp -p "$PG_PORT" -U "$SUPERUSER" -d "$PG_DATABASE")

log_info "Creating roles..."

"${ADMIN_PSQL[@]}" -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$PG_ADMIN_USER') THEN
        CREATE ROLE $PG_ADMIN_USER NOLOGIN;
    END IF;
END
\$\$;
ALTER ROLE $PG_ADMIN_USER NOLOGIN;

DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$PG_USER') THEN
        CREATE ROLE $PG_USER LOGIN;
    END IF;
END
\$\$;
ALTER ROLE $PG_USER LOGIN PASSWORD '$PG_WORKER_PASSWORD';

DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$PG_MESSAGING_CORE_USER') THEN
        CREATE ROLE $PG_MESSAGING_CORE_USER LOGIN;
    END IF;
END
\$\$;
ALTER ROLE $PG_MESSAGING_CORE_USER LOGIN PASSWORD '$PG_MESSAGING_CORE_PASSWORD';
SQL

log_ok "Roles ready: $PG_ADMIN_USER (owner), $PG_USER, $PG_MESSAGING_CORE_USER"

# -----------------------------------------------------------------------------
# 4. Schema and tables
# -----------------------------------------------------------------------------
log_info "Creating schema and tables..."

"${DB_PSQL[@]}" -v ON_ERROR_STOP=1 <<SQL
CREATE SCHEMA IF NOT EXISTS witsaba AUTHORIZATION $PG_ADMIN_USER;

-- devices is the only table the v1 stack needs.
CREATE TABLE IF NOT EXISTS witsaba.devices (
    mac             TEXT        PRIMARY KEY,
    name            TEXT,
    fw              TEXT,
    chip            TEXT,
    last_source_ip  INET,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL
);

-- The discovery scanner reports devices whose last_seen_at falls inside the
-- last 60s. Without this index that query is a full scan of the table.
CREATE INDEX IF NOT EXISTS devices_last_seen_at_idx
    ON witsaba.devices (last_seen_at DESC);

ALTER TABLE witsaba.devices OWNER TO $PG_ADMIN_USER;
SQL

# -----------------------------------------------------------------------------
# 5. Grants
# -----------------------------------------------------------------------------
log_info "Applying grants..."
"${DB_PSQL[@]}" -v ON_ERROR_STOP=1 <<SQL
GRANT USAGE ON SCHEMA witsaba TO $PG_USER, $PG_MESSAGING_CORE_USER;

-- workers: full CRUD, no DDL.
GRANT SELECT, INSERT, UPDATE, DELETE ON witsaba.devices TO $PG_USER;

-- messaging-core: read-only. It never writes.
GRANT SELECT ON witsaba.devices TO $PG_MESSAGING_CORE_USER;

-- Neither role may create objects in the schema.
REVOKE CREATE ON SCHEMA witsaba FROM PUBLIC;
REVOKE ALL ON SCHEMA witsaba FROM $PG_USER, $PG_MESSAGING_CORE_USER;
GRANT USAGE ON SCHEMA witsaba TO $PG_USER, $PG_MESSAGING_CORE_USER;
SQL

log_ok "Grants applied"

# -----------------------------------------------------------------------------
# 6. Verify as the application roles
# -----------------------------------------------------------------------------
log_info "Verifying pg-worker can write..."
PGPASSWORD="$PG_WORKER_PASSWORD" "$PSQL" -h "$PG_HOST" -p "$PG_PORT" \
    -U "$PG_USER" -d "$PG_DATABASE" -tAc "SELECT 1" >/dev/null
log_ok "pg-worker authenticates"

log_info "Verifying pg-messaging-core can read..."
PGPASSWORD="$PG_MESSAGING_CORE_PASSWORD" "$PSQL" -h "$PG_HOST" -p "$PG_PORT" \
    -U "$PG_MESSAGING_CORE_USER" -d "$PG_DATABASE" \
    -tAc "SELECT count(*) FROM witsaba.devices" >/dev/null
log_ok "pg-messaging-core authenticates"

echo ""
log_ok "Database bootstrap complete"
log_info "  database : $PG_DATABASE"
log_info "  schema   : witsaba"
log_info "  tables   : witsaba.devices"
log_info ""
log_warn "Default passwords are in $INSTALL_DIR/witsaba.env"
log_warn "Change them there AND in the database before exposing anything."
log_info ""
log_info "Next: ./10-build-go.sh"
