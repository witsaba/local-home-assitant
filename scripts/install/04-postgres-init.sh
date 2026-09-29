#!/bin/bash
# =============================================================================
# 04-postgres-init.sh - Create the witsaba database, roles and schema
# =============================================================================
# Idempotent: safe to re-run at any time.
#
# Role model and grants are kept byte-compatible with the Docker stack so the
# same services work against either target:
#   services/postgres/scripts/02-roles.sql  -> role creation
#   services/postgres/init/03-schema.sql    -> schema + default privileges
#
# Two things differ from the compose stack, unavoidably:
#   1. initdb made the invoking OS user the cluster superuser, so "pg-admin"
#      is created here as a NOLOGIN role that only owns objects. In compose
#      the postgres image creates POSTGRES_USER=pg-admin as a real superuser.
#   2. The compose init deliberately creates no tables (migrations own the
#      DDL). The native install needs witsaba.devices to exist for the two Go
#      services to start, so it is created here and granted explicitly to
#      match what the default privileges below would have produced.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$HOME/.witsaba"

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

ENV_FILE="$INSTALL_DIR/witsaba.env"
[ -f "$ENV_FILE" ] || { log_err "$ENV_FILE missing. Run 01-postgresql.sh first."; exit 1; }
set -a; . "$ENV_FILE"; set +a

# -----------------------------------------------------------------------------
# Validate the config before touching the database.
#
# A missing or empty variable used to reach the SQL layer and surface as a
# syntax error like:
#     ERROR:  zero-length delimited identifier at or near """
# That is a terrible error for a configuration problem. Fail here instead,
# naming the variable, and point at the step that generates this file.
# -----------------------------------------------------------------------------
REQUIRED_VARS=(
    PG_HOST PG_PORT PG_DATABASE
    PG_USER PG_WORKER_PASSWORD
    MESSAGING_CORE_PG_USER MESSAGING_CORE_PG_PASSWORD
    PG_ADMIN_USER
)
missing=()
for var in "${REQUIRED_VARS[@]}"; do
    [ -n "${!var:-}" ] || missing+=("$var")
done
if [ ${#missing[@]} -gt 0 ]; then
    log_err "incomplete config in $ENV_FILE -- empty: ${missing[*]}"
    log_err "re-run 01-postgresql.sh to regenerate it"
    exit 1
fi

# These passwords are about to be written into the database. If the env file
# was hand-edited into something weak, say so here rather than discovering it
# during an incident. Non-fatal: an operator may legitimately supply their own.
. "$SCRIPT_DIR/_lib.sh"
if is_weak_secret "$PG_WORKER_PASSWORD" || is_weak_secret "$MESSAGING_CORE_PG_PASSWORD"; then
    log_warn "a database password in $ENV_FILE looks weak (placeholder, short or low-entropy)"
    log_warn "re-run 01-postgresql.sh to regenerate, or set your own in $ENV_FILE"
fi

SUPERUSER="$(id -un)"
PSQL="$HOMEBREW_PREFIX/opt/postgresql@16/bin/psql"

# Admin access over the unix socket: pg_hba.conf grants `trust` for local
# connections, so no password is needed and the password never reaches argv.
ADMIN=("$PSQL" -h /tmp -p "$PG_PORT" -U "$SUPERUSER" -d postgres -v ON_ERROR_STOP=1)
DB=("$PSQL" -h /tmp -p "$PG_PORT" -U "$SUPERUSER" -d "$PG_DATABASE" -v ON_ERROR_STOP=1)

run_admin() { "${ADMIN[@]}" -c "$1"; }
run_db()    { "${DB[@]}"    -c "$1"; }

# Role names contain hyphens, which is illegal in a bare SQL identifier. Every
# reference below is double quoted, exactly as services/postgres/init/03-schema.sql
# does it. A single missing quote pair here is a syntax error, not a warning.
Q_ADMIN="${PG_ADMIN_USER}"                   # pg-admin
Q_WORKER="${PG_USER}"                        # pg-worker
Q_MESSAGING="${MESSAGING_CORE_PG_USER}"      # pg-messaging-core
WORKER_PW="${PG_WORKER_PASSWORD}"
MESSAGING_PW="${MESSAGING_CORE_PG_PASSWORD}"

role_exists() {
    "${ADMIN[@]}" -tAc "SELECT 1 FROM pg_roles WHERE rolname = '$1'" | grep -q 1
}

# -----------------------------------------------------------------------------
# 1. Start the cluster
# -----------------------------------------------------------------------------
if ! "${ADMIN[@]}" -tAc 'SELECT 1' >/dev/null 2>&1; then
    log_info "Starting PostgreSQL..."
    "$INSTALL_DIR/postgres/start.sh" || {
        log_err "Could not start PostgreSQL. See $PG_LOG_DIR/pg_ctl.log"
        exit 1
    }
    sleep 1
fi
log_ok "PostgreSQL is accepting connections (superuser: $SUPERUSER)"

# -----------------------------------------------------------------------------
# 2. Database
# -----------------------------------------------------------------------------
if "${ADMIN[@]}" -tAc "SELECT 1 FROM pg_database WHERE datname = '$PG_DATABASE'" | grep -q 1; then
    log_ok "Database '$PG_DATABASE' already exists"
else
    log_info "Creating database '$PG_DATABASE'..."
    run_admin "CREATE DATABASE \"$PG_DATABASE\" ENCODING 'UTF8' TEMPLATE template0"
    log_ok "Database created"
fi

# -----------------------------------------------------------------------------
# 3. Roles
#
#   "pg-admin"            NOLOGIN. Owns the schema and the tables. Nothing
#                         authenticates as it, so it cannot be used to connect.
#   "pg-worker"           LOGIN. workers service. DML on witsaba.devices.
#   "pg-messaging-core"   LOGIN. messaging-core service. SELECT only.
#
# Neither service role gets CREATEDB, CREATEROLE or SUPERUSER. The ALTER after
# each CREATE also rotates the password, so editing witsaba.env and re-running
# this script is the supported way to change credentials.
# -----------------------------------------------------------------------------
log_info "Ensuring roles..."

if ! role_exists "$Q_ADMIN"; then
    run_admin "CREATE ROLE \"$Q_ADMIN\" NOLOGIN"
    log_ok "  created role \"$Q_ADMIN\" (NOLOGIN)"
else
    run_admin "ALTER ROLE \"$Q_ADMIN\" NOLOGIN"
    log_ok "  role \"$Q_ADMIN\" present (NOLOGIN)"
fi

if ! role_exists "$Q_WORKER"; then
    run_admin "CREATE ROLE \"$Q_WORKER\" LOGIN PASSWORD '$WORKER_PW'"
    log_ok "  created role \"$Q_WORKER\" (LOGIN)"
else
    run_admin "ALTER ROLE \"$Q_WORKER\" LOGIN PASSWORD '$WORKER_PW' NOSUPERUSER NOCREATEDB NOCREATEROLE"
    log_ok "  role \"$Q_WORKER\" present (LOGIN, password synced)"
fi

if ! role_exists "$Q_MESSAGING"; then
    run_admin "CREATE ROLE \"$Q_MESSAGING\" LOGIN PASSWORD '$MESSAGING_PW'"
    log_ok "  created role \"$Q_MESSAGING\" (LOGIN)"
else
    run_admin "ALTER ROLE \"$Q_MESSAGING\" LOGIN PASSWORD '$MESSAGING_PW' NOSUPERUSER NOCREATEDB NOCREATEROLE"
    log_ok "  role \"$Q_MESSAGING\" present (LOGIN, password synced)"
fi

# -----------------------------------------------------------------------------
# 4. Schema + default privileges
#
# Identical to services/postgres/init/03-schema.sql.
# -----------------------------------------------------------------------------
log_info "Creating schema 'witsaba' and default privileges..."

run_db "CREATE SCHEMA IF NOT EXISTS witsaba AUTHORIZATION \"$Q_ADMIN\""

run_db "GRANT USAGE ON SCHEMA witsaba TO \"$Q_WORKER\""
run_db "GRANT USAGE ON SCHEMA witsaba TO \"$Q_MESSAGING\""

# Default privileges only affect objects created *after* this point, which is
# why step 5 has to grant on the table explicitly.
run_db "ALTER DEFAULT PRIVILEGES FOR ROLE \"$Q_ADMIN\" IN SCHEMA witsaba
        GRANT INSERT, UPDATE, DELETE, SELECT ON TABLES TO \"$Q_WORKER\""
run_db "ALTER DEFAULT PRIVILEGES FOR ROLE \"$Q_ADMIN\" IN SCHEMA witsaba
        GRANT SELECT ON TABLES TO \"$Q_MESSAGING\""
run_db "ALTER DEFAULT PRIVILEGES FOR ROLE \"$Q_ADMIN\" IN SCHEMA witsaba
        GRANT USAGE, SELECT ON SEQUENCES TO \"$Q_WORKER\""
run_db "ALTER DEFAULT PRIVILEGES FOR ROLE \"$Q_ADMIN\" IN SCHEMA witsaba
        GRANT SELECT ON SEQUENCES TO \"$Q_MESSAGING\""

# Neither service role may create objects in the schema.
run_db "REVOKE CREATE ON SCHEMA witsaba FROM PUBLIC"
log_ok "Schema and default privileges ready"

# -----------------------------------------------------------------------------
# 5. witsaba.devices
#
# Shape matches the workers README contract exactly. The scanner refreshes
# name/fw/chip/last_source_ip/last_seen_at on every UPSERT and preserves
# first_seen_at across refreshes.
# -----------------------------------------------------------------------------
log_info "Creating table witsaba.devices..."

run_db "CREATE TABLE IF NOT EXISTS witsaba.devices (
            mac             TEXT        PRIMARY KEY,
            name            TEXT,
            fw              TEXT,
            chip            TEXT,
            last_source_ip  INET,
            first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
            last_seen_at    TIMESTAMPTZ NOT NULL
        )"

# GET /api/devices/active filters on last_seen_at within the last 60s. Without
# this index that is a sequential scan on every dashboard poll.
run_db "CREATE INDEX IF NOT EXISTS devices_last_seen_at_idx
             ON witsaba.devices (last_seen_at DESC)"

run_db "ALTER TABLE witsaba.devices OWNER TO \"$Q_ADMIN\""

# Mirrors the default privileges above, for this already-existing table.
run_db "GRANT SELECT, INSERT, UPDATE, DELETE ON witsaba.devices TO \"$Q_WORKER\""
run_db "GRANT SELECT ON witsaba.devices TO \"$Q_MESSAGING\""
log_ok "Table witsaba.devices ready"

# -----------------------------------------------------------------------------
# 6. Verify the real contract: each service role over TCP, with its password
# -----------------------------------------------------------------------------
log_info "Verifying \"$Q_WORKER\" can authenticate over TCP..."
PGPASSWORD="$WORKER_PW" "$PSQL" -h "$PG_HOST" -p "$PG_PORT" \
    -U "$Q_WORKER" -d "$PG_DATABASE" -v ON_ERROR_STOP=1 \
    -c "SELECT 1 AS ok" >/dev/null
log_ok "  \"$Q_WORKER\" authenticates (SELECT allowed)"

log_info "Verifying \"$Q_MESSAGING\" can read but not write..."
PGPASSWORD="$MESSAGING_PW" "$PSQL" -h "$PG_HOST" -p "$PG_PORT" \
    -U "$Q_MESSAGING" -d "$PG_DATABASE" -v ON_ERROR_STOP=1 \
    -c "SELECT count(*) AS devices FROM witsaba.devices" >/dev/null
log_ok "  \"$Q_MESSAGING\" can read witsaba.devices"

# A read-only role must be rejected on write. Expected to fail, so ON_ERROR_STOP
# is deliberately off and the exit status is inverted.
if PGPASSWORD="$MESSAGING_PW" "$PSQL" -h "$PG_HOST" -p "$PG_PORT" \
        -U "$Q_MESSAGING" -d "$PG_DATABASE" \
        -c "INSERT INTO witsaba.devices (mac, last_seen_at) VALUES ('00:00:00:00:00:00', now())" \
        >/dev/null 2>&1; then
    log_err "  \"$Q_MESSAGING\" was able to INSERT -- least-privilege model is broken"
    exit 1
fi
log_ok "  \"$Q_MESSAGING\" correctly denied INSERT"

# -----------------------------------------------------------------------------
# 7. Summary
# -----------------------------------------------------------------------------
echo ""
log_ok "Database bootstrap complete"
echo ""
echo "  database : $PG_DATABASE"
echo "  schema   : witsaba (owner \"$Q_ADMIN\")"
echo "  table    : witsaba.devices"
echo "  indexes  : devices_pkey, devices_last_seen_at_idx"
echo ""
printf "  %-20s %-10s %s\n" "role" "login" "privileges"
printf "  %-20s %-10s %s\n" "\"$Q_ADMIN\"" "no" "owns schema + tables"
printf "  %-20s %-10s %s\n" "\"$Q_WORKER\"" "yes" "SELECT, INSERT, UPDATE, DELETE"
printf "  %-20s %-10s %s\n" "\"$Q_MESSAGING\"" "yes" "SELECT only"
echo ""
log_warn "Passwords are still the defaults in $ENV_FILE"
log_warn "Change them there and re-run this script to rotate."
log_info ""
log_info "Next: ./10-build-go.sh"
