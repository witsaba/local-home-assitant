# `messaging-core`

> Go backend service that boots its own **embedded NATS v2.15.0**
> server in-process, serves a **WebSocket camera streaming gateway**,
> and persists state in **Postgres**. Hexagonal layout. zap logging + OTel bridge.

This is the first backend service in the `local-home-assitant` repo.
Its purpose is to act as the central messaging broker and the camera
streaming gateway the rest of the stack talks to.

---

## Quick start

```bash
# Build + run with defaults: NATS on 127.0.0.1:4222, WS server on :8080
go build -o bin/messaging-core ./cmd/messaging-core
./bin/messaging-core

# With docker compose (from repo root)
docker compose up -d --build messaging-core
```

The binary logs structured JSON to stdout. A clean run looks like:

```json
{"level":"info","msg":"messaging-core starting","version":"0.1.0-dev","nats_host":"127.0.0.1","nats_port":4222,"stream_port":8080,"log_level":"info"}
{"level":"info","msg":"NATS server listening","url":"nats://127.0.0.1:4222","name":"messaging-core"}
{"level":"info","msg":"Postgres pool connected","pg_host":"127.0.0.1","pg_port":5432}
{"level":"info","msg":"API server started","addr":":8081"}
{"level":"info","msg":"messaging-core fully started","nats":"127.0.0.1:4222","stream":":8080","api":":8081"}
```

Stop with `Ctrl-C` / `SIGTERM`. Shutdown drains within 10s and exits 0.

---

## Configuration

All configuration comes from environment variables. Defaults make the service run on a developer laptop with zero setup.

| Variable | Default | Notes |
|---|---|---|
| `NATS_HOST` | `127.0.0.1` | NATS bind address. **Dev only.** |
| `NATS_PORT` | `4222` | NATS port. 0–65535. |
| `STREAM_PORT` | `8080` | **HTTP/WS port** for the camera streaming gateway. |
| `API_PORT` | `8081` | HTTP REST API port for device list endpoints. Must differ from `STREAM_PORT`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `NATS_DATA_DIR` | *(empty)* | Reserved for JetStream follow-up. |
| `MESSAGING_CORE_PG_HOST` | `127.0.0.1` | Postgres host (all containers use host network, reach postgres at 127.0.0.1). |
| `MESSAGING_CORE_PG_PORT` | `5432` | Postgres port. |
| `MESSAGING_CORE_PG_DATABASE` | `witsaba` | Database name. |
| `MESSAGING_CORE_PG_USER` | `pg-messaging-core` | Postgres role (SELECT on `witsaba.devices`). |
| `MESSAGING_CORE_PG_PASSWORD` | *(none)* | Required. |
| `MESSAGING_CORE_PG_MAX_CONNS` | `10` | Pool max connections. |
| `MESSAGING_CORE_PG_MIN_CONNS` | `1` | Pool min connections. |
| `MESSAGING_CORE_PG_MAX_CONN_LIFETIME` | `1h` | Pool max connection lifetime. |
| `MESSAGING_CORE_PG_MAX_CONN_IDLE_TIME` | `30m` | Pool max idle time. |

Invalid values (non-numeric port, missing MESSAGING_CORE_PG_PASSWORD, unknown log level) cause the process to exit with code `2`.

---

## REST API — Active Devices

### `GET /api/devices/active`

Returns all devices from `witsaba.devices` whose `last_seen_at` is within the last **60 seconds**.

```bash
curl http://127.0.0.1:8081/api/devices/active
```

**Response** — `200 OK`, `Content-Type: application/json`:

```json
[
  {
    "mac": "e08cfe3091b0",
    "name": "kitchen-cam",
    "fw": "0.1.0",
    "chip": "esp32cam",
    "last_source_ip": "192.168.1.100",
    "last_seen_at": "2026-09-28T14:30:00Z"
  }
]
```

Returns `[]` (empty array) when no devices have been seen in the last minute. Returns `500` on database error.

The `pg_messaging_core` Postgres role (SELECT only) is used; no write access is required.

---

## Camera Streaming Gateway

The service acts as a **WebSocket relay** between ESP32 camera chips and web clients:

```
5 web clients ──ws://pi:8080/stream/{mac}──▶ messaging-core
                                               │
                                               │ lazy WS connect
                                               ▼
                                         ESP32 chip /ws/cams
```

### WebSocket endpoint

| Method | Path | Description |
|---|---|---|
| `GET` | `/stream/{mac}` | WebSocket upgrade; validates MAC in DB, relays frames to client |
| `GET` | `/healthz` | Returns `{"status":"ok"}` |

**Connection flow:**
1. Client upgrades to WS at `/stream/e08cfe3091b0`
2. messaging-core queries `witsaba.devices` for the MAC
3. If not found: WS closes with HTTP 404
4. If found: messaging-core lazily connects to the chip's `/ws/cams`
5. Chip sends JPEG frames → messaging-core → all web clients watching that MAC

**Fan-out:** Multiple web clients watching the same camera share one chip connection. messaging-core broadcasts each frame to all registered viewers.

**Lazy chip connection:** The chip is only connected when at least one viewer is watching. When the last viewer disconnects, the chip connection is closed.

**Chip reconnection:** On WiFi blip / chip disconnect, messaging-core retries with exponential backoff (1s, 2s, 4s, 8s, 16s — up to 5 attempts).

### NATS subjects (reserved for follow-up)

```
devices.cam.{mac}.frames   → binary JPEG frames (future: chip publishes directly)
devices.cam.{mac}.status   → JSON status (future)
devices.cam.{mac}.hello    → JSON hello (future)
```

---

## Architecture (Hexagonal / Ports & Adapters)

```
cmd/messaging-core/main.go           ← composition root
└── internal/
    ├── domain/                     ← platform-agnostic types (Subject, Message)
    ├── application/
    │   └── ports/                  ← interfaces: Logger, Server
    └── infrastructure/
        ├── config/                 ← env-var loader (NATS + PG + STREAM_PORT)
        ├── logger/                 ← zap + otelzap bridge
        ├── natsserver/            ← embedded NATS server lifecycle
        ├── db/                     ← pgx/v5 singleton pool
        ├── devices/               ← device repository (SELECT by MAC, ListActive)
        ├── api/                   ← REST API server (GET /api/devices/active)
        ├── wsclient/              ← chip WS client (gorilla/websocket)
        ├── wsserver/             ← Gin HTTP/WS server (/stream/:mac)
        └── streamhub/             ← per-MAC viewer registry + fan-out
```

Dependency direction is strict:

```
   cmd  ──►  infrastructure  ──►  application/ports  ◄──  domain
                                                ▲
                                                └──── cmd also depends on ports
```

---

## Verification

```bash
# All tests
go test ./...

# Publish/receive round-trip only
go test ./internal/infrastructure/natsserver/... -v -run ReceivesPublished
```

---

## File-by-file map

| File | Purpose |
|---|---|
| `cmd/messaging-core/main.go` | Composition root: config → NATS → Postgres → StreamHub → WS server → SIGINT |
| `internal/domain/messaging.go` | `Subject`, `Message` types. |
| `internal/application/ports/logger.go` | `Logger` interface + `Field` struct. |
| `internal/infrastructure/config/config.go` | Env-var loader (NATS + PG + STREAM_PORT). |
| `internal/infrastructure/db/pool.go` | pgx/v5 singleton pool. |
| `internal/infrastructure/devices/` | Device repository (SELECT by MAC, ListActive from `witsaba.devices`). |
| `internal/infrastructure/api/` | REST API server (`GET /api/devices/active`). |
| `internal/infrastructure/wsclient/` | Chip WS client (gorilla/websocket → chip `/ws/cams`). |
| `internal/infrastructure/wsserver/` | Gin HTTP/WS server at `/stream/:mac`. |
| `internal/infrastructure/streamhub/` | Per-MAC viewer registry, lazy chip connect, fan-out. |
| `internal/infrastructure/natsserver/` | Embedded NATS server. |
| `internal/infrastructure/logger/` | zap + otelzap bridge. |

---

## Docker

Linux only — `network_mode: host` puts containers on the VM's network on Docker Desktop Mac/Windows.

```bash
# Build + run via compose (from repo root)
docker compose up -d --build messaging-core

# Build image standalone
docker build -t witsaba/messaging-core:local \
  -f services/messaging-core/Dockerfile \
  services/messaging-core
```

`STREAM_PORT=8080` is set in the compose file. The WS server listens on `:8080` inside the container.

---

## Follow-ups (out of scope for this branch)

| Item | Why deferred |
|---|---|
| NATS pub/sub fan-out | NATS subjects reserved; plain pub/sub is a config flag + consumer code change |
| JetStream frame persistence | Requires `NATS_DATA_DIR` config + consumer code |
| Auth on WS endpoint | Closed LAN assumption; matches existing stack |
| Frame buffering / DVR (last N seconds) | JetStream or Redis follow-up |
| `ws://pi:8080/stream/{mac}` consumer | Shipped: `frontend/web_ui/static/stream.html` subscribes over the nginx-proxied `/stream/` and paints the frames. See `odd/tasks/camera-stream-ui.md`. |
| Healthcheck HTTP endpoint | `/healthz` is sufficient for compose healthcheck |
| Multi-chip reconnect (same MAC from different Pis) | Home scale: one Pi owns one fleet |

---

## Versioning

`var version` in `cmd/messaging-core/main.go` is stamped at build time:

```bash
go build -ldflags '-X main.version=v0.2.0' \
         -o bin/messaging-core ./cmd/messaging-core
```
