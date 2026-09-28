# Feature: `messaging-core` — Camera Streaming Gateway

## Goal

Transform `messaging-core` from a stateless embedded-NATS skeleton into a
**camera streaming gateway** that:

1. Serves a WebSocket endpoint to web clients (`ws://pi:8080/stream/{mac}`)
2. On client request: validates the MAC against `witsaba.devices` (Postgres), gets the IP
3. Connects to the chip's `/ws/cams` endpoint (lazy — only when first viewer arrives)
4. Relays JPEG frames from the chip to all registered web viewers for that MAC

```
5 web clients → ws://pi:8080/stream/e08cfe3091b0
                                    │
                         messaging-core
                           (lazy)
                                    │ ws://192.168.1.x/ws/cams
                                    ▼
                              ESP32 chip
```

**Scope**: messaging-core only. The Qwik frontend and workers service are
separate workstreams. The chip firmware (`/ws/cams`) is unchanged.

## Design Decisions (locked)

| Decision | Choice | Rationale |
|---|---|---|
| HTTP framework | **Gin v1.12.x** | 48% market share in Go (2026); mature, `net/http` compatible, native gorilla/websocket support |
| WS library | **gorilla/websocket** | De-facto Go WS standard; works seamlessly with Gin |
| Web client port | **8080** (separate from NATS 4222) | Clean separation of concerns; NATS for inter-service messaging, HTTP/WS for client streaming |
| Chip connection | **Lazy** — connect only when first viewer requests | Avoids connecting to offline devices; reconnect-on-first-request |
| Chip disconnect | **When last viewer drops** | Minimize chip resource usage |
| Postgres client | **pgx/v5 + pgxpool** (singleton, mirrors workers) | Already proven in workers service |
| DB query | `SELECT mac, name, fw, chip, host(last_source_ip) FROM witsaba.devices WHERE mac = $1` | Returns IP as `host(last_source_ip)::TEXT` |
| Viewer lifecycle | Registry per MAC: `map[mac][]*ws.Conn` | Fan-out to all registered clients on each frame |
| Auth | **None** | Closed LAN assumption; matches existing stack philosophy |
| NATS usage | **Deferred** | Plain NATS pub/sub can be layered in a follow-up; this branch is WS-only |
| DB pool lifecycle | Open at startup, close on SIGTERM | Standard pattern from workers |

## Architecture

```
services/messaging-core/
├── cmd/messaging-core/
│   └── main.go                      ← composition root: config → logger → NATS → DB → StreamHub → Gin → block on SIGINT
├── internal/
│   ├── application/
│   │   └── ports/
│   │       ├── logger.go            ← existing
│   │       └── messaging.go          ← existing
│   ├── domain/
│   │   └── messaging.go              ← existing (Subject type)
│   └── infrastructure/
│       ├── config/config.go          ← T1: extend with PG_* + STREAM_PORT
│       ├── db/                       ← T2: NEW: singleton pool (mirrors workers)
│       │   ├── pool.go
│       │   └── pool_test.go
│       ├── devices/                  ← T3: NEW: device repository (SELECT by MAC)
│       │   ├── devices.go            ← interface: GetByMAC(ctx, mac) (Device, error)
│       │   └── store.go             ← pgx implementation
│       ├── logger/                   ← existing
│       ├── natsserver/              ← existing
│       ├── streamhub/               ← T6: NEW: per-MAC viewer registry + fan-out
│       │   ├── hub.go               ← CameraHub struct, viewer registration/deregistration
│       │   └── hub_test.go
│       ├── wsclient/               ← T4: NEW: chip WS client
│       │   ├── client.go            ← Connect, receive frames, broadcast
│       │   └── client_test.go
│       └── wsserver/               ← T5: NEW: Gin + WS server for web clients
│           ├── server.go            ← Gin router, /stream/:mac WS handler
│           └── server_test.go
```

## Data Structures

```go
// Device represents a row from witsaba.devices.
type Device struct {
    MAC         string    // primary key
    Name        string    // nullable
    FW          string    // nullable
    Chip        string    // nullable
    LastSourceIP net.IP   // host(last_source_ip)::INET
}

// ViewerSession wraps a gorilla WebSocket connection for a web client.
type ViewerSession struct {
    MAC   string           // which camera this viewer watches
    Conn  *websocket.Conn  // the web client WS connection
    Done  chan struct{}    // closed when the viewer disconnects
}
```

## Public API (new interfaces)

```go
// DeviceRepository queries the witsaba.devices table.
type DeviceRepository interface {
    GetByMAC(ctx context.Context, mac string) (*Device, error)
}

// StreamHub manages per-camera state: chip WS connections and viewer registries.
type StreamHub interface {
    // RegisterViewer adds a web client to a camera's fan-out list.
    // If no chip is connected to this MAC, it initiates a lazy chip connection.
    RegisterViewer(ctx context.Context, mac string, conn *websocket.Conn) error
    // DeregisterViewer removes a web client. If no viewers remain for a MAC,
    // the chip connection is closed.
    DeregisterViewer(ctx context.Context, mac string, conn *websocket.Conn)
    // Close cleanly shuts down all chip connections and viewer sessions.
    Close(ctx context.Context)
}

// WSClient connects to a chip's /ws/cams endpoint.
type WSClient interface {
    // Connect establishes a WS connection to ws://ip/ws/cams and starts
    // a goroutine that reads frames and calls onFrame for each JPEG.
    Connect(ctx context.Context, ip string, onFrame func([]byte)) error
    // Close gracefully closes the chip connection.
    Close(ctx context.Context) error
}
```

## Wire Contract (Chip → messaging-core)

The chip's `/ws/cams` emits:

```jsonc
// hello — first text frame (JSON)
{
  "type": "hello",
  "mac":  "e08cfe3091b0",
  "name": "iot-cam-1",
  "fw":   "0.1.0",
  "caps": ["jpeg", "stream", "identify", "control"]
}

// status — every 30s (JSON)
{
  "type": "status",
  "mac": "e08cfe3091b0",
  "uptime_s": 1247,
  "rssi_dbm": -58,
  "free_heap": 87432,
  "fb_drops": 3,
  "frames_sent": 37210,
  "frames_dropped": 7,
  "fps_applied": 10
}

// binary — JPEG frame (op=0x2, final=true)
<payload> = raw JPEG bytes
```

messaging-core reads all three frame types. Text frames (`hello`, `status`) are
logged for observability. Binary frames are relayed verbatim to all registered
viewers for that MAC.

## Wire Contract (messaging-core → Web Client)

messaging-core serves `ws://pi:8080/stream/{mac}` and relays the chip frames
unchanged:

- TEXT `hello` / `status` → forwarded as-is
- BINARY JPEG → forwarded as-is

The web client receives exactly what the chip sends, preserving the chip's
native wire format.

## HTTP Endpoints (new)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/stream/{mac}` | WebSocket upgrade; validates MAC in DB, registers viewer, relays frames |
| `GET` | `/healthz` | Returns 200 OK if service is running |

## Error Handling

| Scenario | Behavior |
|----------|----------|
| MAC not found in DB | WS close with code 4001, reason `"device_not_found"` |
| Chip IP unreachable | WS close with code 4002, reason `"chip_unreachable"` |
| Chip WS handshake fails | WS close with code 4003, reason `"chip_handshake_failed"` |
| Web client disconnects | Deregister from hub; close chip if no other viewers |
| Chip disconnects (WiFi blip) | Reconnect with exponential backoff (max 5 retries, 1s/2s/4s/8s/16s); notify viewers of temporary disruption |
| messaging-core restarts | All chip connections are recreated lazily on next viewer request |

## NATS Subject Naming (deferred — not in this branch)

```
devices.cam.{mac}.frames   → binary JPEG frames (future: from chip NATS publisher)
devices.cam.{mac}.status   → JSON status (future)
devices.cam.{mac}.hello   → JSON hello (future)
```

These subjects are reserved but not implemented in this branch.

## Tasks

### T1 — Extend config: add PG_* env vars + STREAM_PORT

- Modify: `services/messaging-core/internal/infrastructure/config/config.go`
  - Add: `PGHost`, `PGPort`, `PGDatabase`, `PGUser`, `PGPassword`
  - Add: `STREAM_PORT` (default 8080)
  - Add: `PGMaxConns`, `PGMinConns`, `PGMaxConnLifetime`, `PGMaxConnIdleTime`
  - Mirror the `config.go` pattern from `services/workers/internal/infrastructure/config/config.go`
  - `Load()` reads all new vars with sane defaults
- Work-unit commit: `feat(messaging-core): extend config with PG_* + STREAM_PORT`

### T2 — Add db singleton pool (mirrors workers pattern, pgx/v5)

- New: `services/messaging-core/internal/infrastructure/db/pool.go`
  - Copy the `db` package pattern from `services/workers/internal/infrastructure/db/pool.go`
  - `db.PoolConfig` → `db.Open(cfg)` → `db.Get()` singleton
  - Uses `github.com/jackc/pgx/v5/pgxpool`
- New: `services/messaging-core/internal/infrastructure/db/pool_test.go`
- Work-unit commit: `feat(messaging-core): add pgx/v5 singleton db pool`

### T3 — Add device repository (SELECT by MAC, mirrors workers)

- New: `services/messaging-core/internal/infrastructure/devices/devices.go`
  - `Device` struct matching the schema
  - `DeviceRepository` interface: `GetByMAC(ctx, mac) (*Device, error)`
  - `NotFoundError` sentinel
- New: `services/messaging-core/internal/infrastructure/devices/store.go`
  - `Pgx` implementation: `SELECT mac, name, fw, chip, host(last_source_ip) FROM witsaba.devices WHERE mac = $1`
  - Mirrors `services/workers/internal/infrastructure/devices/store.go`
- New: `services/messaging-core/internal/infrastructure/devices/store_test.go`
  - Unit test with fakeQuerier
- Work-unit commit: `feat(messaging-core): add device repository (SELECT by MAC)`

### T4 — Add WS client: chip connection via gorilla/websocket

- New: `services/messaging-core/internal/infrastructure/wsclient/client.go`
  - `WSClient` interface (above)
  - `ChipClient` struct: dials `ws://ip/ws/cams`, reads frames
  - On `hello` text frame: log MAC/name/fw/caps for observability
  - On binary frame: call `onFrame([]byte)` callback
  - Thread-safe: `onFrame` callback is called from a single read goroutine
  - `Close()` performs clean WS close + drains the read loop
- New: `services/messaging-core/internal/infrastructure/wsclient/client_test.go`
  - Mock WS server (httptest server with WS handler)
  - Test: connect → receive hello → receive binary → close
- Add dependency: `github.com/gorilla/websocket` to `go.mod`
- Work-unit commit: `feat(messaging-core): add WS client for chip /ws/cams connection`

### T5 — Add WS server: Gin + gorilla/websocket at /stream/:mac

- New: `services/messaging-core/internal/infrastructure/wsserver/server.go`
  - `StreamServer` struct holds Gin engine + port
  - `GET /stream/:mac` — WS upgrade, validates MAC via `DeviceRepository`, calls `StreamHub.RegisterViewer`
  - `GET /healthz` — 200 OK
  - On WS close: call `StreamHub.DeregisterViewer`
  - Uses `github.com/gin-gonic/gin` + `github.com/gorilla/websocket`
- New: `services/messaging-core/internal/infrastructure/wsserver/server_test.go`
- Add dependency: `github.com/gin-gonic/gin` to `go.mod`
- Work-unit commit: `feat(messaging-core): add Gin WS server at /stream/:mac`

### T6 — Add camera hub: per-MAC registry + fan-out logic

- New: `services/messaging-core/internal/infrastructure/streamhub/hub.go`
  - `StreamHub` interface (above)
  - `CameraHub` struct:
    ```go
    type CameraHub struct {
        mu      sync.RWMutex
        viewers map[string]map[*websocket.Conn]struct{}  // mac → conn set
        chips   map[string]wsclient.WSClient            // mac → chip WS client
        devices devices.DeviceRepository
        log     ports.Logger
    }
    ```
  - `RegisterViewer`: validate MAC → get IP from DB → if no chip, connect lazily → add viewer to registry
  - `DeregisterViewer`: remove viewer → if viewer set empty, close chip connection
  - `BroadcastToViewers(mac string, frame []byte)`: fan-out to all viewers for that MAC
  - Chip reconnection: on chip disconnect, reconnect with exponential backoff (5 retries)
- New: `services/messaging-core/internal/infrastructure/streamhub/hub_test.go`
- Work-unit commit: `feat(messaging-core): add camera hub with viewer registry + fan-out`

### T7 — Wire everything in cmd/messaging-core/main.go

- Modify: `services/messaging-core/cmd/messaging-core/main.go`
  - After NATS server starts: open DB pool via `db.Open(cfg)`
  - Build `devices.NewPgx(db.Get())`
  - Build `streamhub.NewCameraHub(devicesRepo, log)`
  - Build `wsserver.NewStreamServer(streamHub, devicesRepo, log)` on `STREAM_PORT`
  - Start the WS server in a goroutine
  - On SIGTERM: close stream hub → close DB pool → shutdown NATS
- Work-unit commit: `feat(messaging-core): wire streaming gateway into composition root`

### T8 — Update docker-compose.yml: add STREAM_PORT

- Modify: `docker-compose.yml`
  - messaging-core: add `STREAM_PORT: 8080` env var
  - Healthcheck for messaging-core (verify STREAM_PORT is listening)
- Work-unit commit: `chore(compose): add STREAM_PORT=8080 to messaging-core`

### T9 — Unit tests + integration test

- Add: `services/messaging-core/internal/infrastructure/db/pool_test.go`
- Add: `services/messaging-core/internal/infrastructure/devices/store_test.go`
- Add: `services/messaging-core/internal/infrastructure/wsclient/client_test.go`
- Add: `services/messaging-core/internal/infrastructure/wsserver/server_test.go`
- Add: `services/messaging-core/internal/infrastructure/streamhub/hub_test.go`
- Work-unit commit: `test(messaging-core): add unit tests for all new components`

### T10 — Update README

- Modify: `services/messaging-core/README.md`
  - Document: new `WS_PORT`, `PG_*` env vars
  - Document: `/stream/{mac}` WS endpoint
  - Document: architecture diagram (updated)
  - Document: NATS subjects (reserved for follow-up)
- Work-unit commit: `docs(messaging-core): update README with streaming gateway`

## Out of Scope (follow-ups)

| Item | Why deferred |
|------|-------------|
| NATS pub/sub fan-out | Plain NATS integration; NATS subjects reserved but not wired |
| JetStream frame persistence | Requires NATS_DATA_DIR config + consumer code |
| Auth / token on WS endpoint | Closed LAN assumption; matches existing stack |
| Frame buffering / DVR (last N seconds) | JetStream or Redis follow-up |
| Multiple concurrent chip connections (same MAC from different Pis) | Home scale: one Pi owns one fleet |
| Healthcheck HTTP endpoint (beyond /healthz) | /healthz is sufficient for compose healthcheck |
| Frontend integration | Separate Qwik workstream |
| Device auto-discovery (NATS → chip) | Follows NATS integration |
| Reconnecting to offline chips while viewers exist | Exponential backoff reconnect (T6) covers basic cases |

## Acceptance Criteria

1. `go build ./...` exits 0 with zero new warnings.
2. `go vet ./...` is clean.
3. `go test ./...` passes (all new + existing tests).
4. messaging-core starts on port 8080 (WS server) alongside NATS on 4222.
5. `ws://localhost:8080/stream/e08cfe3091b0` with an unknown MAC → WS close code 4001.
6. `ws://localhost:8080/stream/{valid-mac}` with chip reachable → frames forwarded to web client.
7. Two parallel web clients watching the same MAC → both receive identical frames (fan-out verified).
8. All web clients for a MAC disconnect → chip WS connection closed.
9. Chip WiFi blip → exponential backoff reconnect (≤ 5 retries), viewers notified.
10. `docker compose build messaging-core` succeeds.
11. Fleet parity: single binary for all hardware classes.

## Env Vars Added

| Variable | Default | Description |
|----------|---------|-------------|
| `STREAM_PORT` | `8080` | HTTP/WS server port for web clients |
| `PG_HOST` | `127.0.0.1` | Postgres host |
| `PG_PORT` | `5432` | Postgres port |
| `PG_DATABASE` | `witsaba` | Postgres database |
| `PG_USER` | `pg-messaging-core` | Postgres user |
| `PG_PASSWORD` | (none) | Postgres password |
| `PG_MAX_CONNS` | `10` | Pool max connections |
| `PG_MIN_CONNS` | `1` | Pool min connections |
| `PG_MAX_CONN_LIFETIME` | `1h` | Pool max connection lifetime |
| `PG_MAX_CONN_IDLE_TIME` | `30m` | Pool max idle time |

## Commit Log

| Task | Commit | Status |
|------|--------|--------|
| T1 | `6da67b5` feat(messaging-core): extend config with PG_* + STREAM_PORT | ✅ |
| T2 | `db8a683` feat(messaging-core): add pgx/v5 singleton db pool | ✅ |
| T3 | `b08e0b8` feat(messaging-core): add device repository (SELECT by MAC) | ✅ |
| T4 | `5da5d44` feat(messaging-core): add WS client for chip /ws/cams connection | ✅ |
| T5 | `b9f0509` feat(messaging-core): add Gin WS server at /stream/:mac | ✅ |
| T6 | `03187ed` feat(messaging-core): add camera hub with viewer registry + fan-out | ✅ |
| T7 | `d99f93f` feat(messaging-core): wire streaming gateway into composition root | ✅ |
| T8 | `821d6db` chore(compose): add STREAM_PORT=8080 and PG_* to messaging-core | ✅ |
| T9 | (tests alongside each component) | ✅ |
| T10 | `a81478f` docs(messaging-core): update README with streaming gateway | ✅ |

## Tracking

Mirrored to Engram under project `local-home-assitant`, topic key
`messaging-core/streaming-gateway`. `todo` list reflects this plan.
