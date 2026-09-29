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

- [x] **T1** Stream helper layer in `app.js` + `.stream-*` styles in `app.css`.
      Socket lifecycle, blob-URL frame painter with correct revoke ordering,
      bounded reconnect, status transitions. No page yet. → `28dc9cb`
- [x] **T2** `stream.html` viewer page. `?mac=` resolution, device picker
      fallback, viewer surface, status chip, live frame counter, stop control,
      single-viewer + `/capture` starvation notices. → `b9b16d8`
- [x] **T3** Entry points in `index.html` and `devices.html`, then run the
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

Delegated direct for T1 and T2; inline for T3. The multi-file write rule fired
for every task, but the delegation runtime **destroyed its worktree twice**
(once mid-run, once after reporting success), so after T2 the remaining work
moved inline. T3 is two small pages plus a few lines of CSS. Parent owns every
verification and every commit.

**Operational lesson for this repo:** commit each work unit immediately. T1
survived a post-hoc worktree wipe only because it was already committed; T2's
`stream.html` was untracked when the tree vanished and had to be recovered from
a `/tmp` copy. The blast radius of a sandbox wipe is bounded by how recently
you committed.

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

### Incident 2 — worktree wiped after the writer reported success

T2's writer reported `status: completed` with 20/20 checks and a clean
two-tree `git status`. The worktree then vanished entirely, taking the
untracked `stream.html` with it. The branch ref survived, so the worktree was
rebuilt with `prune --expire=now` + re-add, and the page was restored from the
`/tmp` copy taken beforehand. This is what motivated committing each work unit
immediately and moving T3 inline.

### T2 — stream.html viewer page ✅ (`b9b16d8`)

333-line page: `?mac=` → auto-connect-single → picker → empty state, with a
status chip, frame counter, stop button, and the two standing chip notices.

The writer's checker was **structural only** — ids, class names, JS style — and
passed 20/20 while the page had a defect that would have made it unusable. Four
real defects, all fixed:

1. `witsaba.onVisible(resolveAndStream, 10000)` plus a duplicate initial
   `resolveAndStream()` call meant `resolveAndStream` ran on a 10 s timer.
   Since it closes the open stream before opening the next, the socket was torn
   down and re-established every 10 seconds: the image blanks, the operator
   reloads, and the chip connection churns. The 10 s loop that is correct for
   `devices.html` is wrong for a live socket. Fixed — resolve runs once, and
   the tick is guarded by `if (controller) return`. Tab visibility is already
   owned by `witsaba.stream`, so the page must not also own it.
2. **Pressing Stop was undone within 10 s.** `closeStream()` nulls the
   controller, so the next tick saw "no stream" and reconnected. Fixed with an
   explicit `userStopped` flag, cleared only by an explicit open.
3. The picker path removed the standing single-viewer and `/capture` 503
   notices from the DOM, losing them exactly when several operators are about
   to contend for the same hardware.
4. A dead no-op expression in the option loop whose comment claimed it
   appended the node.

Defects 1 and 2 were found by a **behavioural** harness, not by reading alone
and not by the structural checker. Note also that three of the four initial
behavioural failures were bugs in the harness itself (asserting before the
`witsaba.api()` promise settled) — the same class of error as in T1, where
three of five failures were the harness, not the app.

### T3 — entry points ✅

`index.html` gains a fifth feature card linking to `/stream` (not
`aria-disabled`, unlike the Discovery/Logs/Settings placeholders) and a Stream
nav link. `devices.html` gains a per-row **View** deep link to
`/stream?mac=<mac>` plus its table header, and the nav link. The MAC is passed
through `encodeURIComponent` into the query string and read back with
`URLSearchParams`, so a device name from the LAN never becomes markup.
`app.css` gains `.device-table__action` / `.device-table__view` using only
existing tokens. Done inline, not delegated — see Route.

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

### T2 (observed on the dev machine, Node v26.10.0)

- Structural checker (writer's, re-run by parent): 20/20.
- Behavioural harness in `/tmp`: 21/21. Runs the real inline page script
  against a stub DOM and counts socket opens — connect-once-then-never-disturbed
  across three simulated 10 s ticks, Stop sticking, `?mac=` bypassing the device
  fetch, no auto-connect while choosing, stop disabled with zero devices, one
  option per device, and notice retention.
- T1 helper re-run after T2 landed: 28/28, so T2 did not regress T1.

### T3 (observed on the dev machine, Node v26.10.0)

- `bash scripts/install/test-scripts-load.sh` → **30 passed, 0 failed**.
- `bash scripts/install/test-nginx-config.sh` → 1 passed, **1 failed**, 2
  skipped. The failure is **pre-existing and environmental**, verified by
  running the identical script on the baseline `main` worktree and getting the
  same 1/1/2 result: `document root missing: ~/.witsaba/nginx/html`, plus nginx
  not installed and nothing listening on :4173. This machine has never had the
  Pi install run. Not caused by this feature, and not fixable here.
- Nav consistency: `index.html`, `devices.html` and `stream.html` each expose
  Home / Devices / Stream.
- Added-line scans across the three T3 files: 0 `innerHTML`; new CSS references
  only pre-existing tokens.

### Repository checks NOT run (and why)

- `scripts/test_ws_stream.py` — a device smoke test that needs real hardware
  and a reachable chip. Not runnable here.
- The full nginx suite beyond the above needs `~/.witsaba` and a running
  stack. **Run on the Pi.**

## Still pending (cannot be checked off this machine)

- `GET /stream` serving `stream.html` through nginx `try_files`, and
  `GET /stream?mac=<known>` painting real frames. nginx is not installed here,
  so the URL-space claim is reasoned from the config (`/stream` does not start
  with the `location /stream/` prefix, so it falls through to
  `try_files $uri.html`), not executed. **Verify on the Pi.**
- End-to-end frame painting against a real camera. Requires the Pi.
- `test-nginx-config.sh` is fully green only on a machine where the install
  has been run; on a fresh checkout it fails on the missing document root.

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
