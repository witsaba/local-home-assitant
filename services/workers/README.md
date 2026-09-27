# `workers`

> Go service that hosts **periodic background jobs**. Hexagonal
> layout, zap logging with the OTel bridge. First job: `discovery`
> — a network scanner that finds witsaba devices on the local
> LAN by probing `GET /whoami`.

This is the home for *all* future periodic workers in the
`local-home-assitant` monorepo — schedulers, ingestion jobs,
control-plane crons, etc. New workers are added as packages under
`internal/jobs/<name>/` and registered in `cmd/workers/main.go`.

---

## Quick start

```bash
# Show every developer target this repo exposes
make help

# Build + run with defaults: scan every minute, pool of 64, log info
make run

# Or invoke directly
go build -o bin/workers ./cmd/workers
./bin/workers

# Override via env vars
DISCOVERY_INTERVAL_SECONDS=30 \
DISCOVERY_WORKER_POOL_SIZE=128 \
DISCOVERY_PROBE_TIMEOUT_MS=2000 \
LOG_LEVEL=debug \
./bin/workers
```

The binary logs structured JSON to **stderr** (not stdout — stdout
is block-buffered and can drop logs on SIGTERM). A clean run
looks like:

```json
{"level":"info","ts":"2026-09-27T11:01:00Z","msg":"workers starting","version":"0.1.0-dev-gitsha","log_level":"info","discovery_interval_s":60,"discovery_pool_size":64,"discovery_probe_timeout_ms":1500}
{"level":"info","ts":"2026-09-27T11:01:00Z","msg":"scheduler running","job_count":1}
{"level":"info","ts":"2026-09-27T11:01:00Z","msg":"job started","job":"discovery"}
... (every 60s) ...
{"level":"info","ts":"2026-09-27T11:02:00Z","msg":"scan complete","devices_found":2}
{"level":"info","ts":"2026-09-27T11:02:00Z","msg":"witsaba device found","source_ip":"192.168.1.42","name":"cam-front-door","mac":"aabbccddeeff","fw":"v1.2.3","chip":"ESP32-S3","discovered_at":"2026-09-27T11:02:00.123Z"}
```

Stop with `Ctrl-C` / `SIGTERM`. Shutdown drains pending events,
then exits with code `0`.

---

## Configuration

All configuration comes from environment variables. Defaults make
the service run on a developer laptop with zero setup.

| Variable                      | Default     | Notes                                                  |
| ----------------------------- | ----------- | ------------------------------------------------------ |
| `DISCOVERY_INTERVAL_SECONDS`  | `60`        | How often the discovery job fires. Must be > 0.        |
| `DISCOVERY_PROBE_TIMEOUT_MS`  | `1500`      | Per-IP `GET /whoami` timeout. Must be > 0.             |
| `DISCOVERY_WORKER_POOL_SIZE`  | `64`        | Max concurrent probe goroutines per scan. Must be > 0. |
| `LOG_LEVEL`                   | `info`      | `debug`, `info`, `warn`, `error` (case-insensitive).   |
| `PG_HOST`                     | `127.0.0.1` | Hostname of the Postgres container. Must be non-empty. |
| `PG_PORT`                     | `5432`      | Postgres TCP port. Must be in `[1, 65535]`.            |
| `PG_DATABASE`                 | `witsaba`   | Database name. Must be non-empty.                      |
| `PG_USER`                     | `pg-worker` | Role used by the workers service. Must be non-empty.  |
| `PG_PASSWORD`                 | (none)      | Password for the role above. Must be non-empty.       |

Invalid values (interval ≤ 0, unknown log level, empty PG_*,
PG_PORT out of range, etc.) cause the process to exit with code
`2` before the scheduler starts.

---

## Postgres

The workers service connects to the shared `witsaba` Postgres
container defined at the repo root. Connection targets are read
from `PG_*` env vars (see the table above); defaults match the
top-level `docker-compose.yml` `PG_*` block, which itself defaults
`PG_PASSWORD` to `PG_WORKER_PASSWORD` so operators only define one
password per role.

### Role

This service authenticates as `pg-worker`. The role is created by
`services/postgres/init/02-roles.sql` on the first init of the
postgres data volume. Its GRANTs come from
`services/postgres/init/03-schema.sql`:

  - `INSERT`, `UPDATE`, `DELETE`, `SELECT` on every table owned by
    `pg-admin` in `witsaba`.
  - `USAGE` on the `witsaba` schema itself.
  - No DDL, no `CREATEDB`, no `CREATEROLE`, no `SUPERUSER`.

`pg-messaging-core` is the sibling role for `services/messaging-core`
and gets `SELECT` only.

### Startup behavior

`main.go` opens a `pgxpool.Pool` early in startup and `Ping`s it
with a 5s timeout:

  - On success, the pool is handed to `devices.NewPgx(pool, pool)`
    and the consumer drains events into it.
  - On failure (DB unreachable, bad credentials, schema missing),
    the process logs the connection error with the host/port/user
    context and exits with code `2`.

This means **workers refuses to run without Postgres**, which is the
intended behavior: the discovery job's only purpose is to feed the
devices table.

### `witsaba.devices` shape

```
mac            TEXT        PRIMARY KEY
name           TEXT
fw             TEXT
chip           TEXT
last_source_ip INET
first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
last_seen_at   TIMESTAMPTZ NOT NULL
```

Every UPSERT refreshes `name`, `fw`, `chip`, `last_source_ip`, and
`last_seen_at`; `first_seen_at` is preserved across refreshes
(insert-only). The repository emits a single SQL statement per
event — no `BEGIN`/`COMMIT` overhead and no N+1 risk.

### Verifying the connection from outside

```bash
# From the compose stack
docker compose exec postgres \
  pg_isready -h 127.0.0.1 -U pg-admin -d witsaba

# Insert as pg-worker (should succeed)
docker compose exec -e PGPASSWORD="$PG_WORKER_PASSWORD" postgres \
  psql -U pg-worker -d witsaba \
  -c "INSERT INTO witsaba.devices (mac, last_seen_at) VALUES ('aa:bb:cc:dd:ee:ff', now());"

# Read as pg-worker (should succeed)
docker compose exec -e PGPASSWORD="$PG_WORKER_PASSWORD" postgres \
  psql -U pg-worker -d witsaba \
  -c "SELECT mac, name, fw FROM witsaba.devices LIMIT 10;"
```

See `services/postgres/README.md` for the full operational
contract (init semantics, asymmetry vs NATS, image choice).

---

## Architecture

```
cmd/workers/main.go                     ← composition root, signal handling, events channel
└── internal/
    ├── types/                          ← cross-package domain types (DiscoveryEvent)
    ├── worker/
    │   ├── job.go                      ← Job interface (Name, Interval, Run)
    │   └── scheduler.go                ← ticker + panic recovery + ctx propagation
    └── infrastructure/
        ├── config/                     ← env-var loader (PG_* too)
        ├── devices/                    ← devices.Repository (Noop + pgx.Upsert)
        ├── logger/                     ← zap + otelzap bridge
        ├── routetable/                 ← platform-specific subnet discovery (Linux / Darwin)
        └── probe/                      ← /whoami probe with X-Witsaba-Device filter

    internal/jobs/discovery/            ← the FIRST worker (plug-in job package)
    ├── discovery.go                    ← Job impl: enumerate subnets → CIDR expand → pool → emit
    ├── consumer.go                     ← drains the events channel, persists + logs each hit
    └── types.go                        ← type-alias back into internal/types
```

**Adding a new worker** is a 3-step recipe:

1. Create `internal/jobs/<name>/<name>.go` with a type that
   implements `worker.Job` (`Name`, `Interval`, `Run`).
2. Add the constructor to `cmd/workers/main.go` next to
   `discoveryJob` and append it to the `[]worker.Job{...}` slice.
3. Optionally add tests under `internal/jobs/<name>/`.

The discovery job's events channel and consumer live in main.go;
new workers can share that channel by emitting matching event
types, or main.go can own separate channels per worker family.

---

## How `discovery` works

```
+--------------------+     +-----------------------+     +------------------+
|   routetable       |     |  discovery job        |     |  consumer        |
| DiscoverAttached   | ──► | expand CIDR           | ──► | drain events     |
| Subnets()          |     | spawn N probe workers |     | log hits at INFO |
| (Linux/Darwin)     |     | (semaphore, ctx)      |     | via zap          |
+--------------------+     +----------+------------+     +------------------+
                                       │ hit (event)
                                       ▼
                              +------------------+
                              |  main-owned      |
                              |  events channel  |
                              |  (buffer 256)    |
                              +------------------+
```

Filter logic — the only thing keeping this from being a port
scanner is the **`X-Witsaba-Device: true` HTTP response header**
that every witsaba firmware emits. Starlink's `/whoami`, or any
other random device that happens to answer on port 80, will not
have this header and is silently dropped at the probe layer.

### Persistence

Every matched event reaches the `Consumer`, which:

1. Calls `repository.Upsert(ctx, event)` against the
   `pg-worker` role in the `witsaba.devices` table.
2. Logs the hit at INFO with `upsert_ok=true`, OR at WARN with
   `upsert_ok=false` and the driver error attached.

The consumer is **resilient**: an Upsert failure does not stop the
loop. The channel is still drained, the scheduler still ticks, and
the next event gets another chance. Repeated failures show up in
the log stream and the operator can react.

See the [Postgres section](#postgres) below for the role model,
table shape, and how to verify the GRANTs from a shell.

---

## Verification

```bash
# Show every developer target
make help

# All tests across config, logger, probe, routetable, worker, discovery
make test

# Per-package
go test ./internal/infrastructure/probe/... -v

# Coverage
make cover

# Cross-build (Linux target)
GOOS=linux GOARCH=amd64 go build ./...

# Lint (requires golangci-lint v2)
make lint
```

A few of the interesting tests, in plain English:

| Test                                                | What it proves                                                       |
| --------------------------------------------------- | -------------------------------------------------------------------- |
| `probe.TestProbeWhoami_HeaderTrueMatch`             | A device that emits `X-Witsaba-Device: true` is reported as a match. |
| `probe.TestProbeWhoami_HeaderAbsent`                | Starlink-like response (no header) → no match, no error.             |
| `probe.TestProbeWhoami_HeaderFalseValue`            | Header present but `false` → no match, no error.                     |
| `routetable.TestParseRouteTable_*`                  | Linux `/proc/net/route` parses default-route + multi-route + bad.    |
| `routetable.TestParseRouteOutput_*`                 | Darwin `route -n get default` parses gateway + interface.            |
| `discovery.TestDiscovery_HitEvents`                 | Two hosts in a /30, both witsaba → exactly 2 events emitted.         |
| `discovery.TestDiscovery_MissPlainHost`             | Two hosts, neither witsaba → zero events.                            |
| `discovery.TestDiscovery_CtxCancelled`              | Pre-cancelled ctx → returns `context.Canceled`.                      |
| `discovery.TestDiscovery_NoTargets`                 | /31 (no usable hosts) → zero events, no crash.                       |
| `worker.TestScheduler_PanicRecovered`               | Panicking job does not stop the scheduler.                           |
| `worker.TestScheduler_TickFires`                    | 15 ms interval → at least 2 ticks in 50 ms.                          |
| `consumer.TestConsumer_LogsEvents`                  | 3 known events → exactly 3 zap records with expected fields.         |

---

## File-by-file map

| Path                                                            | Purpose                                                              |
| --------------------------------------------------------------- | -------------------------------------------------------------------- |
| `cmd/workers/main.go`                                           | Composition root. Loads config, builds logger, owns events channel, wires scheduler + consumer + signals. |
| `internal/types/discovery.go`                                   | `DiscoveryEvent` domain type (cross-package).                        |
| `internal/worker/job.go`                                        | `Job` interface + tiny compile-check test.                           |
| `internal/worker/scheduler.go`                                  | Ticker + per-job goroutine + panic recovery + `Done()` channel.      |
| `internal/infrastructure/config/config.go`                      | `Load()` reads env vars with defaults, validates.                    |
| `internal/infrastructure/logger/logger.go`                      | zap JSON encoder → stderr, bridged to OTel via otelzap (noop).       |
| `internal/infrastructure/routetable/routetable.go`              | `Subnet` type + package doc.                                         |
| `internal/infrastructure/routetable/routetable_linux.go`        | `/proc/net/route` + `net.Interfaces()`.                              |
| `internal/infrastructure/routetable/routetable_darwin.go`       | `route -n get default` + `net.Interfaces()`.                         |
| `internal/infrastructure/routetable/routetable_unsupported.go`  | `ErrUnsupportedPlatform` for other OSes.                             |
| `internal/infrastructure/probe/whoami.go`                        | `GET /whoami` probe, `X-Witsaba-Device` filter.                      |
| `internal/infrastructure/devices/devices.go`                     | `Repository` interface (`Upsert`, `Close`) used by the consumer.    |
| `internal/infrastructure/devices/noop.go`                        | `Noop` reference implementation: logs and returns nil.              |
| `internal/infrastructure/devices/store.go`                       | `Pgx` implementation: single-statement UPSERT into `witsaba.devices`.|
| `internal/infrastructure/devices/integration_test.go`            | Integration tests gated by `-tags=integration` + `INTEGRATION=postgres`. |
| `internal/jobs/discovery/discovery.go`                          | `Job` impl: subnets → CIDR → worker pool → emit.                     |
| `internal/jobs/discovery/consumer.go`                           | Drains the events channel, persists + logs each hit.                |
| `Makefile`                                                      | Developer targets (`make help` for the list).                        |
| `.golangci.yaml`                                                | Minimal `golangci-lint v2` config used by `make lint`.               |

---

## Follow-ups (intentionally out of scope for this branch)

* **iot_cams firmware patch** — emit `X-Witsaba-Device: true`
  on every `GET /whoami` response. Until that lands on every
  deployed device, `discovery` will find zero witsaba devices
  (expected). Tracked in a separate firmware PR.
* **Versioned schema migrations** — the `witsaba.devices` table
  is currently created inline by the integration test. A real
  migration tool (golang-migrate or equivalent) is the next
  follow-up so production boots land the same DDL the test
  verifies.
* **`/healthz` endpoint** — no HTTP surface today; a future
  liveness probe would go in `cmd/workers/health.go` alongside
  the existing `cmd/workers/main.go`.
* **IPv6 subnet enumeration** — `routetable` and `discovery`
  are IPv4-only. Add `net.IP.To16()` paths and CIDR expansion
  for v6 when needed.
* **mDNS / DNS-SD probe** — `/whoami` over unicast HTTP catches
  devices that are already on the LAN but joined silently.
  mDNS would catch devices that haven't yet been admitted.
* **Shared scaffolding package** — if a third worker lands and
  the boilerplate (subnet enumeration, event channel, etc.)
  starts to duplicate, extract `pkg/worker/` as an importable
  helper and import it from each `internal/jobs/<name>/`.
* **Sub-net scan budget / adaptive pool sizing** — for big
  subnets the current cap of 64 concurrent probes may not be
  enough. Add adaptive sizing based on CIDR size.
* **Auth on `/whoami`** — currently trusted by virtue of being
  on the home LAN. Add a shared-secret header before exposing
  this worker to anything but trusted networks.

---

## Docker

Linux only — see "LAN discovery" at the bottom of this section.

### Build & run via compose (from repo root)

```bash
docker compose up -d --build      # build + start both services
docker compose logs -f workers    # tail the worker
docker compose down               # stop and remove
```

The compose file pins `network_mode: host` so the worker sees the
host's interfaces and routing table. The discovery job enumerates
subnets from `/proc/net/route` (Linux) inside the container — with
host networking that is the **host's** route table, so the scan
covers the real LAN (e.g. `192.168.1.0/24`).

### Build the image standalone

```bash
# Default version stamp (0.1.0-dev-unknown)
docker build -t witsaba/workers:local \
  -f services/workers/Dockerfile \
  services/workers

# Stamped from the current commit
docker build \
  --build-arg VERSION=0.1.0 \
  --build-arg GIT_HEAD=$(git rev-parse --short HEAD) \
  -t witsaba/workers:dev \
  -f services/workers/Dockerfile \
  services/workers
```

### Image details

- Build stage: `golang:1.26.3-alpine3.23` (~66 MB compressed).
- Runtime stage: `alpine:3.23` + `ca-certificates`.
- Static binary (`CGO_ENABLED=0`), cross-compiled to `linux`.
- Runs as `nobody` (uid 65534).
- No listening port in v1 (discovery is outbound only).

### Env configuration in container

All env vars from the table at the top of this README are wired
through the project-level `docker-compose.yml`. Override any of
them in `.env`:

```env
DISCOVERY_INTERVAL_SECONDS=30
DISCOVERY_PROBE_TIMEOUT_MS=2000
DISCOVERY_WORKER_POOL_SIZE=128
LOG_LEVEL=debug

# Postgres (compose defaults PG_PASSWORD to PG_WORKER_PASSWORD,
# so usually only the *_PASSWORD values need to be set)
PG_WORKER_PASSWORD=change-me-worker
POSTGRES_PASSWORD=change-me-admin
```

The compose file also forwards the Postgres connection block to
this service (`PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USER`,
`PG_PASSWORD`). The default `PG_PASSWORD` falls back to
`PG_WORKER_PASSWORD` so operators only set one value per role.

`NATS_HOST` and `NATS_PORT` are also forwarded for future NATS
clients (the v1 worker does not consume them yet). The default
`NATS_HOST=messaging-core` resolves via the `extra_hosts` entry
the compose file injects into this container's `/etc/hosts`,
mapping `messaging-core` to `127.0.0.1` (the host loopback, where
the embedded NATS server is bound under `network_mode: host`).

The compose file declares `depends_on: postgres: { condition:
service_healthy }` so this service only starts after Postgres has
accepted a connection on `127.0.0.1:5432`.

### LAN discovery — Linux-only caveat

This is the critical constraint: **`network_mode: host` on Docker
Desktop (Mac/Windows) puts the container on the VM's network
namespace, NOT your LAN**. On this Mac dev machine the worker
container sees only `127.0.0.0/8` and cannot reach `192.168.1.51`.
The discovery log will show `devices_found: 0` here.

To validate end-to-end discovery (worker → `GET /whoami` against
your witsaba cameras), run on a **real Linux host** — Linux server,
Raspberry Pi, NAS — where the host's routing table includes your
LAN interface. On that host:

```bash
docker compose up -d --build
docker compose logs -f workers
```

You should see:

```json
{"level":"info","msg":"witsaba device found","source_ip":"192.168.1.51","name":"iot-cam","mac":"...","fw":"...","chip":"..."}
```

Mac dev support via `ipvlan` / `macvlan` (so the worker container
sits directly on your Mac's LAN) is tracked separately and not in
this branch.

Cross-platform builds: see `docker buildx build --platform
linux/amd64 ...` (messaging-core README has the full example).
---

## Versioning

`var version` in `cmd/workers/main.go` is the build identifier.
Override at build time:

```bash
go build -ldflags '-X main.version=v0.2.0' \
         -o bin/workers ./cmd/workers
```

The Makefile stamps it automatically with the short git head.
