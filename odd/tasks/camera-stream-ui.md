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
| 1 | `/stream` is the page, `/stream/{mac}` is the socket — **but this needed an explicit `location = /stream`, see T4** | My original reasoning was WRONG. I claimed `/stream` does not start with `/stream/`, so it falls through to `try_files $uri.html` and serves `stream.html`. True for location *matching*, but nginx additionally 301s any URI that is the **stem of a prefix location** to its slash form, and that redirect happens **before** `try_files` runs. Verified on the Pi: `/api` and `/assets` both 301 for the same reason, while `/devices` (no matching prefix) serves 200. An exact `=` match beats a prefix match, so `location = /stream { try_files /stream.html =404; }` resolves it. |
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
- [x] **T4** nginx exact-match `location = /stream` + `= /stream.html`
      no-store, with regression tests. Found by deploying to the Pi.

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

### T4 — nginx routing fix, found by deploying to the Pi ✅

The whole feature was broken on the Pi and none of the local checks could see
it. `/stream` returned **301 → `/stream/` → 404**: the viewer page was
unreachable, and every nav link pointed at it. `stream.html` was deployed and
serving **200 at `/stream.html`** the whole time, simply never consulted.

Root cause, proven by measurement rather than inference:

| URI | has `location /x/`? | result |
|---|---|---|
| `/api` | yes | **301** → `/api/` (no `api.html` exists at all) |
| `/assets` | yes | **301** → `/assets/` |
| `/stream` | yes | **301** → `/stream/` |
| `/devices` | no | **200** via `try_files $uri.html` |
| `/discovery`, `/nope` | no | 404 |

So the rule is: **nginx 301s the stem of a prefix location to its slash form,
before `try_files` is consulted.** This is a consequence of `location /stream/`
existing, not of anything about the page. My T1 design note asserted the
opposite, and I had explicitly deferred proving it — "nginx is not installed on
this machine, so the URL-space claim is reasoned from the config, not executed.
**Verify on the Pi.**" That deferral is what let the error survive three
commits.

Fixed by adding an exact-match block, which outranks the prefix:

```
location = /stream {
    try_files /stream.html =404;
}
```

`scripts/install/13-nginx.sh` was explicitly out of scope when this feature
started; it is now in scope, because the feature does not work without it. The
same file also gained `location = /stream.html` with `no-store`, closing the
follow-up logged during T1 (a cached page shell can outlive the assets it
references after a redeploy).

Regression tests added to `scripts/install/test-nginx-config.sh`: a static
assertion that the exact-match block exists, plus live assertions that
`/stream` returns 200 *and* that `/stream/<mac>` still reaches the gateway
rather than being swallowed by the new block. The second one matters — an
over-broad fix would silently break the socket.

### Deployed and verified on the Pi (192.168.1.115, aarch64, 899 MB)

Deployed from `origin/feature/camera-stream-ui` @ `0ea17f4` via a fresh clone,
because the server's original `~/witsaba/local-home-assitant` checkout became
unlistable — `ls` showed it, `stat`/`cd` on the same path returned `ENOENT`.
The live install in `~/.witsaba` was never affected and stayed up throughout.

- `13-nginx.sh` → `nginx -t` successful, 7 files / 84K deployed. Then
  `reload.sh` — **required**, because T4 changed the config and the script
  deliberately does not reload. A reload keeps the master PID and start time,
  so confirm it by the new worker process, not by the master timestamp.
- All 10 routes HTTP 200: `/`, `/devices`, `/stream`,
  `/stream?mac=d4e9f48d381c`, `/stream.html`, `/assets/app.{js,css}`,
  `/favicon.svg`, `/healthz`, `/api/devices/active`.
- Deployed files byte-match the repo commit (md5 on all 5).
- `test-nginx-config.sh` on the Pi: **28 passed, 0 failed, 0 skipped**,
  including the new `/stream` and `/stream/<mac>` assertions. (On a machine
  where the install has never run it reports 1/1/2 because everything is
  skipped.)
- 3 cameras active, so the multi-device picker path is live.

**End-to-end stream, through nginx → messaging-core → chip:**

| MAC | Address | Frames | Avg payload | SOI/EOI | Distinct |
|---|---|---|---|---|---|
| `d4e9f48d381c` | 192.168.1.199 | 58 | 23,511 B | 58/58 | 58/58 |
| `c8f09e9d5008` | 192.168.1.48 | 59 | 65,394 B | 59/59 | 59/59 |

~10 fps, matching `CAM_STREAM_PERIOD_MS`. Handshake 101 through nginx in ~62 ms
with `Sec-WebSocket-Accept` verified.

The gateway relays **binary frames only** — `broadcastToViewers` writes
`BinaryMessage` and the chip's text hello is not forwarded. So
`scripts/test_ws_stream.py` (written for the chip, which does send a text
hello) fails on the gateway path with "first frame opcode 0x2 (expected TEXT
0x1)". That is a harness/target mismatch, not a product fault; the viewer page
paints binary frames and ignores text, which is exactly right here.

### Pre-existing defect found in messaging-core (NOT fixed here)

Exposed by actually using the feature. In
`services/messaging-core/internal/infrastructure/`:

1. **`DeregisterViewer` is never called in production code.** It is declared
   in the `StreamHub` interface (`wsserver/server.go:43`) and implemented
   (`streamhub/hub.go:158`), but the only callers are in `hub_test.go`. The
   comment at `wsserver/server.go:203` claims "the hub's read goroutine will
   notice and call DeregisterViewer" — **no such goroutine exists**.
   `handleStream` only calls `RegisterViewer` and `go pingLoop`.
2. **`pingLoop` cannot detect a dead client.** It sets a read deadline and a
   pong handler, but nothing ever reads, so the deadline is never evaluated
   and no pong can arrive. On ping-write failure it closes the conn without
   deregistering it.
3. **`broadcastToViewers` writes with no write deadline** (`hub.go:311`).

Observed consequence: `viewer_count` only ever increments (1 → 2 → 3, never
back to 0), so the chip connection is never closed, and a wedged write to a
dead viewer stalls the relay for **every** viewer of that camera — a second
client got 0 frames while the log looked healthy. `systemctl --user restart
witsaba-messaging-core` cleared it and streaming resumed immediately.

This is outside this feature's scope (`services/messaging-core/**` was
explicitly excluded, and the fix is Go service work plus tests). It needs its
own change: a read loop per viewer that deregisters on close, and a write
deadline on the relay.

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

## Notes and environment observations

- `test-nginx-config.sh` is fully green only where the install has been run; on
  a fresh checkout it fails on the missing document root and skips the rest.
  Both states are expected; run it on the Pi.
- The Pi's `engram.service` is crash-looping (`restart counter is at 7774`,
  `status=203/EXEC`) and `hermes-gateway.service` is failing. Unrelated to this
  feature, but worth the operator's attention.

## Follow-ups

- The instrumented harness that proves the blob lifecycle, and the behavioural
  harness for the page, are real assets currently living only in `/tmp`. There
  is no test directory for `static/` and adding one is outside the scope the
  user chose. Worth deciding before close: commit them somewhere, or accept
  that the next change to `witsaba.stream` is unguarded.
- **`messaging-core` viewer leak** (see above). Highest-value follow-up: one
  leaked viewer per page open, a chip connection that never closes, and a
  relay that can wedge for every viewer of a camera.
- `= /stream.html` no-store rule in `13-nginx.sh` — **done in T4**, no longer a
  follow-up.
- The chip's `/ws/cams` drops inbound frames, so runtime FPS/resolution
  control needs the `{"cmd":"stream"}` plane (deferred in
  `odd/tasks/ws-cams-endpoint.md`).
