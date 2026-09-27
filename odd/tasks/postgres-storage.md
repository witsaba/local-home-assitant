# Feature: Postgres storage with pgvector (Linux target, Docker Compose)

## Goal

Add a Postgres service to the existing Docker Compose stack so the workers
service can persist `DiscoveryEvent`s into a `witsaba.devices` table (MAC as PK,
idempotent UPSERT). The new database is reusable for future services, ships
with the `pgvector` extension available (no vector columns yet), and uses a
least-privilege role model:

- One admin role (`pg-admin`) — owner of the `witsaba` schema, can DDL and GRANT.
  **Not** `postgres`.
- Two service roles (`pg-worker`, `pg-messaging-core`) — one per service, no
  DDL, only the DML they need.
- All services `depends_on: postgres: { condition: service_healthy }` so they
  wait for a ready database.

Validated against the same Linux target the existing compose file targets
(network_mode: host, LAN discovery).

## Design decisions (locked with user)

| #  | Decision                          | Value                                                                                                       |
|----|-----------------------------------|-------------------------------------------------------------------------------------------------------------|
| 1  | Postgres image                    | `pgvector/pgvector:pg16-trixie` (Debian 13, pgvector preinstalled, official maintainer). The `-slim` tag assumed in the original plan does not exist in the pgvector Docker Hub repo; `pg16-trixie` is the actual lightest official pgvector build. |
| 2  | Schema                            | Single shared schema `witsaba`. All roles operate on tables inside it.                                      |
| 3  | Roles                             | `pg-admin` (DDL/owner), `pg-worker` (DML on `devices`), `pg-messaging-core` (SELECT on `devices`)           |
| 4  | `witsaba.devices` table           | PK `mac TEXT`; cols `name TEXT`, `fw TEXT`, `chip TEXT`, `last_source_ip INET`, `first_seen_at TIMESTAMPTZ`, `last_seen_at TIMESTAMPTZ` |
| 5  | Persist semantics                 | `INSERT ... ON CONFLICT (mac) DO UPDATE` — idempotent UPSERT, refreshes `last_seen_at`, `last_source_ip`, `name`, `fw`, `chip`. `first_seen_at` is set on insert only. |
| 6  | Driver (Go)                       | `github.com/jackc/pgx/v5` + `pgxpool`. Pure-Go, supports pgvector, no libpq dependency.                    |
| 7  | Persistence                       | Named volume `witsaba-postgres-data` mounted on `/var/lib/postgresql/data`.                                 |
| 8  | Healthcheck                       | `pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"`, `interval: 5s`, `retries: 10`, `start_period: 10s`       |
| 9  | depends_on                        | `messaging-core` and `workers` both declare `depends_on: postgres: { condition: service_healthy }`          |
| 10 | Bootstrap                         | `services/postgres/init/01-bootstrap.sql` mounted at `/docker-entrypoint-initdb.d/`. Creates roles + schema + GRANTs. **Does not** create tables. |
| 11 | Tables DDL                        | Out of scope for this feature. Follow-up: versioned migrations (golang-migrate or similar).                |
| 12 | Connectivity (compose)            | Services connect via `127.0.0.1:5432`. `network_mode: host` means loopback is the host loopback. `extra_hosts` is NOT used for Postgres (no DNS indirection needed). |
| 13 | Env overrides                     | `.env` next to compose; `env.example` documents every new var. Defaults baked into compose with `${VAR:-default}` for booleans/hosts only — passwords have NO defaults and must be set. |
| 14 | Env vars (new)                    | `POSTGRES_DB=witsaba`, `POSTGRES_USER=pg-admin`, `POSTGRES_PASSWORD`, `POSTGRES_HOST=127.0.0.1`, `POSTGRES_PORT=5432`, `PG_WORKER_PASSWORD`, `PG_MESSAGING_CORE_PASSWORD`. Service-side: `PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USER`, `PG_PASSWORD`. |
| 15 | Cleanup                           | Delete empty `services/service_a/` placeholder.                                                            |
| 16 | Tests                             | Unit tests for the new repository (with a fake `pgxpool`-style interface) **plus** one integration test gated by `INTEGRATION=postgres` using a live Postgres (testcontainers-go or local). Default `go test ./...` stays hermetic. |

## Rationale for image choice

User asked for "minimum Postgres version that supports pgvector but light".

- pgvector requires Postgres ≥ 12. PG 16 is the current widely-deployed stable
  release with full pgvector 0.7+ support.
- `pgvector/pgvector:pg16-trixie` is the official pgvector-maintained image
  on Debian 13 (trixie). It ships pgvector preinstalled and avoids musl ABI
  quirks. The original plan assumed a `-slim` tag that does not exist in the
  pgvector Docker Hub repo; the user accepted `pg16-trixie` as the lightest
  available official build (Debian 13, ~155 MB compressed).
- Alpine was rejected: pgvector does not ship official Alpine builds; forcing
  a build-from-source layer adds maintenance cost for marginal savings.

## Rationale for role model

- Single shared `witsaba` schema chosen over per-service schemas to keep
  early-stage inter-service joins cheap. Each service still authenticates as
  its own role with the least privilege it needs.
- `pg-admin` exists so application connections never touch `postgres`. All
  future DDL/GRANTs run as `pg-admin`. Connections from services use
  `pg-worker` / `pg-messaging-core` only.
- `pg-worker` gets `INSERT`, `UPDATE`, `SELECT` on `witsaba.devices`.
  `pg-messaging-core` gets `SELECT` on `witsaba.devices`. No DDL, no DML
  beyond that. New tables will require explicit GRANTs from `pg-admin`.

## Rationale for connectivity (127.0.0.1, no extra_hosts)

The stack uses `network_mode: host` on every service. Under that mode:
- Docker service-name DNS is disabled.
- `127.0.0.1` inside the container == `127.0.0.1` on the host loopback.
- Postgres binds to `127.0.0.1:5432` inside its own container, and the
  host-loopback reaches it because every container shares the host network.
- No `extra_hosts` entry is required for Postgres the way it is for
  `messaging-core`, because the host we want is already literal `127.0.0.1`.

NATS uses `extra_hosts` only because the embedded NATS server is configured
to bind on a hostname (`messaging-core`), not because of DNS — see
`docker-compose.yml` header comment. That asymmetry is preserved.

## Public surface

No new Go types in `messaging-core`. New surface:

```
docker-compose.yml                              ← add postgres service, healthcheck, depends_on everywhere
.env.example                                    ← document new env vars
services/postgres/init/01-bootstrap.sql          ← roles + schema + grants (initdb-only)
services/postgres/README.md                     ← compose usage, init semantics, rotation notes
services/workers/go.mod                         ← + github.com/jackc/pgx/v5
services/workers/internal/infrastructure/config ← + PG_HOST, PG_PORT, PG_DATABASE, PG_USER, PG_PASSWORD
services/workers/internal/infrastructure/devices← NEW package: repository interface + pgx impl + noop for tests
services/workers/internal/jobs/discovery/consumer.go  ← call repository.Upsert instead of log
services/workers/cmd/workers/main.go            ← wire repository
services/workers/README.md                      ← Postgres section, env vars
services/messaging-core/internal/infrastructure/config ← + PG_* (read-only connection; not consumed yet)
services/messaging-core/README.md               ← note on pg-messaging-core role
services/service_a/                             ← DELETE (empty placeholder)
odd/tasks/postgres-storage.md                   ← this file
```

## Tasks (one work-unit commit each)

- [ ] 1. Plan file committed (`odd/tasks/postgres-storage.md`)
- [ ] 2. `chore(repo): remove empty services/service_a placeholder`
- [ ] 3. `feat(postgres): bootstrap SQL (pg-admin role, schema, GRANTs)`
- [ ] 4. `feat(compose): add postgres service with pg_isready healthcheck and named volume`
- [ ] 5. `docs(env): document Postgres credentials in env.example`
- [ ] 6. `feat(compose): wire messaging-core and workers to depends_on postgres healthy`
- [ ] 7. `feat(workers): add pgx v5 dependency and devices repository skeleton (interface + noop)`
- [ ] 8. `feat(workers): pgx-backed repository implementation + unit tests`
- [ ] 9. `feat(workers): PG_* env config in config.Load + validation`
- [ ] 10. `feat(workers): wire repository in main.go (pgxpool from cfg)`
- [ ] 11. `refactor(workers): discovery consumer calls repository.Upsert`
- [ ] 12. `test(workers): integration test gated by INTEGRATION=postgres`
- [ ] 13. `docs(postgres): services/postgres/README.md (compose, init semantics, asymmetry vs NATS)`
- [ ] 14. `docs(workers): README — Postgres section, env vars consumed`
- [ ] 15. `docs(messaging-core): README — pg-messaging-core role, env vars consumed`
- [ ] 16. `chore(compose): validate with docker compose config -q`

## Out of scope (follow-ups)

- Versioned migrations for `devices` (and future tables). Today the table is
  not created by the bootstrap; the worker would UPSERT into nothing yet —
  the integration test will create the table inside its own schema. Production
  schema is created by a follow-up migration feature.
- pgvector usage (no vector columns in `devices` yet — image ships the
  extension for future use).
- TLS / SCRAM auth between services and Postgres (startup defaults are fine
  on host loopback; revisit if Postgres is ever moved off-host).
- Backups (`pg_dump`), rotation, point-in-time recovery.
- `messaging-core` actually reading from `devices`. Today it only gains the
  role and config; consumers are a follow-up.

## Acceptance criteria

- `docker compose config -q` exits 0.
- `docker compose build` succeeds for every service (postgres is a pull, not a build).
- On a Linux target, `docker compose up -d` brings up Postgres healthy, then
  messaging-core and workers. The workers container logs `witsaba device found`
  and, for the camera at `192.168.1.51`, an `UPSERT` succeeds into
  `witsaba.devices` (verifiable via `docker compose exec postgres psql ...`).
- `psql -U pg-worker -d witsaba -c "INSERT INTO witsaba.devices ..."` works.
  `psql -U pg-messaging-core -d witsaba -c "INSERT INTO witsaba.devices ..."`
  fails with `permission denied for table devices`.
- `psql -U pg-messaging-core -d witsaba -c "SELECT mac FROM witsaba.devices LIMIT 1;"` works.
- `.env.example` documents every new var; `.env` is git-ignored.
- `go test ./...` in `services/workers` is hermetic by default.
- Every work-unit commit is a single Conventional Commit scoped to one logical
  change. After every commit, the worktree `git status` is clean.
- Linux-host validation cannot run on this Mac dev machine — explicitly noted in
  `services/postgres/README.md`.

## Tracking

- Worktree: `~/workspace/witsaba/repositories/local-home-assitant-worktrees/postgres-storage/`
- Branch: `feat/postgres-storage`
- odd/tasks file: `odd/tasks/postgres-storage.md` (this file)
- Visible `todo` list mirrors the task checklist above

## Closed-by commits

(populated as each work-unit commit is made)

| #  | SHA  | Subject |
|----|------|---------|
| 1  | TBD  | docs(plan): postgres storage with pgvector (linux target, docker compose) |