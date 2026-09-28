# Feature: devices API — active device list

## Branch
`feat/devices-api-active-list` (current worktree)

## Goal
Add a `GET /api/devices/active` REST endpoint to `messaging-core` that returns devices whose `last_seen_at` is within the last 60 seconds. Wire a Qwik `/devices` page in `web_ui` that fetches and displays them.

## Acceptance criteria
- `GET /api/devices/active` returns JSON `[{mac, name, fw, chip, last_source_ip, last_seen_at}]`
- Only devices with `last_seen_at >= now() - 60 seconds` are returned
- `GET /api/devices/active` returns `200` with empty array when no devices are active
- Frontend `/devices` page renders the device list with MAC, name, IP, and last-seen
- Frontend degrades gracefully when the API is unreachable

## Tasks

- [x] **T1** Add `ListActive(ctx, maxAge) ([]*Device, error)` to `devices.DeviceRepository` interface and `devices.Pgx` implementation
- [x] **T2** Wire `net/http` server + `GET /api/devices/active` handler into `messaging-core main.go`; add `API_PORT` env var (default 8081)
- [x] **T3** Add unit tests for `ListActive` (store test with fake Querier) and HTTP handler
- [x] **T4** Create `frontend/web_ui/src/routes/devices/index.tsx` — fetches `/api/devices/active`, renders device cards
- [x] **T5** Update `env.example` with `API_PORT`; update `messaging-core/README.md`

## Tech decisions
- HTTP server is separate stdlib `net/http` mux (not chi/gin — keep deps minimal)
- `API_PORT` defaults to 8081 to avoid conflict with existing `STREAM_PORT` 8080
- `maxAge` passed as `time.Duration`; SQL uses `now() - $1` with `interval '1 minute'`
- `pg_messaging_core` role has SELECT on `witsaba.devices` — no schema changes needed
- Frontend uses Qwik's `routeLoader$` for SSR-fetch, graceful error state in component

## Commit plan
1. `feat(devices): add ListActive to repository interface + Pgx implementation` — f4e91ad
2. `feat(api): wire HTTP server + GET /api/devices/active into messaging-core` — f4e91ad
3. `test(devices): add unit tests for ListActive and HTTP handler` — f4e91ad
4. `feat(ui): add /devices route page fetching from API` — cc00eee
5. `docs: document API_PORT in env.example and messaging-core README` — f4e91ad
