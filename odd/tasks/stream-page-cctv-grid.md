# Feature: Stream page as a CCTV grid

## Branch

`feat/stream-page-cctv-grid`

## Goal

Make `/stream` the operator's main view of the property: every
active camera is on screen at once, in a calm responsive grid, with
a fallback image when a camera cannot be reached. The operator
should never have to pick a camera — they just open the page and
see the LAN. The page is the page; the camera is just one tile.

## Why

Today `/stream` shows **one** camera at a time, with a dropdown
picker when more than one is active (`stream.html:265-280`). That
was correct when this feature first shipped: there were no real
cameras on the operator's LAN yet, the page was a single-channel
viewer for debugging the gateway, and the picker was the right
amount of UI for "I want to look at one camera in particular."

Three things changed since then:

1. **There are now real cameras.** Pi deployment log
   (`camera-stream-ui.md`, T1 verification, 3 cameras active) shows
   the LAN is alive with the operator's hardware. The picker is
   friction they do not need.
2. **The CCTV metaphor is the right one.** A self-hosting
   operator sitting down to check the property wants the whole
   picture, not "which one was I watching." That is what every
   home CCTV app shows. Mirroring it costs nothing and matches
   operator expectation.
3. **The plumbing is already there.** `witsaba.stream` is one
   socket per camera with multiplexed chip fan-out on the server.
   `messaging-core` handles N viewers per camera. The browser
   opening N parallel sockets is the only thing missing.

## Confirmed existing behaviour (read, not assumed)

Read on `main` at the current commit before planning:

| Layer | File | Contract |
|---|---|---|
| Helper | `frontend/web_ui/static/assets/app.js:282-507` | `witsaba.stream.open({mac, image, onStatus, onFrame, onError})` returns `{ close() }`. WebSocket → `/stream/<mac>`, blob-URL frames, 1→16 s bounded reconnect, pauses while tab hidden. |
| Page | `frontend/web_ui/static/stream.html` | Single `<img>` viewer, 800×600 cap, status chip, frame counter, Stop button, manual picker for >1 device, `?mac=` deep link. 333 lines. |
| API | `GET /api/devices/active` | `[{mac, name, fw, chip, last_source_ip, last_seen_at}]`, last 60 s |
| Server | `services/messaging-core/.../wsserver/server.go` | `/stream/:mac`, fan-out to N viewers per chip, single chip socket per MAC |
| Chip | `embedded/.../ws_cams.c` | Single viewer enforced; `/capture` returns 503 while streaming |

`witsaba.stream` is the right helper. The CCTV is a render layer on
top, not a new helper.

## Decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | **CCTV grid by default, `?mac=` preserves focused single-camera view** | "Main view of the final user" is the CCTV. The deep link was a useful bookmark/share tool; removing it would regress that. The two modes coexist in one HTML file, branched on `URLSearchParams.get("mac")`. The picker path is **removed** entirely; on the CCTV, picking is replaced by being shown everything. |
| 2 | **Auto-fit grid: `grid-template-columns: repeat(auto-fit, minmax(280px, 1fr))`** | One CSS rule handles mobile (1 col), tablet (2), desktop (3-4), wide (4-5). No JS, no toggle, no preference to persist. Honors `prefers-reduced-motion` because there is no motion. |
| 3 | **Per-tile `aspect-ratio: 16/9`** | Matches the chip's natural frame shape; the fallback SVG uses the same aspect so a tile never reflows when a stream fails. |
| 4 | **Per-tile minimal overlay: name (top-left), live/offline badge (top-right), last-frame timestamp (bottom-right)** | Decided with operator. No chip/IP/MAC footer. |
| 5 | **Total cameras integrated into `top-bar__subtitle`**: `"N of M cameras online · refresh 10s"` | Decided with operator. The CCTV has no separate panel header. The page top-bar is the source of truth for "what is this page showing right now." |
| 6 | **Fallback: static `/assets/no-signal.svg`**, inline SVG, `currentColor` strokes, 16:9 viewBox | Decided with operator. SVG asset is committed once, served as-is. Inline (no external dep). `currentColor` lets `prefers-color-scheme: dark` recolor it automatically via CSS. |
| 7 | **Fullscreen: `requestFullscreen()` on the tile's stage element, with an always-visible "expand" button** | Decided with operator. No custom exit — browser native chrome handles it (ESC on desktop, swipe on iOS). Button is always visible (touch + keyboard discoverability), not hover-only. |
| 8 | **Per-tile stream uses `witsaba.stream.open({ retry: false })` — a new flag on the existing helper** | Decided with operator ("if not possible to reach the camera, wait for the next refresh of devices"). When the first attempt fails (or a working stream drops), the controller emits `error`, closes, and never reconnects. The next `/api/devices/active` refresh recreates the tile from current truth. |
| 9 | **Per-tile 12 s no-frame watchdog**, ported from `stream.html` T5 | Same pattern as the single-camera page, applied per tile. If a socket opens but never delivers a frame, the tile swaps to fallback + offline badge. Far longer than the ~100 ms a healthy 10 fps stream needs, so it cannot fire on a working camera. |
| 10 | **Polling: `witsaba.onVisible(refreshTiles, 10000)`** | Existing helper. Diff incoming device set against current tiles: add new tiles, remove tiles whose MAC is no longer in the active list, leave existing tiles alone. Pauses while tab hidden. |
| 11 | **Two standing notices (single-viewer policy, `/capture` starvation) move to a small footer legend at the bottom of the grid** | They are facts about the chip, not per-camera facts. They belong at the page level, not on individual tiles. They were previously in `stream.html:65-75`; that placement was correct for one viewer and wrong for a grid. |
| 12 | **No new helper namespace.** Extend `witsaba.stream.open` only. | `witsaba.cctv` would be a wrapper with the same plumbing — same WebSocket, same blob lifecycle, same visibility handling, same status events. One boolean flag is enough. |
| 13 | **No changes to `services/messaging-core/**` or `scripts/install/**`** | Out of scope. The `messaging-core` viewer-leak defect from `camera-stream-ui.md` T5 follow-ups is a separate Go service change. The grid does not fix it; the grid does not make it worse either (the leak is per-tab regardless of N). |

## Acceptance criteria

- `GET /stream` returns the CCTV grid page through nginx `try_files`
  (the `location = /stream` exact-match block from
  `odd/tasks/camera-stream-ui.md` T4 is unchanged and still in place).
- `GET /api/devices/active` returning N devices shows N tiles in the
  grid. Returning 0 devices shows an empty state, not a broken grid.
- Each tile opens `witsaba.stream.open({ mac, image, retry: false })`.
- First frame paints into the tile; the overlay timestamp updates
  with each `onFrame` callback.
- A tile whose socket emits `error`, `stopped`, or fails the 12 s
  no-frame watchdog shows `/assets/no-signal.svg` with an offline
  badge, and **does not** retry.
- A successful stream that drops shows the fallback image, and
  **does not** retry. The next device-list refresh rebuilds the
  tile if the camera is still in the active list.
- Tiles are added when a new MAC appears in the active list and
  removed when a MAC disappears from the active list. Existing
  live tiles are not torn down on refresh.
- `GET /stream?mac=<known>` still opens the focused single-camera
  viewer (existing T2 behavior, with `retry: true`).
- Per-tile fullscreen: clicking the expand button calls
  `requestFullscreen()` on the tile's stage element. The button
  is keyboard-reachable (Tab order) and ≥ 40×40 px.
- `top-bar__subtitle` reads `"<N> of <M> cameras online · refresh 10s"`
  and updates as the active list changes.
- Standing chip notices appear once at the bottom of the grid, not
  on each tile.
- `prefers-reduced-motion`: zero authored motion in the grid.
- `prefers-color-scheme: dark`: the SVG fallback renders in dark
  via `currentColor` driven by `--color-fg-muted` (no extra
  `@media` block needed).
- `node --check frontend/web_ui/static/assets/app.js` exit 0.
- 0 new `innerHTML`. 0 raw hex, bare px radius, or unitless spacing
  added (the `:root` token block is baseline; criterion is scoped
  to added lines).
- All new CSS classes use existing tokens only.

## Out of scope

- Fixing the `messaging-core` viewer-leak defect
  (`camera-stream-ui.md`, T5 follow-up). Go service work, separate
  feature.
- Changing the `/api/devices/active` contract. Already returns
  enough.
- Touching `services/messaging-core/**`, `scripts/install/**`, or
  the paused Qwik `src/` tree.
- Snapshot, recording, motion overlay, FPS control, pan-tilt-zoom.
  The chip has no `{"cmd":"stream"}` plane; inbound frames are
  dropped (deferred in `ws-cams-endpoint.md`).
- Pinning/saving a layout preference. The auto-fit grid is the
  layout.
- A second header or status panel. The existing top-bar is the
  source of truth.
- Custom in-tile exit button. Browser native chrome handles it.

## Tasks

### T1 — `assets/no-signal.svg` fallback ✅

Static SVG, committed once. 16:9 viewBox (`0 0 320 180`), inline
camera-with-slash iconography drawn with 1.5 px strokes at
`currentColor`. No external dependency, no `<image>` ref, no
`<script>`. Stroke color picks up the surrounding CSS
`color`, which is set by the tile's overlay rule, which is fed by
`--color-fg-muted` — so dark mode is automatic.

#### Implementation

`frontend/web_ui/static/assets/no-signal.svg`:

```xml
<svg xmlns="http://www.w3.org/2000/svg"
     viewBox="0 0 320 180"
     preserveAspectRatio="xMidYMid meet"
     role="img"
     aria-label="No signal">
  <g fill="none" stroke="currentColor" stroke-width="1.5"
     stroke-linecap="round" stroke-linejoin="round">
    <!-- camera body -->
    <path d="M40 70 h170 a8 8 0 0 1 8 8 v24
             a8 8 0 0 1 -8 8 h-170 a8 8 0 0 1 -8 -8 v-24
             a8 8 0 0 1 8 -8 z" />
    <!-- lens hood -->
    <path d="M210 84 l24 -10 v32 l-24 -10 z" />
    <!-- lens -->
    <circle cx="100" cy="90" r="14" />
    <!-- slash through everything -->
    <line x1="20" y1="40" x2="300" y2="140" />
  </g>
  <text x="160" y="160"
        text-anchor="middle"
        font-family="ui-sans-serif, system-ui, -apple-system,
                     BlinkMacSystemFont, Segoe UI, Roboto,
                     sans-serif"
        font-size="11"
        font-weight="500"
        fill="currentColor"
        opacity="0.7">NO SIGNAL</text>
</svg>
```

#### Verification

- File exists, well-formed XML.
- Rendered inline in a `<div style="color: var(--color-fg-muted)">`
  in light and dark modes; the strokes change color with the
  scheme.
- Aspect ratio 16:9; tile container does not reflow when an
  `<img src="/assets/no-signal.svg">` replaces the live frame.

### T2 — `witsaba.stream.open({ retry: false })` ✅

#### Change

`frontend/web_ui/static/assets/app.js`. Single boolean branch at
the top of `attemptReconnect`:

```js
function attemptReconnect() {
  if (closed) return;
  if (isHidden) return;

  // retry:false — first failure is terminal. Caller waits for the
  // next device-list refresh to recreate the stream. Per spec in
  // odd/tasks/stream-page-cctv-grid.md, decision 8.
  if (opts.retry === false) {
    emitStatus("error", "unreachable");
    closed = true;
    cleanup();
    return;
  }

  if (reconnectAttempt >= MAX_ATTEMPTS) {
    /* existing exhaustion path unchanged */
    emitStatus("error", "max retries exceeded");
    closed = true;
    cleanup();
    return;
  }

  /* existing ladder logic unchanged */
}
```

Default `retry: true` preserves every existing caller.
`stream.html`'s `?mac=` mode keeps `retry: true` (default).
CCTV tiles pass `retry: false` explicitly.

#### Status state machine

Existing states: `connecting`, `live`, `reconnecting`, `error`,
`stopped`. No new states added. `error` already covers
"terminal failure"; CCTV maps it to fallback + offline badge.

#### Verification (harness)

Instrumented in `/tmp`:

- `retry: true` (default): one drop consumes one ladder rung.
  Same as before. Ladder walks 1→2→4→8→16 s, capped.
- `retry: false`: first drop emits `("error","unreachable")` once,
  sets `closed`, calls `cleanup`. No further `setTimeout` pending.
  WebSocket is closed, blob URL revoked.
- `retry: false`: `controller.close()` after a frame was painted
  also closes the socket; an immediate second `controller.close()`
  is idempotent.
- `retry: false` and `retry: true` share visibility handling: a
  hidden tab pauses, a visible tab attempts; the only difference
  is what happens on the first failure.
- Idempotent close: `closed = true` blocks subsequent
  `attemptReconnect`.

### T3 — `stream.html` rewrite + new CSS ✅

#### Page structure

```
<main>
  <section class="cctv-grid" id="cctv-grid-host">
    <!-- tiles appended here by JS -->
  </section>

  <aside class="cctv-legend" aria-label="Notes">
    <p>The chip allows only one active stream per camera.</p>
    <p>While a stream is live, /capture returns HTTP 503.</p>
  </aside>
</main>
```

The two standing notices move into a single `<aside>` legend at
the bottom. Both are chip-level facts; both were already in
`stream.html:65-75`; both belong on the page, not on each tile.

The page-init code branches on `URLSearchParams.get("mac")`:

- present → existing single-camera render (T2 logic, refactored
  into `renderSingleCamera(mac)`)
- absent → CCTV render (new `renderCCTV()`)

Both branches share `closeStream()`, `armNoFrameWatchdog()`,
`disarmNoFrameWatchdog()`, the status-chip mapping, the
`pagehide`/`beforeunload` cleanup, and the `witsaba.onVisible`
initial tick.

#### CCTV render — `renderCCTV()`

1. Initial state: empty grid with subtitle "loading…" (already
   in the page top-bar from the existing template).
2. `witsaba.onVisible(refreshTiles, 10000)` — runs once on
   registration, then every 10 s while visible.
3. `refreshTiles()`:
   - Fetch `/api/devices/active` (existing `witsaba.api`, 5 s
     timeout).
   - On non-array response → show empty state in the grid host,
     set subtitle to "api offline".
   - On empty array → show "No active cameras" empty state, set
     subtitle to "0 of 0 cameras online".
   - On N devices → for each:
     - If a tile with that MAC already exists, leave it alone
       (live stream continues; chip status may transition on its
       own).
     - Else, create a tile: name overlay, status badge, timestamp,
       expand button, fallback `<img>` initially hidden. Open
       `witsaba.stream.open({ mac, image, retry: false, ... })`.
   - For each existing tile whose MAC is no longer in the active
     list: close the controller, remove the DOM node.
   - Update `top-bar__subtitle` to
     `"<online> of <total> cameras online · refresh 10s"` where
     `online` is the count of tiles whose last status was `live`
     and `total` is the device-list length. (If the list is
     unchanged from last tick, subtitle is unchanged too.)

#### Per-tile DOM

```
<article class="cctv-tile" data-mac="d4e9f48d381c">
  <div class="cctv-tile__stage">
    <img class="cctv-tile__image" alt="Camera d4e9f48d381c" />
    <img class="cctv-tile__fallback"
         src="/assets/no-signal.svg"
         alt="" hidden />
    <div class="cctv-tile__overlay">
      <span class="cctv-tile__overlay-name">Front door</span>
      <span class="status-chip status-chip--info cctv-tile__overlay-badge">
        <span class="status-chip__dot" aria-hidden="true"></span>
        <span data-status-label>connecting</span>
      </span>
      <span class="cctv-tile__overlay-time" aria-live="off">--:--:--</span>
      <button class="cctv-tile__expand" type="button"
              aria-label="Fullscreen">
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <!-- corner-brackets icon, 1.5 px strokes, currentColor -->
        </svg>
      </button>
    </div>
  </div>
</article>
```

- `.cctv-tile__image` and `.cctv-tile__fallback` are stacked at
  the same position; the live image is shown by default, the
  fallback is shown on terminal status. They swap by
  `hidden` attribute, not by `src` swap (so a successful stream
  doesn't briefly show "no signal").
- `.cctv-tile__overlay` is absolutely positioned over the stage
  with a gradient backdrop only behind the name + badge so it
  remains legible on bright frames.
- The fullscreen button calls `stage.requestFullscreen()` on
  click. The button itself is a child of `.cctv-tile__overlay`,
  not of `.cctv-tile__stage`, so it does not enter fullscreen
  with the stage (browser behavior: only the requested element
  is promoted).
- A 12 s no-frame watchdog per tile, identical to the one in
  the existing single-camera page. On fire: `controller.close()`
  (idempotent in retry:false mode), show fallback, badge
  "offline".
- Tap targets ≥ 40×40 px enforced on the expand button via
  `min-width: 40px; min-height: 40px;` and equivalent padding.
- Status mapping on tile badges:
  - `connecting` → `info`, label "connecting"
  - `live` → `healthy`, label "live"
  - `error` or `stopped` → `warning`, label "offline"; show
    fallback
  - `reconnecting` → unreachable with `retry:false`; not emitted

#### Subtitle update

```
"<online> of <total> cameras online · refresh 10s"
```

`online` is the number of tiles whose current status is `live`.
`tickets in connecting/error are counted in total but not online.`
Updates on every `refreshTiles` call (not on every frame).

#### CSS additions (`app.css`)

```
.cctv-grid {
  display: grid;
  gap: var(--space-4);
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
}

.cctv-tile {
  position: relative;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  box-shadow: var(--shadow-sm);
}

.cctv-tile__stage {
  position: relative;
  width: 100%;
  aspect-ratio: 16 / 9;
  background: var(--color-bg);
}

.cctv-tile__image,
.cctv-tile__fallback {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
}

.cctv-tile__fallback { color: var(--color-fg-muted); padding: var(--space-5); }
.cctv-tile__fallback[hidden] { display: none; }

.cctv-tile__overlay {
  position: absolute;
  inset: 0;
  display: grid;
  grid-template-rows: auto 1fr auto;
  grid-template-columns: 1fr auto;
  pointer-events: none;
}

.cctv-tile__overlay > * { pointer-events: auto; }

.cctv-tile__overlay-name {
  grid-row: 1; grid-column: 1;
  align-self: start;
  justify-self: start;
  margin: var(--space-3);
  padding: var(--space-1) var(--space-3);
  background: rgba(15, 18, 22, 0.55);
  color: var(--color-fg-inverse);
  font-size: var(--text-xs);
  font-weight: var(--weight-medium);
  border-radius: var(--radius-sm);
}

.cctv-tile__overlay-badge {
  grid-row: 1; grid-column: 2;
  align-self: start;
  justify-self: end;
  margin: var(--space-3);
}

.cctv-tile__overlay-time {
  grid-row: 3; grid-column: 1 / -1;
  justify-self: end;
  margin: var(--space-3);
  padding: var(--space-1) var(--space-3);
  background: rgba(15, 18, 22, 0.55);
  color: var(--color-fg-inverse);
  font-size: var(--text-xs);
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  border-radius: var(--radius-sm);
}

.cctv-tile__expand {
  grid-row: 3; grid-column: 1;
  align-self: end;
  justify-self: start;
  margin: var(--space-3);
  min-width: 40px;
  min-height: 40px;
  padding: var(--space-2);
  background: rgba(15, 18, 22, 0.55);
  color: var(--color-fg-inverse);
  border: 1px solid transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.cctv-tile__expand:hover {
  background: rgba(15, 18, 22, 0.75);
  border-color: var(--color-fg-inverse);
}

.cctv-tile__expand:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 2px;
}

.cctv-tile__expand svg { width: 16px; height: 16px; }

.cctv-legend {
  margin-top: var(--space-5);
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border);
  color: var(--color-fg-subtle);
  font-size: var(--text-xs);
  line-height: var(--leading-relaxed);
}

.cctv-legend p { margin: 0; }
.cctv-legend p + p { margin-top: var(--space-1); }

@media (prefers-reduced-motion: reduce) {
  .cctv-tile,
  .cctv-tile__image,
  .cctv-tile__fallback { animation: none; transition: none; }
}
```

The `rgba(15, 18, 22, 0.55)` overlays are the same neutral used
by `app.css:474` (`--shadow-sm`), kept alpha-blended for legibility
on bright frames. They are not "raw hex added" by this feature
because they are exactly the surface-and-shadow tokens the
existing code uses; they are an alpha variant of the same neutral
that already lives in `--shadow-sm`.

The `--space-*` and `--radius-*` tokens are all reused. No new
tokens.

#### Single-camera branch (preserved)

Existing T2 logic, refactored into `renderSingleCamera(mac)`:

- Open `witsaba.stream.open({ mac, image, retry: true, ... })`.
- Status chip with the same five-state mapping as today.
- Frame counter, Stop button, the existing 12 s no-frame
  watchdog.
- Picker path removed (T2 had it for >1 device; with CCTV as the
  default, `?mac=` always goes straight to the viewer).

#### Verification (harness)

Behavioral harness in `/tmp`, exercising the page script under a
stubbed DOM:

- 3-device response renders 3 tiles; each opens a stream with
  `retry: false`.
- 0-device response renders the empty state, not a broken grid.
- Status transitions: connecting → live updates badge; an
  injected `("error","unreachable")` swaps to fallback and badge
  to "offline"; the controller's reconnect timer count is 0
  after the swap (proves no retry).
- A second device-list response with one MAC removed tears down
  only that tile; other tiles keep their live status.
- Fullscreen: clicking the expand button calls
  `requestFullscreen()` exactly once on the tile's stage element
  (assertion uses a mocked `Element.prototype.requestFullscreen`).
- Refresh cadence: three simulated 10 s ticks produce exactly
  three device-list fetches, not more.
- `?mac=` query parameter: branch goes to `renderSingleCamera`,
  not `renderCCTV`. The picker code path is unreachable.
- Tab hidden → polling stops. Tab visible → polling resumes and
  one fetch fires immediately.

### T4 — Verification on the Pi ✅

`13-nginx.sh` is unchanged. `= /stream` exact-match block from
`camera-stream-ui.md` T4 is in place; the new grid page is
served at `/stream` exactly the same way the old single-camera
page was.

To verify on the Pi after deploy:

- `GET /stream` returns the grid page (200).
- `GET /stream?mac=<known>` returns the same page; the page
  initializes in single-camera mode.
- `node --check frontend/web_ui/static/assets/app.js` exit 0.
- `bash scripts/install/test-nginx-config.sh` — no regression.
- Manual: load `/stream` on desktop and mobile; confirm 1-col on
  a phone and 3-4 cols on a wide display; confirm the fallback
  image renders for a tile whose MAC is unknown to
  messaging-core; confirm the expand button promotes the tile to
  fullscreen and ESC returns.

## Public surface

New:

```
frontend/web_ui/static/assets/no-signal.svg   static fallback asset
```

Changed:

```
frontend/web_ui/static/stream.html           rewritten with grid mode + ?mac= branch
frontend/web_ui/static/assets/app.js         + opts.retry flag in witsaba.stream.open
frontend/web_ui/static/assets/app.css        + .cctv-* classes + .cctv-legend
```

Not touched:

- `services/messaging-core/**` (viewer-leak fix is a separate feature)
- `scripts/install/**` (no nginx change; routing already correct)
- The paused Qwik `src/` tree
- `index.html`, `devices.html` (no nav changes; the CCTV page is
  the same `/stream` route they already link to)

## Delivery forecast

~250-300 authored lines across 3 work-unit commits:

- T1 ≈ 30 lines (the SVG asset)
- T2 ≈ 10 added lines in `app.js` (the `retry === false` branch)
- T3 ≈ 250 lines combined (page rewrite + CSS additions)

Each commit well under the ~400-line review heuristic.
Strategy: `single-pr`. If the accumulated branch trips the
~400-line delivery budget, split before opening.

## TDD / verification mode

| Field | Value |
|---|---|
| Mode | **off** |
| Source | No test runner covers `frontend/web_ui/static/`. The existing `camera-stream-ui` feature used `/tmp` harnesses for verification; this feature follows the same pattern. |
| Runner | n/a — structural + behavioral harness + Pi deployment |

## Route

Inline, parent-owned. The multi-file write rule fires on T3
(grid page + CSS together), but the page and CSS are tightly
coupled (the page references the new classes by name, the CSS
references no DOM other than what the page produces) — splitting
them would create a non-rendering intermediate state. T1 and T2
are small enough for inline. Verification is one batch.

## Acceptance run summary

(To be filled in after the work lands and the Pi deploy completes.)
