# Feature: `messaging-core` — NATS skeleton (embedded server)

## Goal

Stand up a new Go backend service — **`messaging-core`** — at
`services/messaging-core/`. The service boots its own embedded NATS
server (v2.15.0) **inside** the Go process; no external broker is
required. The repo gets a Hexagonal / Ports & Adapters layout, a
production-grade logger (zap + official `otelzap` bridge), and a
clean runnable skeleton that other services can already connect to
once this branch lands.

This branch is the **skeleton only**. Producers and consumers are
explicitly out of scope and will be added in later branches.

## Design decisions (locked with the user)

| Decision | Choice | Rationale |
| --- | --- | --- |
| Service folder | `services/messaging-core/` | Stated purpose: "main core backend for the messaging". |
| Module path | `github.com/witsaba/local-home-assitant/services/messaging-core` | Inferred from the workspace path; consistent with the existing repo layout. |
| Architecture | **Hexagonal / Ports & Adapters** | Locked with the user. Strict dependency direction: `domain` ← `application` ← `infrastructure`; `cmd` is the only composition root. |
| Folder layout | `cmd/messaging-core/` + `internal/{domain,application,infrastructure}/` | Standard Hexagonal split; keeps everything non-exportable. |
| NATS server | `github.com/nats-io/nats-server/v2/server` **v2.15.0** | Latest stable (released 2026-09-17). Embedded via `server.NewServer(opts)`. |
| NATS client | `github.com/nats-io/nats.go` **v1.54.0** | Used by the verification test only; not yet wired into the main binary. |
| NATS auth | Insecure, **localhost-only** (127.0.0.1:4222), no auth | Locked with the user. Suitable for dev; production auth lands in a follow-up branch. |
| Logger | `go.uber.org/zap` + `go.opentelemetry.io/contrib/bridges/otelzap` | 71 ns/op, 0 allocs/op; only fast logger with an **official** OTel bridge. |
| Config | env vars via `os.Getenv` with sane defaults; `internal/infrastructure/config` | Zero deps; viper/koanf can be slotted in later without changing call sites. |
| Verification | Go test in `internal/infrastructure/natsserver/server_test.go` | Confirms: server starts, client connects, message is received. Does **not** add producer/consumer code to the binary. |
| Worktree | `feat/messaging-core-nats-scaffold` (under `…-worktrees/`) | Matches existing branch convention; isolate from `main` and from the existing `iot-cams-wifi-provisioning` worktree. |

## Public surface (this branch)

```go
// internal/application/ports/logger.go
type Logger interface {
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)
    Debug(msg string, fields ...Field)
    Sync() error
}

// internal/application/ports/messaging.go
type Server interface {
    Start(ctx context.Context) error
    Ready() <-chan struct{}   // closed when NATS is accepting clients
    Addr() string             // "nats://127.0.0.1:4222"
    Shutdown(ctx context.Context) error
}
```

```go
// cmd/messaging-core/main.go
func main()        // env-config → logger → NATS server → SIGINT/SIGTERM → graceful shutdown
```

The test uses the production constructor, never reaching into private
fields:

```go
// internal/infrastructure/natsserver/server_test.go
func TestEmbeddedServer_StartsAndReceivesMessage(t *testing.T) {
    s, _ := natsserver.New(natsserver.Config{ Host: "127.0.0.1", Port: 0 }, logger)
    s.Start(ctx)
    defer s.Shutdown(ctx)
    <-s.Ready()

    nc, _ := nats.Connect(s.Addr())
    defer nc.Drain()
    if err := nc.Publish("test.subject", []byte("ping")); err != nil { t.Fatal(err) }
    if err := nc.FlushTimeout(time.Second); err != nil { t.Fatal(err) }
}
```

## Tasks

- [ ] **T1 — Worktree + Go module init + Hexagonal folder skeleton**
  - Worktree already on `feat/messaging-core-nats-scaffold`.
  - `go mod init github.com/witsaba/local-home-assitant/services/messaging-core`
  - Folder skeleton with `.gitkeep` placeholders:
    ```
    services/messaging-core/
      cmd/messaging-core/.gitkeep
      internal/domain/.gitkeep
      internal/application/ports/.gitkeep
      internal/infrastructure/{config,logger,natsserver}/.gitkeep
      README.md
    ```
  - Work-unit commit: `chore(messaging-core): scaffold hexagonal layout + go.mod`

- [ ] **T2 — Config loader**
  - `internal/infrastructure/config/config.go` reads env vars: `NATS_HOST`, `NATS_PORT`, `LOG_LEVEL`, `NATS_DATA_DIR` (optional, unused for now).
  - Defaults: `127.0.0.1`, `4222`, `info`.
  - Pure function `Load() (Config, error)` — no globals.
  - Unit test: `config_test.go` covering defaults + override.
  - Work-unit commit: `feat(messaging-core): env-based config loader`

- [ ] **T3 — Logger adapter (zap + otelzap wiring)**
  - `internal/infrastructure/logger/logger.go` exports `New(level string) (ports.Logger, error)`.
  - Uses `zap.NewProductionConfig()` when `LOG_LEVEL=info` (or any non-debug), `zap.NewDevelopmentConfig()` when `debug`.
  - JSON encoder; ISO8601 timestamps; caller info enabled.
  - The `otelzap` bridge is wired in as a `zapcore.Core` wrapper so every log line carries `trace_id` / `span_id` once OTel is initialized later.
  - `Sync()` flushes buffers.
  - Work-unit commit: `feat(messaging-core): zap logger adapter with otelzap bridge`

- [ ] **T4 — Domain + application ports (interfaces only)**
  - `internal/domain/messaging.go` — placeholder type:
    ```go
    package domain
    type Subject string   // e.g. "devices.telemetry"
    ```
  - `internal/application/ports/logger.go` — `Logger` interface (above).
  - `internal/application/ports/messaging.go` — `Server` interface (above).
  - `.gitkeep` files deleted from these folders.
  - Work-unit commit: `feat(messaging-core): domain types + ports interfaces`

- [ ] **T5 — NATS server adapter (embedded server lifecycle)**
  - `internal/infrastructure/natsserver/server.go`:
    - `type Config struct { Host string; Port int }`
    - `New(cfg Config, log ports.Logger) (ports.Server, error)` builds `server.NewServer(&server.Options{Host: cfg.Host, Port: cfg.Port, …})`.
    - `Start(ctx)` runs `go s.srv.Start(); <-s.srv.ReadyNotify()` then closes the `Ready()` channel.
    - `Shutdown(ctx)` runs `srv.Shutdown()` then `srv.Wait()`.
    - `Addr()` returns `nats://host:port` once `Ready()` has fired.
  - Logs: `"NATS server starting"`, `"NATS server listening on nats://127.0.0.1:4222"`, `"NATS server shutting down"`.
  - Work-unit commit: `feat(messaging-core): embedded NATS server adapter`

- [ ] **T6 — Wire `cmd/messaging-core/main.go`**
  - Reads config → builds logger → constructs NATS server → starts it → blocks on `SIGINT`/`SIGTERM` signal channel → graceful shutdown.
  - Exit code: 0 on signal, 1 on startup failure.
  - `go build` produces `bin/messaging-core`.
  - Work-unit commit: `feat(messaging-core): main composition root + signal handling`

- [ ] **T7 — Verification test (server starts + receives a message)**
  - `internal/infrastructure/natsserver/server_test.go`:
    - Picks a random port (`Port: 0` in `Config`, then read back from the running server).
    - Starts the server, waits for `Ready()`, connects a `nats` client, publishes `test.subject`, calls `FlushTimeout`.
    - Asserts no error from publish/flush (server accepted and routed the message).
  - `internal/infrastructure/config/config_test.go` covers defaults and overrides.
  - Work-unit commit: `test(messaging-core): embedded NATS server receives published message`

- [ ] **T8 — README**
  - `services/messaging-core/README.md` documents:
    - Purpose
    - How to build (`go build ./...`)
    - How to run (`./bin/messaging-core`, env vars)
    - Architecture (Hexagonal — with a one-paragraph "why" per layer)
    - Verification (`go test ./...` — what it proves)
    - Explicit follow-up list (auth, JetStream, OTel SDK init, producers/consumers).
  - Work-unit commit: `docs(messaging-core): service README`

- [ ] **T9 — Final sweep**
  - `go build ./...`, `go vet ./...`, `go test ./...` all green.
  - Run the binary for ~2 seconds under `go run`, observe logs, kill cleanly.
  - Update this doc with the closed-task commit list.
  - Work-unit commit: `chore(messaging-core): final review notes`.

## Out of scope (follow-ups, not part of this branch)

- NATS token / credentials / TLS — auth lands in a follow-up.
- JetStream — same.
- OTel SDK init (tracer provider + log exporter) — only the bridge is wired; full SDK init is a follow-up.
- Any producer or consumer — explicit per the user's instruction.
- Hot reload of config, graceful drain of in-flight messages, health-check HTTP endpoint.

## Acceptance criteria

1. `go build ./...` produces `bin/messaging-core` with no warnings.
2. `go vet ./...` is clean.
3. `go test ./...` passes, including the embedded-server test.
4. Running `bin/messaging-core` logs the listening address within ~200 ms and exits 0 on `SIGINT`.
5. The verification test proves the embedded server accepts and routes a real NATS publish.

## Tracking

Mirrored to Engram under project `local-home-assitant` topic
`services/messaging-core/nats-scaffold`. `todo` list reflects this plan.
