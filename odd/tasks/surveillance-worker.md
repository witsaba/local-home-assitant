# Feature: periodic surveillance capture for `services/workers/`

## Goal

Add a new periodic job to `services/workers/` that, every 15 minutes,
finds every online witsaba camera, calls `GET /capture` on each one,
and saves the resulting JPEG to `$HOME/.witsaba/cameras/<YYYY-MM-DD>/`.

For captures that occur between 17:45 and 05:45 local time, the worker
appends `?flash=1` to the request URL so the firmware's pre-charge
logic kicks in. Outside that window the URL is unmodified. The window
is computed locally on the Pi; the firmware never sees the clock.

## Design decisions (locked)

| Decision | Choice | Rationale |
| --- | --- | --- |
| Camera source | **Hybrid: Postgres + freshness filter** | Read from `witsaba.devices`, skip rows whose `last_seen_at` is older than 5 minutes. The discovery job (every 60 s) keeps the table fresh; the worker doesn't re-scan subnets |
| Job interface | `worker.Job` (Name / Interval / Run) | Reuses the existing scheduler; no scheduler changes |
| Interval | `15 * time.Minute` (configurable via `SURVEILLANCE_INTERVAL_MINUTES`) | User-specified; matches the "no extend" constraint |
| Capture timeout | 10 s per camera (`SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS`) | LAN is fast; slow response = something is wrong and we should not block the next tick |
| HTTP client | One fresh `http.Client` per tick with `DisableKeepAlives: true` | Mirrors `probe/whoami.go`; matches existing worker patterns |
| Concurrency | Sequential across cameras inside one tick | 3 cameras × ~1 s = ~3 s per tick. Bounded semaphore can be added later if fleet grows |
| JPEG storage | `$HOME/.witsaba/cameras/<YYYY-MM-DD>/<HH-MM-SS>_<mac>.jpg` | One folder per day, one file per camera per tick. Path constants configurable via `SURVEILLANCE_ROOT_DIR` (default `~/.witsaba/cameras`) |
| Flash window | `flash := h > 17 \|\| h < 5 \|\| (h == 5 && m < 45)` | Strict window per user direction; the firmware is unaware of the window |
| Failure handling | One camera fails → WARN log, continue to next. All fail → ERROR log, return `nil` from `Run()` so the scheduler keeps ticking | Per-camera error is not a job error |
| Repository | Existing `devices.Repository` (no schema change) | The discovery job already writes the table; the surveillance job only reads |
| Event / consumer channel | **None.** Surveillance does not emit `DiscoveryEvent`. It uses an internal `chan captureResult` drained inside `Run()` and writes files directly | Keeps the workers' shared event channel clean of non-discovery events. The consumer in `jobs/discovery` is not modified |

## Layout

```
internal/jobs/surveillance/
    surveillance.go      ← the worker.Job
    capture.go           ← HTTP GET /capture per camera + clock window
    storage.go           ← mkdir + write JPEG to disk
    types.go             ← CaptureRequest, CaptureResult, Window struct
    *_test.go            ← unit tests (fake clock, fake HTTP transport, fs in tmp)
```

`internal/worker/job.go` is **not modified** — the existing interface
fits the new job without changes.

## Clock window helper

```go
// internal/jobs/surveillance/capture.go
func shouldFlash(now time.Time) bool {
    h, m := now.Hour(), now.Minute()
    return h > 17 || h < 5 || (h == 5 && m < 45)
}
```

`now` is read with `time.Now()` (Pi local time) — no timezone plumbing
needed. The Pi's `/etc/localtime` is the source of truth. If a future
operator runs the worker outside the Pi, they set `SURVEILLANCE_TZ=`
to override (out of scope for this PR).

## Capture flow inside `Run()`

```
1. t0 = now
2. If !shouldFlash(t0): flash = false; else flash = true
3. Query devices repo for cameras where last_seen_at > now - 5min
4. If 0 cameras: log "no devices online", return nil
5. For each device:
       url := fmt.Sprintf("http://%s/capture", device.SourceIP)
       if flash { url += "?flash=1" }
       resp := client.Get(url, timeout=10s)
       If !200 or read err: log WARN, continue
       dst := $HOME/.witsaba/cameras/<YYYY-MM-DD>/<HH-MM-SS>_<device.MAC>.jpg
       os.MkdirAll(filepath.Dir(dst), 0o755)
       os.WriteFile(dst, body, 0o644)
       log INFO "captured mac=... bytes=... flash=..."
6. Log "tick complete cameras=N flash=Y ok=K err=E"
7. Return nil
```

## Config additions

| Env var | Default | Used for |
| --- | --- | --- |
| `SURVEILLANCE_INTERVAL_MINUTES` | `15` | Job interval |
| `SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS` | `10` | Per-camera HTTP timeout |
| `SURVEILLANCE_DEVICE_FRESHNESS_MINUTES` | `5` | Skip devices not seen within this window |
| `SURVEILLANCE_ROOT_DIR` | `~/.witsaba/cameras` | Where the JPEGs land (per-day subdirs) |

`SURVEILLANCE_FLASH_HOUR_START` and `SURVEILLANCE_FLASH_HOUR_END` are
**not** added — the 17:45–05:45 window is a build-time constant per
the "no extend" constraint.

## Files touched

| File | Change |
| --- | --- |
| `internal/infrastructure/config/config.go` | Add the four env vars above + validation |
| `internal/infrastructure/devices/store.go` | Add `ListFresh(ctx, olderThan)` method (SELECT ... WHERE last_seen_at > $1) |
| `internal/infrastructure/devices/devices.go` | Add `ListFresh` to the `Repository` interface |
| `internal/jobs/discovery/...` | **Not modified** |
| `cmd/workers/main.go` | Build the new job, append to `[]worker.Job{discoveryJob, surveillanceJob}`, wire repo access to the new job (via a setter) |
| `services/workers/Dockerfile` | Verify `HOME` env still resolves to a writable path for the container user |

New files: `internal/jobs/surveillance/{surveillance.go,capture.go,storage.go,types.go}` + tests.

## Tasks

- [ ] T1 — `internal/jobs/surveillance/types.go`: `CaptureRequest`,
  `CaptureResult`, `Window` struct. `internal/clock` seam interface for
  testability (`type Clock interface { Now() time.Time }`).
- [ ] T2 — `internal/jobs/surveillance/capture.go`: `shouldFlash(now)`,
  `captureOne(ctx, httpClient, device, flash, timeout)`, HTTP client
  builder with `DisableKeepAlives: true`.
- [ ] T3 — `internal/jobs/surveillance/storage.go`: `ensureDayDir(root, day)`,
  `pathFor(root, day, mac, when)`, `writeFile(path, body)`.
- [ ] T4 — `internal/jobs/surveillance/surveillance.go`: `Job` struct,
  `NewJob(interval, repo, root, captureTimeout, flashWindowStart, flashWindowEnd,
  logger, clock, httpClient)`, `Run(ctx, emit)` that lists fresh devices,
  calls `captureOne` for each, calls `storage.writeFile`, logs result.
  Setter seams: `SetRepo`, `SetHTTPClient`, `SetClock`.
- [ ] T5 — `internal/jobs/surveillance/*_test.go`: unit tests for
  `shouldFlash` (boundary cases), `captureOne` (httptest server
  returning JPEG bytes + 503), `storage` (tmp dir + verify file
  written), `Job.Run` with a fake repo + fake clock + fake HTTP server.
- [ ] T6 — `internal/infrastructure/devices/store.go`: add `ListFresh(ctx, olderThan)`
  returning `([]types.DiscoveryEvent, error)`. Add to `Repository` interface.
  Add noop implementation in `noop.go`.
- [ ] T7 — `internal/infrastructure/config/config.go`: add the four env
  vars with defaults, validate them.
- [ ] T8 — `cmd/workers/main.go`: instantiate the new job, append to
  `[]worker.Job{}`, log the new settings.
- [ ] T9 — `go build ./...`, `go vet ./...`, `go test -race ./...` all
  pass on linux/amd64 and darwin/arm64.
- [ ] T10 — Manual sanity check: run the worker locally against a fake
  `http.Server` on `:18080`, verify files land in `$HOME/.witsaba/cameras/<today>/`
  with the right names.

## Acceptance criteria

- `go build ./...` exits 0 on linux/amd64.
- `go test -race ./...` exits 0, no data races.
- A tick that finds 3 fresh cameras writes 3 files
  (`<HH-MM-SS>_<mac>.jpg`) into today's folder under `~/.witsaba/cameras/`.
- A tick that finds 0 fresh cameras does not write anything and logs
  "no devices online" at INFO.
- A capture where the camera returns 503 or times out produces a WARN
  log and the next camera is still tried.
- Files persist across worker restarts (the directory layout is
  idempotent — `os.MkdirAll` + atomic `os.WriteFile`).
- The discovery job's interval, the existing consumer, and the Postgres
  schema are unchanged.

## Non-goals (explicit)

- No motion detection. No PIR trigger. No WebSocket consumption.
- No image post-processing (resize, watermark, EXIF).
- No upload to NAS / cloud / Telegram. Files only land in
  `~/.witsaba/cameras/`. The user said "do no extend for now this".
- No deletion policy. Old files accumulate forever.
- No index in Postgres. The filesystem is the index.
- No timezone plumbing. Pi local time is the source of truth.

## Risk / dependencies on other PRs

This PR is independent of `feat/camera-flash-capture` from a code
perspective — the worker can land and ship with `?flash=1` even if
the firmware doesn't honor it yet (the URL just produces a no-flash
frame). The two PRs should still be reviewed independently.
