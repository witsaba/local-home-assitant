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
# Build (output goes to ./bin/messaging-core)
go build -o bin/messaging-core ./cmd/messaging-core

# Run with defaults: bind 127.0.0.1:4222, log level info
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

Invalid values (non-numeric port, unknown log level) cause the
process to exit with code `2` before anything starts.

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
go test ./...

# Just the publish/receive round-trip
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
* **Health endpoint**: an HTTP `/healthz` independent from the
  NATS internal healthz, suitable for k8s probes.

---

## Versioning

`var version` in `cmd/messaging-core/main.go` is the build
identifier. Override at build time:

```bash
go build -ldflags '-X main.version=v0.2.0' \
         -o bin/messaging-core ./cmd/messaging-core
```
