#!/bin/bash
#
# Bootstrap roles, schema, and grants for the witsaba Postgres database.
#
# This script is mounted at /docker-entrypoint-initdb.d/ in the postgres
# container. The official postgres entrypoint runs every *.sh in that
# directory once, in lexical order, only on a freshly initialized data
# directory (i.e. an empty or never-created $PGDATA volume). Re-running
# docker compose up against an existing volume will skip this entirely.
#
# It is idempotent at the role level via DO blocks: if pg-worker or
# pg-messaging-core already exist, we UPDATE their passwords instead of
# failing. Re-running it on a fresh DB is safe.
#
# Environment consumed (provided by docker-compose env:):
#   PG_ADMIN_DB               — database to connect to for DDL (defaults to POSTGRES_DB)
#   PG_WORKER_PASSWORD        — password for the pg-worker role
#   PG_MESSAGING_CORE_PASSWORD— password for the pg-messaging-core role
#
# We do NOT create pg-admin here: the official postgres image's entrypoint
# creates POSTGRES_USER as a superuser before running initdb hooks. In our
# compose file POSTGRES_USER=pg-admin, so by the time this script runs,
# pg-admin already exists with CREATEDB/CREATEROLE privileges inherited
# from the image's bootstrap.
#
# We do NOT create the witsaba schema's tables here: DDL on tables is
# versioned migrations, out of scope for this bootstrap. The integration
# test will create the schema's tables inside its own ephemeral schema.

set -euo pipefail

: "${PG_ADMIN_DB:=${POSTGRES_DB:-witsaba}}"
: "${PG_WORKER_PASSWORD:?PG_WORKER_PASSWORD must be set}"
: "${PG_MESSAGING_CORE_PASSWORD:?PG_MESSAGING_CORE_PASSWORD must be set}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# -v ON_ERROR_STOP=1 makes psql abort on the first error, so a partial
# bootstrap never leaves the database in an inconsistent state.
PSQL=(psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$PG_ADMIN_DB")

echo "[bootstrap] applying 02-roles.sql"
"${PSQL[@]}" -f "$SCRIPT_DIR/02-roles.sql" \
    -v "pg_worker_password=${PG_WORKER_PASSWORD}" \
    -v "pg_messaging_core_password=${PG_MESSAGING_CORE_PASSWORD}"

echo "[bootstrap] applying 03-schema.sql"
"${PSQL[@]}" -f "$SCRIPT_DIR/03-schema.sql"

echo "[bootstrap] done"
