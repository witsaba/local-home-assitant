# `postgres`

> Shared Postgres database for the witsaba `local-home-assistant`
> stack. Hosts three roles (`pg-admin`, `pg-worker`,
> `pg-messaging-core`), the `witsaba` schema, and the future
> `devices` table. Ships with the `pgvector` extension preinstalled
> for vector-embedding follow-ups.

The container is **only defined by `docker-compose.yml` at the repo
root** — there is no Go service, no Dockerfile of our own, and no
build context. This directory exists for:

  - `init/` — the SQL bootstrapped into `/docker-entrypoint-initdb.d/`
    when the data volume is empty.
  - This README — the operational contract every operator needs.

---

## Quick start

The postgres container is part of the top-level compose stack:

```bash
# from repo root
cp env.example .env          # edit passwords before any real deploy
docker compose up -d postgres

# Confirm it is healthy (pg_isready exits 0)
docker compose exec postgres pg_isready -h 127.0.0.1 -U pg-admin -d witsaba
```

`docker compose up -d` brings up everything; the messaging-core and
workers services both `depends_on: postgres: { condition: service_healthy }`
so they only start after Postgres has accepted a connection.

> **Linux-only target.** The compose stack uses `network_mode: host`
> on every service. Docker Desktop (Mac/Windows) puts host-mode
> containers on the VM's network, so the binding to the host
> loopback does not work as expected on this Mac dev machine. Run
> the stack on a real Linux host.

---

## Roles

Three roles exist. None of them is the `postgres` superuser — that
account is locked to the container's bootstrap and never handed to
an application.

| Role                 | Created by                | Used by                              | Privileges on `witsaba.devices` |
| -------------------- | ------------------------- | ------------------------------------ | ------------------------------- |
| `pg-admin`           | `POSTGRES_USER` env       | Migrations, DDL, GRANTs (manual)     | Owner of the schema.            |
| `pg-worker`          | `02-roles.sql`            | `services/workers` (UPSERT)          | `INSERT`, `UPDATE`, `DELETE`, `SELECT` |
| `pg-messaging-core`  | `02-roles.sql`            | `services/messaging-core` (future SELECT) | `SELECT` only             |

All three roles are `LOGIN`-able (so services can connect) but have
no `CREATEDB`, no `CREATEROLE`, and no `SUPERUSER`. The
`ALTER DEFAULT PRIVILEGES` in `03-schema.sql` means any **future**
table created by `pg-admin` in `witsaba` automatically inherits
the right GRANTs — no need to re-run the bootstrap scripts.

---

## init scripts: how and when they run

The `init/` directory is mounted at
`/docker-entrypoint-initdb.d/` (read-only). The official Postgres
entrypoint runs every `*.sh` there **exactly once**, in lexical
order, **only when the data directory is freshly initialized**:

```
01-bootstrap.sh    # orchestrator: runs the SQL files via psql -v
02-roles.sql       # idempotent CREATE ROLE for pg-worker and
                   # pg-messaging-core
03-schema.sql      # CREATE SCHEMA witsaba + ALTER DEFAULT PRIVILEGES
```

Implications:

  - The scripts **do not** run on `docker compose up` against an
    already-initialized volume. They run on the very first `up`
    that creates `witsaba-postgres-data`.
  - To re-bootstrap (e.g. after rotating the admin password), run
    `docker compose down -v` and `docker compose up -d` again.
    **This destroys the data directory.**
  - To rotate a service role's password non-destructively, edit
    `PG_WORKER_PASSWORD` / `PG_MESSAGING_CORE_PASSWORD` in `.env`
    and run `docker compose restart postgres`. The roles are
    re-`ALTER`-ed to the new passwords on the **next** init, so a
    non-destructive rotation requires a one-off `psql -c "ALTER
    ROLE \"pg-worker\" PASSWORD '...'"` against the running
    container. A follow-up feature will formalize rotation.

---

## Connectivity

Postgres binds to `127.0.0.1:5432` only:

  - `command: postgres -c listen_addresses=127.0.0.1` in
    `docker-compose.yml` keeps it on the host loopback.
  - Under `network_mode: host` the container shares the host
    network stack, so binding to `0.0.0.0` would expose 5432 to
    the LAN. Loopback-only binding is enforced via GUC, not via
    firewall rules.
  - Other compose services reach it via `127.0.0.1:5432` directly
    (their host loopback). No `extra_hosts` entry is needed
    because the target host is already a literal IP, unlike the
    NATS hostname (`messaging-core`) that needs to be mapped via
    `/etc/hosts`.

This is the asymmetry vs `messaging-core` that the top-level
`docker-compose.yml` header explains. Both services use
`network_mode: host` for the same reason (LAN discovery), but
only NATS needs hostname indirection; Postgres does not.

---

## Environment variables

See `../env.example` at the repo root for the authoritative list.
Quick reference:

| Variable                     | Default                  | Required | Notes                                       |
| ---------------------------- | ------------------------ | -------- | ------------------------------------------- |
| `POSTGRES_DB`                | `witsaba`                | no       | Database name created on first init.        |
| `POSTGRES_USER`              | `pg-admin`               | no       | Owner of the schema. Used for migrations.   |
| `POSTGRES_PASSWORD`          | (none)                   | **yes**  | Compose refuses to start without it.        |
| `POSTGRES_PORT`              | `5432`                   | no       | Loopback only.                              |
| `PG_WORKER_PASSWORD`         | (none)                   | **yes**  | Consumed by `01-bootstrap.sh`.              |
| `PG_MESSAGING_CORE_PASSWORD` | (none)                   | **yes**  | Consumed by `01-bootstrap.sh`.              |

Passwords are deliberately without defaults so an accidental
`docker compose up` with a partial `.env` fails fast with a clear
compose error.

---

## Tables: out of scope for this feature

The bootstrap creates the **schema** (`witsaba`) and the
**GRANTs**, but it does not create the `devices` table. Table DDL
is a versioned-migration concern owned by a follow-up feature
(probably `golang-migrate` or equivalent). The integration test in
`services/workers/internal/infrastructure/devices/integration_test.go`
provisions the table inside an ephemeral container with the exact
shape the production migration will land with:

```sql
CREATE TABLE witsaba.devices (
    mac            TEXT        PRIMARY KEY,
    name           TEXT,
    fw             TEXT,
    chip           TEXT,
    last_source_ip INET,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL
);
```

When the migration feature lands, that schema is the source of
truth and the integration test's inline `CREATE TABLE` should be
removed in favor of running the same migration.

---

## Operations cheat sheet

```bash
# Tail logs (init output shows up here on first boot)
docker compose logs -f postgres

# Open a psql shell as pg-admin
docker compose exec postgres psql -U pg-admin -d witsaba

# Open a psql shell as pg-worker (verifies the GRANTs)
docker compose exec -e PGPASSWORD="$PG_WORKER_PASSWORD" postgres \
  psql -U pg-worker -d witsaba

# Inside psql, verify role permissions:
#   pg-worker should succeed:
INSERT INTO witsaba.devices (mac, last_seen_at)
  VALUES ('aa:bb:cc:dd:ee:ff', now());
#   pg-messaging-core should FAIL:
INSERT INTO witsaba.devices (mac, last_seen_at)
  VALUES ('11:22:33:44:55:66', now());
#   pg-messaging-core should succeed:
SELECT mac FROM witsaba.devices LIMIT 1;

# Re-bootstrap (DESTRUCTIVE: destroys the data volume)
docker compose down -v
docker compose up -d

# Healthcheck from the host (used by docker compose ps)
docker compose exec postgres pg_isready -h 127.0.0.1 -U pg-admin -d witsaba
```

---

## Image choice rationale

The image is `pgvector/pgvector:pg16-trixie` (Debian 13, pgvector
preinstalled by the pgvector maintainer). The plan originally
specified a `pg16-trixie-slim` tag that does not exist on Docker
Hub; `pg16-trixie` is the actual lightest official build.
`pgvector` is preinstalled but unused by this feature — it is in
the image so the next follow-up (vector embeddings of device
metadata, or image features) does not require an image swap.

Alternatives considered and rejected:

  - **`postgres:16-trixie` + pgvector install script** — adds an
    initdb script we have to maintain; no real weight saving.
  - **`postgres:16-alpine` + pgvector source build** — pgvector
    has no official Alpine build; compiling against musl adds
    maintenance and slow first-boot cost for marginal size.
