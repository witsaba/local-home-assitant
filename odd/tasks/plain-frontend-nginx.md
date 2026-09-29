# Feature: Plain HTML/CSS/JS frontend served by nginx (user space)

## Goal

Replace the runtime footprint of the Qwik/Vite frontend on the Raspberry Pi with
a dependency-free static frontend served by nginx, and proxy `/api` and `/ws`
from the same origin so the browser makes no cross-origin request and
`messaging-core` needs no CORS change.

## Why (measured on the target Pi)

| Component | RAM | Disk |
|---|---|---|
| `vite preview` | **143 MB** | **308 MB** (`node_modules`) |
| Postgres (`shared_buffers=128MB`) | 80-150 MB | ~40 MB |
| messaging-core + workers | ~50 MB combined | 37 MB |
| OS baseline | 241 MB | — |

The frontend was the single most expensive process on a 899 MB machine, and it
serves two pages whose entire behaviour is:

- one `fetch` to `GET /api/devices/active`
- zero `useSignal` / `useTask$` / `useVisibleTask$` / `onClick$` / `onSubmit$`

`PRODUCT.md` also states the UI is viewed from another device on the LAN, never
on the Pi itself, so its Pi-side cost is pure overhead.

## Design decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Keep the Qwik scaffold in `frontend/web_ui/src` | Non-destructive. Deleting it and rewriting PRODUCT.md / DESIGN.md is a separate, deliberate change. |
| 2 | New static frontend at `frontend/web_ui/static` | Self-contained, no build step, no `node_modules`. |
| 3 | nginx via Homebrew, run unprivileged | `brew install nginx`, own config + prefix under `~/.witsaba/nginx`. No root, no port below 1024. |
| 4 | `daemon off` + `Type=simple` | systemd owns the process directly: no fork/PIDFile guesswork, honest restart semantics. |
| 5 | Single origin on `:4173` | `/api` and `/stream` proxy to `messaging-core`; static files served from disk. Zero CORS. |
| 6 | Replace the `witsaba-web-ui` unit | It ran `pnpm preview`, which is `qwik build preview && vite preview --open` — a full 1.5 min rebuild on every start, plus a browser launch on a headless box. |
| 7 | Port the existing design tokens verbatim | The rewrite must not look like a downgrade. Colours, text scale, radii and shadows carry over from `global.css`. |
| 8 | Keep `11-build-frontend.sh` as the opt-in Qwik path | Still useful on a workstation for framework work. Just not the Pi runtime. |

## Public surface

New:

```
frontend/web_ui/static/index.html            home: status + feature cards
frontend/web_ui/static/devices.html          active device list, polls the API
frontend/web_ui/static/assets/app.css        design tokens + components
frontend/web_ui/static/assets/app.js         fetch/polling/relative-time helpers
scripts/install/13-nginx.sh                  install nginx, generate config, deploy
scripts/install/test-nginx-config.sh         validate generated config + proxy
```

Changed:

```
scripts/install/12-systemd-services.sh   web-ui unit -> nginx unit
scripts/install/install-ubuntu.sh       add step 13; make 11 opt-in
scripts/install/uninstall-ubuntu.sh     stop removing a web-ui unit that no longer exists
```

## Tasks

- [x] 1. Static frontend: `app.css` (design tokens ported from `global.css`)
- [x] 2. Static frontend: `app.js` (api fetch, relative time, status helpers)
- [x] 3. Static frontend: `index.html` (home, four feature cards)
- [x] 4. Static frontend: `devices.html` (table, empty/error states, auto-refresh)
- [x] 5. `13-nginx.sh` — install, generate unprivileged config, deploy static files
- [x] 6. `test-nginx-config.sh` — `nginx -t`, asset presence, live proxy round-trip
- [x] 7. `12-systemd-services.sh` — nginx unit replaces web-ui
- [x] 8. Orchestrator + uninstall updates
- [x] 9. `README.md` for `scripts/install`
- [x] 10. Verify on the Pi: measure RAM, confirm proxy and WebSocket upgrade

## Out of scope

- Deleting the Qwik scaffold or rewriting PRODUCT.md / DESIGN.md
- The camera stream viewer. `messaging-core` already serves
  `GET /stream/{mac}` and it is ~20 lines of plain JS
  (`new WebSocket(...)`), so it is the natural next increment, not part of a port.
- TLS. The LAN is trusted; `cloudflared` was removed deliberately.
- `/discovery`, `/logs`, `/settings` — placeholder cards in the current UI, and
  they stay placeholder cards here.
- Node build tooling for the static assets. There is nothing to compile.

## Acceptance criteria

- `nginx -t` passes on the generated config, run unprivileged.
- `curl http://<pi>:4173/` returns the home page.
- `curl http://<pi>:4173/api/devices/active` is proxied to `messaging-core`
  and returns JSON, with no CORS involved.
- A WebSocket upgrade through `/stream/{mac}` reaches `messaging-core`.
- Total frontend runtime RAM is under 20 MB, versus 143 MB for `vite preview`.
- Removing `~/.witsaba/frontend` (the 308 MB Qwik tree) does not break serving.
- Every install script passes `bash -n` and `test-scripts-load.sh`.

## Closed-by commits

| # | SHA | Subject |
|---|-----|---------|
| 1 | `da92e9e` | feat(web): plain HTML/CSS/JS frontend served by nginx, user space |
| 2 | `4ac3197` | fix(systemd): drop capability directives from the user unit; make postgres start idempotent |
| 3 | `2d83da8` | fix(nginx): map extensionless URLs to .html files |

## Verified on the target

- 42 + 30 + 25 = 97 assertions pass across the three suites
- All five units `active` and `enabled`; `NRestarts=0` after a clean restart,
  and still 0 after 25s, so no crash-loop
- `GET /`, `GET /devices`, `GET /assets/app.css` all 200 from the LAN address
- `/api/devices/active` proxied to messaging-core, HTTP 200, JSON array
- `/stream/{mac}` upgrade: status, `Content-Type` and `Content-Length` identical
  to hitting `:8080` directly, so nginx is a transparent proxy
- nginx 8 MB RSS + 4 MB worker, versus 143 MB for `vite preview`
- Whole stack: postgres ~78 MB, messaging-core ~21 MB, workers ~17 MB,
  nginx ~12 MB = ~112 MB, on ~240 MB of OS baseline

## Follow-ups

- Camera stream viewer. `messaging-core` already serves `GET /stream/{mac}`
  and it is roughly 20 lines of plain JS; the proxy is verified and waiting.
- `/discovery`, `/logs`, `/settings` are still placeholder cards, as they were.
- Decide the fate of the Qwik scaffold and update `PRODUCT.md` / `DESIGN.md`,
  which both still describe a Node dev server on the Pi.
- `frontend/web_ui/Dockerfile` pins `NODE_VERSION=22.13.0`, below the
  `>=22.19.0` that `undici@8.11.2` requires, and
  `frontend/web_ui/package.json` claims `engines.node` allows Node 18/20. The
  container build has the same latent failure as the Node 20 path.
