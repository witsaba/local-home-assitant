# `messaging-core`

> Go backend service that boots its own **embedded NATS v2.15.0**
> server in-process. Hexagonal layout. zap logging + OTel bridge.
> **Skeleton only** — no producer, no consumer.

This is the first backend service in the
`local-home-assitant` repo. Its purpose is to be the central
messaging broker the rest of the stack talks to. The broker runs
inside the service binary — there is **no external NATS to
provision**.

---

## Quick start

```bash
# Show every developer target this repo exposes
make help

# Build + run with defaults: bind 127.0.0.1:4222, log level info
make run

# Or invoke directly
go build -o bin/messaging-core ./cmd/messaging-core
./bin/messaging-core

# Override via env vars
NATS_PORT=5222 LOG_LEVEL=debug ./bin/messaging-core
```

The binary logs structured JSON to stdout. A clean run looks like:

```json
{"level":"info","msg":"messaging-core starting","version":"0.1.0-dev","host":"127.0.0.1","port":4222,"log_level":"info"}
{"level":"info","msg":"NATS server starting","host":"127.0.0.1"}
{"level":"info","msg":"NATS server listening","url":"nats://127.0.0.1:4222","name":"messaging-core"}
```

Stop with `Ctrl-C` / `SIGTERM`. Shutdown drains within 10s and
exits 0.

---

## Configuration

All configuration comes from environment variables. Defaults make
the service run on a developer laptop with zero setup.

| Variable        | Default       | Notes                                |
| --------------- | ------------- | ------------------------------------ |
| `NATS_HOST`     | `127.0.0.1`   | Bind address. **Dev only.**          |
| `NATS_PORT`     | `4222`        | 0–65535. `-1` = OS-assigned (tests). |
| `LOG_LEVEL`     | `info`        | `debug`, `info`, `warn`, `error`.    |
| `NATS_DATA_DIR` | *(empty)*     | Reserved for JetStream follow-up.    |
| `PG_HOST`       | `127.0.0.1`   | Postgres host. Loaded today but no DB connection opened yet. |
| `PG_PORT`       | `5432`        | Postgres port. Loaded but unused in v1. |
| `PG_DATABASE`   | `witsaba`     | Database name. Loaded but unused in v1. |
| `PG_USER`       | `pg-messaging-core` | Role for the future read consumer. |
| `PG_PASSWORD`   | (none)        | Password for the role above. Loaded but unused in v1. |

Invalid values (non-numeric port, unknown log level) cause the
process to exit with code `2` before anything starts.

### `PG_*` are loaded but unused today

The compose file forwards `PG_HOST` / `PG_PORT` / `PG_DATABASE` /
`PG_USER` / `PG_PASSWORD` to this service so operators can pin the
credentials once and let any future read consumer pick them up.
The v1 binary loads them into `Config` but does **not** open a
connection — the first read consumer is a separate follow-up.

---

## Architecture (Hexagonal / Ports & Adapters)

```
cmd/messaging-core/main.go           ← composition root, signal handling
└── internal/
    ├── domain/                      ← platform-agnostic types (Subject, Message)
    ├── application/
    │   └── ports/                   ← interfaces: Logger, Server
    └── infrastructure/              ← adapters (depend on ports, never the reverse)
        ├── config/                  ← env-var loader
        ├── logger/                  ← zap + otelzap bridge
        └── natsserver/              ← embedded NATS server lifecycle
```

Dependency direction is strict:

```
   cmd  ──►  infrastructure  ──►  application/ports  ◄──  domain
                                                ▲
                                                └──── cmd also depends on ports
```

* `domain` is pure Go; it imports nothing.
* `application/ports` declares the contracts (`Logger`,
  `Server`) the application needs. It also imports nothing.
* `infrastructure` implements those contracts. It may import
  third-party libraries (zap, nats-server, OTel).
* `cmd` is the only place where the application reaches into
  infrastructure to wire things up.

---

## Verification

```bash
# All 17 tests across config, logger, and natsserver packages
make test

# Or invoke go test directly
go test ./...

# Publish/receive round-trip only
go test ./internal/infrastructure/natsserver/... -v -run ReceivesPublished
```

The smoke test does the following:

1. Boots the embedded NATS server on a random free port.
2. Connects a `nats.go` client.
3. Subscribes to `test.subject`.
4. Publishes a payload.
5. Confirms the subscriber receives the **exact same bytes**.

That round-trip proves the server accepts clients and routes
messages. The fan-out test extends that to five subscribers on
the same subject.

---

## File-by-file map

| File                                                                  | Purpose                                                                             |
| --------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `cmd/messaging-core/main.go`                                          | Composition root. Loads config, builds logger, starts NATS, blocks on SIGINT/SIGTERM. |
| `internal/domain/messaging.go`                                        | `Subject`, `Message` types. Placeholders for future payload schemas.                |
| `internal/application/ports/logger.go`                                | `Logger` interface + `Field` struct.                                                |
| `internal/application/ports/messaging.go`                             | `Server` interface (`Start`, `Ready`, `Addr`, `Shutdown`).                           |
| `internal/infrastructure/config/config.go`                            | `Load()` reads env vars with sane defaults.                                         |
| `internal/infrastructure/logger/logger.go`                            | zap JSON encoder + otelzap bridge for OTel-ready log records.                       |
| `internal/infrastructure/natsserver/server.go`                        | Implements `ports.Server` over `nats-server/v2`.                                   |
| `internal/infrastructure/natsserver/server_test.go`                   | Lifecycle tests (start/ready/shutdown).                                             |
| `internal/infrastructure/natsserver/publish_test.go`                  | Publish/receive end-to-end + fan-out tests.                                         |
| `Makefile`                                                            | Developer targets: `make help` for the list.                                        |
| `.golangci.yaml`                                                      | Minimal golangci-lint config used by `make lint`.                                   |

---

## Follow-ups (intentionally out of scope for this branch)

* **Auth**: NATS token / credentials / TLS. Add `Authorization: <token>`
  in `server.Options` and `Token` in the client option. Tracked
  but not implemented — this branch binds to localhost only.
* **JetStream**: enable via `opts := &server.Options{...}; srv.EnableJetStream(...)`.
  Storage dir is already plumbed through `config.Config.DataDir`.
* **Full OTel SDK init**: the `otelzap` bridge is wired in, but
  the global `LoggerProvider` is still the no-op default until
  a tracer+log exporter is initialised.
* **Producer**: a NATS publisher abstraction over `ports.Server`'s
  client. Will live in `internal/application/messaging` once the
  first publisher is needed.
* **Consumer**: subscription lifecycle + handler registration.
  Same location as the producer.
* **DB read consumer**: an internal/consumer that reads from
  `witsaba.devices` (as `pg-messaging-core`, `SELECT` only) and
  forwards events over NATS. Auth credentials are already plumbed
  via `PG_*`; only the consumer logic is missing.
* **Health endpoint**: an HTTP `/healthz` independent from the
  NATS internal healthz, suitable for k8s probes.

---

## Docker

Linux only — see caveat at the bottom of this section.

### Build & run via compose (from repo root)

```bash
docker compose up -d --build      # build + start both services
docker compose logs -f            # tail both
docker compose down               # stop and remove
```

With `network_mode: host` the container binds `127.0.0.1:4222` on
the host. See "Remote clients" below for cross-host access.

### Build the image standalone

```bash
# Default version stamp (0.1.0-dev-unknown)
docker build -t witsaba/messaging-core:local \
  -f services/messaging-core/Dockerfile \
  services/messaging-core

# Stamped from the current commit
docker build \
  --build-arg VERSION=0.1.0 \
  --build-arg GIT_HEAD=$(git rev-parse --short HEAD) \
  -t witsaba/messaging-core:dev \
  -f services/messaging-core/Dockerfile \
  services/messaging-core
```

### Image details

- Build stage: `golang:1.26.3-alpine3.23` (~66 MB compressed).
- Runtime stage: `alpine:3.23` + `ca-certificates`.
- Static binary (`CGO_ENABLED=0`), cross-compiled to `linux`.
- Runs as `nobody` (uid 65534).

### Remote clients (future)

To accept clients from other hosts, set `NATS_HOST=0.0.0.0` in `.env`
(Compose substitutes it). Then add NATS auth (token / creds / TLS) —
auth and TLS are tracked follow-ups in this README, not part of this
branch.

### NATS hostname: docker service name

`NATS_HOST` defaults to `messaging-core` — the docker service name.
Because the compose file uses `network_mode: host` (so the worker can
reach the LAN), Docker's service-name DNS does not apply. The compose
file injects `extra_hosts: ["messaging-core:127.0.0.1"]` on every
service, so `messaging-core` resolves to the host loopback via each
container's `/etc/hosts`. The embedded NATS server therefore binds to
`127.0.0.1:4222` on the host, and any client (e.g. a future worker
using NATS) can connect with `nats://messaging-core:4222`.

Verified locally on Mac dev host: `docker compose up -d`, then from
the workers container, `nc -zv messaging-core 4222` reports open.

### Linux-only caveat

`network_mode: host` on Docker Desktop (Mac/Windows) puts the
container on the VM's network namespace, **not** your LAN. The
worker in the same stack will not see devices like `192.168.1.51`
from Mac dev. Validate on a real Linux target. Mac dev support via
`ipvlan` / `macvlan` is tracked separately and not in this branch.

Cross-platform builds: building on arm64 produces an arm64 image; on
amd64 an amd64 image. For an explicit target arch, use buildx:

```bash
docker buildx build --platform linux/amd64 \
  -t witsaba/messaging-core:dev \
  -f services/messaging-core/Dockerfile \
  --load services/messaging-core
```

---

## Versioning

`var version` in `cmd/messaging-core/main.go` is the build
identifier. Override at build time:

```bash
go build -ldflags '-X main.version=v0.2.0' \
         -o bin/messaging-core ./cmd/messaging-core
```
