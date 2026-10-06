# web_ui

The witsaba operator front-end: three static pages and one stylesheet and one
script. No framework, no build step, no `node_modules`.

## Layout

```
static/
  index.html      home: system status, feature cards
  devices.html    active device list, polls the API
  stream.html     live camera viewer for one device
  favicon.svg
  assets/app.css  design tokens and components
  assets/app.js   fetch/polling/relative-time helpers, witsaba.* namespace
```

## How it is served

`scripts/install/13-nginx.sh` copies `static/` into `~/.witsaba/nginx/html` and
then asserts that `index.html`, `devices.html`, `assets/app.css` and
`assets/app.js` are present. That assert list is the deployment contract: if
one of them is missing the script fails rather than starting nginx on a partial
tree. `scripts/install/12-systemd-services.sh` supervises nginx as
`witsaba-nginx.service` in user space.

To redeploy after editing a page, re-run `13-nginx.sh`. It replaces the contents
of the document root rather than the directory itself, because nginx may still
hold the old tree open and the running config references that path.

## One origin, no CORS

nginx serves the files and reverse-proxies the backend on a single port, so the
browser never makes a cross-origin request:

| Path | Goes to | Notes |
|---|---|---|
| `/` and other pages | files in `~/.witsaba/nginx/html` | served from disk |
| `/api/*` | `127.0.0.1:8081` | `messaging-core` REST |
| `/api/gallery/*` | `127.0.0.1:8082` | `workers` gallery (longest-prefix match wins) |
| `/stream/*` | `127.0.0.1:8080` | `messaging-core` WebSocket gateway |

The gallery upstream is a separate location block because nginx routes by
**longest matching prefix**, not by file order. `/api/gallery/` is longer
than `/api/`, so it reaches `workers` while every other `/api/` route keeps
going to `messaging-core`. The `workers` listener binds `127.0.0.1` only, so
it is never exposed directly — nginx is the sole LAN-facing door, and this
page is reachable from a phone on the local network.

Gallery endpoints (all on the `workers` listener):

| Method | Path | Answers |
|---|---|---|
| `GET` | `/api/gallery/days?from=&to=` | `[{date, count}]`, one entry per date including empty days |
| `GET` | `/api/gallery/day?date=` | `[{name, mac, time, bytes}]`, newest first |
| `GET` | `/api/gallery/img?date=&name=` | the original JPEG |
| `GET` | `/api/gallery/thumb?date=&name=` | a 320px thumbnail, generated on first request |
| `DELETE` | `/api/gallery/photo?date=&name=` | `204`, permanent |

Thumbnails cache under `~/.witsaba/thumbs/`, a sibling of the capture
archive. That location is not arbitrary: the `workers` unit sets
`ProtectHome=read-only` with `ReadWritePaths=$HOME/.witsaba`, so a cache
anywhere else under `$HOME` would be denied at write time.

The port is `WITSABA_HTTP_PORT`, default `4173`. `/stream/` carries the
`Upgrade` and `Connection` headers a WebSocket needs, and `proxy_buffering off`
keeps camera frames from being held back.

Extensionless page routes come from `try_files $uri $uri.html $uri/ =404`.
Because nginx
issues a trailing-slash redirect for the *stem* of a prefix location before
`try_files` runs, a page whose path is also a proxy prefix needs an exact
`location =` match. `/stream` is exactly that case.

## Editing rules

- No `innerHTML`. Anything from the LAN — a device name, a MAC — is written
  with `textContent`.
- Blob URLs painted from the stream must be revoked before the next `src` is
  assigned, and again on close. At 10fps a naive assignment leaks 36000 live
  blob URLs an hour.
- Polling loops that are correct for a device table are wrong for a live
  socket. `witsaba.stream` owns tab visibility for the viewer; the page must not
  also own it.

## Tests

None. `frontend/web_ui/static/` has no committed test suite. The behaviour
harnesses used while building the viewer live in `/tmp` and were never
committed. See the follow-ups in `odd/tasks/remove-qwik.md`.
