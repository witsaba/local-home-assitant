# Feature: Camera stream viewer (static front-end)

## Goal

Add a live camera viewer page to the plain HTML/CSS/JS front-end that nginx
serves on the Pi, so an operator on the LAN can watch a witsaba camera in a
browser. Binary JPEG frames arrive over the existing
`GET /stream/{mac}` WebSocket gateway and are painted into an `<img>`.

## Why

`messaging-core` has served `GET /stream/{mac}` since #28, and `13-nginx.sh`
has proxied `/stream/` with `Upgrade` headers since #33. Both were built and
verified, and **nothing consumes them**. The previous feature closed with this
named as the next increment:

> Camera stream viewer. `messaging-core` already serves `GET /stream/{mac}`
> and it is roughly 20 lines of plain JS; the proxy is verified and waiting.
> — `odd/tasks/plain-frontend-nginx.md`, Follow-ups

So the missing piece is exactly one page plus a small helper layer. The Qwik
scaffold is paused, so this lands on the static front-end only.

## Confirmed existing behaviour (read, not assumed)

Read on `main` at `e75af53` before planning:

| Layer | File | Contract |
|---|---|---|
| Chip | `embedded/.../ws_cams.c` | `GET /ws/cams`, one complete JPEG per **binary** WS frame, hello is a **text** frame, single-viewer, ~10 fps |
| Server | `services/messaging-core/internal/infrastructure/wsserver/server.go` | `GET /stream/:mac` → upgrade; 404 if MAC absent from DB; pings every 50 s |
| Server | `.../streamhub/hub.go` | one chip connection per MAC, **fan-out to N browser viewers**, lazy chip connect, reconnect 1→16 s |
| Proxy | `scripts/install/13-nginx.sh` | `location /stream/` → `127.0.0.1:8080`, `Upgrade`/`Connection` set, `proxy_buffering off`, 3600 s timeouts |
| Data | `GET /api/devices/active` | `mac`, `name`, `fw`, `chip`, `last_source_ip`, `last_seen_at` |

Two operator-visible consequences of opening a stream, inherited from the
chip, not introduced here — both documented in the page:

1. The chip enforces **single-viewer**. `messaging-core` multiplexes many
   browsers onto one chip socket, so many browsers may watch at once.
2. While a stream is live, the chip's `/capture` is starved and returns
   **503** (5 s mutex budget). The page warns about this.

## Design decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | `/stream` is the page, `/stream/{mac}` is the socket | `location /stream/` only matches the trailing-slash prefix, so `/stream` falls through to `try_files $uri $uri.html` and serves `stream.html`. No nginx change needed. Collapsing the two would require editing the proxy. |
| 2 | `?mac=` query param, with an in-page picker as fallback | Bookmarkable and shareable; a bare `/stream` still works for the single-camera case. |
| 3 | Blob-URL frames, **not** base64 data URIs | `data:` URIs grow the string ~33% and the browser keeps every one alive. Blob URLs are revoked on swap, so a 10 fps feed holds one frame, not a growing heap. |
| 4 | Revoke the previous blob URL *before* assigning the new `src` | The naive `img.src = createObjectURL(...)` leaks one URL per frame; at 10 fps that is 36 000 live blobs/hour. |
| 5 | `close()` on the socket also revokes the in-flight blob | Stopping must not strand the last frame's URL. |
| 6 | Bounded exponential reconnect (1→16 s, cap attempts) | Mirrors the server's own policy, so a dead chip is retried like a dead proxy rather than hot-looping. |
| 7 | Reconnect only while the page is visible | `app.js` already pauses polling on `visibilitychange`; a hidden tab should not hold a chip socket open and starve `/capture`. |
| 8 | Reuse `witsaba.el` / `setStatus` / `showState` | Keeps the page consistent with `index.html` and `devices.html`, and inherits the existing no-`innerHTML` discipline. |
| 9 | `textContent` only for anything from the LAN | `app.js` already forbids `innerHTML`; a device name from `witsaba.devices` must not become markup. |
| 10 | No `scripts/install/13-nginx.sh` change | Explicitly out of scope (see below). The one consequence is logged as a follow-up. |

## TDD / verification mode

| Field | Value |
|---|---|
| Mode | **off** |
| Source | No test runner covers `frontend/web_ui/static/`. `package.json`'s `vitest run` targets the paused Qwik `src/` tree only; test presence does not enable TDD. |
| Runner | n/a — structural + functional checks instead |

## Public surface

New:

```
frontend/web_ui/static/stream.html     live viewer page
```

Changed:

```
frontend/web_ui/static/assets/app.js   + witsaba.stream helper namespace
frontend/web_ui/static/assets/app.css  + .stream-* component styles
frontend/web_ui/static/index.html      camera card now links somewhere real
frontend/web_ui/static/devices.html    per-row "View" action -> /stream?mac=
```

Not touched: `embedded/iot_cams/**`, `services/messaging-core/**`,
`scripts/install/**`, the Qwik `src/` tree.

## Tasks

- [ ] **T1** Stream helper layer in `app.js` + `.stream-*` styles in `app.css`.
      Socket lifecycle, blob-URL frame painter with correct revoke ordering,
      bounded reconnect, status transitions. No page yet.
- [ ] **T2** `stream.html` viewer page. `?mac=` resolution, device picker
      fallback, viewer surface, status chip, live frame counter, stop control,
      single-viewer + `/capture` starvation notices.
- [ ] **T3** Entry points in `index.html` and `devices.html`, then run the
      repository's own checks and close the feature document.

## Acceptance criteria

- `GET /stream` returns the viewer page through nginx `try_files`.
- `GET /stream?mac=<known>` connects to `/stream/<mac>` and paints frames.
- Binary frames paint; the chip's text hello frame does **not** paint (it is
  JSON, not JPEG) and does not throw.
- Previous blob URL is revoked on every swap and on close — verified by
  instrumented count, not by inspection.
- A MAC absent from the DB surfaces a real error, not a blank frame.
- A closed socket retries with backoff and stops when the tab is hidden.
- `bash -n` clean on touched shell; `test-nginx-config.sh` still green.
- No `innerHTML` **added**. Note: the baseline `app.js` already contains the
  word once, in a comment inside `witsaba.el`'s doc block, so a whole-file
  `grep` can never return empty. The criterion is scoped to added lines.
- No raw hex colour, bare px radius, or unitless spacing **added**. The `:root`
  token block is baseline and is full of hex by definition, so the criterion is
  scoped to added lines.

## Out of scope

- **Front-light (flash LED) control.** Explicitly declined. No GPIO 4 driver,
  no `POST /light`, no messaging-core proxy route.
- `scripts/install/13-nginx.sh`. The page deploys automatically because the
  step copies the whole `static/` tree, but `= /stream.html` gets no
  `no-store` rule the way `= /index.html` and `= /devices.html` do. Logged as
  a follow-up rather than widening scope.
- Snapshot, recording, multi-camera grid, PiP, motion overlay, FPS control.
  The chip has no `{"cmd":"stream"}` control plane; inbound frames are dropped.
- Touching or resuming the Qwik scaffold, `PRODUCT.md`, or `DESIGN.md`.

## Delivery forecast

~400 authored changed lines across 3 work-unit commits (T1 ~180, T2 ~180,
T3 ~45) — each commit is well under the ~400-line review heuristic.
Strategy: `single-pr`, since the per-commit load is small and no PR exists yet.
If the accumulated branch later trips the ~400-line delivery budget, split
before opening the PR.

## Route

Delegated direct. T1 and T3 each touch 2+ non-trivial files, which fires the
multi-file write rule; T2 is a single new file but is delegated in the same
batch for a consistent voice. Parent owns verification and every commit.

## Progress

### Incident — worktree lost mid-delegation (recovered, no data loss)

T1's first delegation was cancelled by its own sandbox: the feature worktree
was reduced to only the files the writer was editing. `.git` and every other
tracked file were removed. Detected while the writer was still running, so it
was cancelled immediately.

Recovery, in order: preserved the three surviving files to `/tmp`; removed the
orphan directory; cleared the stale admin dir with
`git worktree prune --expire=now`; re-added the worktree on the intact
`feature/camera-stream-ui` ref; restored the files. `git fsck` clean, 196 files
present, main worktree never contaminated.

Root cause of the recovery difficulty: **`git worktree prune` honours
`gc.worktreePruneExpire`, which defaults to 3 months**, so it silently refuses
to drop a minutes-old stale entry. `--expire=now` is required. Worth
remembering for any future worktree recovery in this repo.

### T1 — stream helper layer + styles ✅

`app.js` gains `witsaba.stream.open({mac, image, onStatus, onFrame, onError})`
returning `{ close() }`; `app.css` gains the 9 `.stream-*` classes. The writer
was cancelled **before** it ran its own harness, so its output was unverified
on arrival. Reading it surfaced four real defects, all fixed:

1. `handleMessage` routed every frame through `FileReader`, making painting
   asynchronous and unbounded — several callbacks in flight at 10 fps could
   paint out of order, and Blob→ArrayBuffer→Blob was a pointless round trip.
   Fixed: socket opens with `binaryType = "arraybuffer"` and `paintFrame` runs
   synchronously, which also gives natural backpressure.
2. `onerror` **and** `onclose` both called `attemptReconnect()`. A real failed
   socket fires both, so one drop consumed two rungs and exhausted the
   1/2/4/8/16 s ladder twice as fast. Fixed: `handleClose` is the single
   reconnect trigger.
3. `"live"` was emitted on socket open, so a viewer reported live while showing
   nothing. Fixed: emitted by `paintFrame` on the first frame that lands.
4. `buildWsUrl` had a hardcoded `"127.0.0.1:8080"` fallback, contradicting the
   same-origin rule. Fixed: throws when there is no page origin.

The writer also left two **brief** defects, not code defects, that were my own
fault and are now corrected in the doc: the `innerHTML` and raw-hex criteria as
originally written were unsatisfiable against untouched baseline files.

## Verification evidence

### T1 (observed on the dev machine, Node v26.10.0)

- `node --check frontend/web_ui/static/assets/app.js` → exit 0
- Instrumented harness (28 assertions, all pass), run from `/tmp`, not
  committed: blob revoke accounting over 10 frames (10 create / 9 revoke while
  live / 0 stranded after close), idempotent close, text frames never paint,
  `"live"` deferred to first frame, one drop consumes exactly one ladder rung,
  ladder walks 1→2→4→8→16 s and is capped with no hot-loop, successful open
  resets the ladder, hidden tab opens no socket, visible tab connects,
  `binaryType === "arraybuffer"`, same-origin URL derivation, `https` → `wss`.
- Added-line scans: 0 new `innerHTML`; 0 raw hex or bare spacing; all 9
  `.stream-*` classes present; `prefers-reduced-motion` respected.

### Still pending (cannot be checked off this machine)

- `GET /stream` serving `stream.html` through nginx `try_files` — nginx is not
  installed on this machine, so the URL-space claim is reasoned from the config,
  not executed. **Verify on the Pi.**
- End-to-end frame painting against a real camera. Requires the Pi.

## Follow-ups

- The instrumented harness that proves the blob lifecycle is a real asset and
  currently lives only in `/tmp`. There is no test directory for `static/` and
  adding one is outside the scope the user chose. Worth deciding before close:
  commit it somewhere, or accept that the next change to `witsaba.stream` is
  unguarded.
- `= /stream.html` needs a `no-store` rule in `13-nginx.sh` to match
  `= /index.html` / `= /devices.html`; otherwise a redeploy can leave a
  browser on a stale shell.
- The chip's `/ws/cams` drops inbound frames, so runtime FPS/resolution
  control needs the `{"cmd":"stream"}` plane (deferred in
  `odd/tasks/ws-cams-endpoint.md`).
