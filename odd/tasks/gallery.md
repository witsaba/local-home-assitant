# Feature: gallery — browse, filter and remove surveillance photos

## Goal

Give the operator a fast way to see what the cameras captured on a given
day, and to remove photos they do not want kept. A week strip sits above
a day view; the selected day's photos appear as a grid of thumbnails. A
photo is removed permanently after an inline confirmation.

Scope is the historical JPEG archive only. Live viewing stays on
`/stream` and is not touched.

## Starting point

This is a from-scratch build on `main` @ `0d9e9ba`. No earlier gallery
attempt is carried over, including its endpoint names and file layout.

## What exists today (explored, verified)

- `services/workers/internal/jobs/surveillance/storage.go` owns the
  on-disk layout. It is **write-only**: `NewStorage`, `PathFor`,
  `EnsureDayDir`, `WriteFile`. There is no read, list or delete.
- The filesystem is the index. No database table, no manifest.
- Capture interval is 15 min (`SURVEILLANCE_INTERVAL_MINUTES`), so
  **96 photos per camera per day**; ~8 600 per month at three cameras.
- `workers` has **no HTTP server at all** — it is a background job host.
- `messaging-core` owns both HTTP surfaces: stdlib `ServeMux` with
  `GET /api/devices/active` on `API_PORT` (8081), and a gin server with
  `/healthz` and `/stream/:mac` on `STREAM_PORT` (8080).
- nginx routes all of `/api/` to messaging-core and has exactly two
  upstreams (`scripts/install/13-nginx.sh:151-152`).
- Frontend is hand-edited HTML/CSS/JS, no framework, no build step.
  Reusable helpers: `witsaba.api()`, `witsaba.el()`, `showState()`,
  `onVisible()`. `el()` uses `textContent` only, never `innerHTML`, so
  LAN-supplied strings cannot inject markup.
- `try_files $uri $uri.html $uri/ =404` means a new page needs no nginx
  config of its own.

## Design decisions (locked)

| Decision | Choice | Rationale |
| --- | --- | --- |
| API home | **`workers` gains an HTTP server** on `GALLERY_PORT` | The surveillance job already owns the capture root; read and delete belong beside the writer. messaging-core serving a directory it has nothing to do with is worse than one new upstream. |
| nginx routing | `location /api/gallery/` ahead of `location /api/` | nginx matches longest prefix first, so only the gallery subtree moves. Every other `/api/` route is untouched. |
| Deletion | **Permanent, inline confirm on the tile** | No modal: `DESIGN.md:205` refuses modals as a first thought. The tile flips to `Remove` → `Confirm remove` / `Cancel` in place. |
| Calendar | **Week strip above a day view** | Seven cells with weekday, date and photo count; a stepper moves beyond the current week. |
| Thumbnails | **On-disk cache** under `~/.witsaba/thumbs/<date>/<name>.jpg` | Regenerating per request makes every revisit re-decode on a Pi. Costs disk and one generation pass per new photo. |
| Cameras | **All cameras mixed**, newest first, chip on each tile | Simplest model and the usual reason to want a gallery. |
| Index | **Filesystem only** — one `os.ReadDir` per day | No schema, no migration, no manifest to corrupt. |
| Pagination | **None** | A day is bounded at ~96 photos per camera. Pagination would add surface for no gain. |

## Endpoint surface

```
GET    /api/gallery/days?from=&to=    -> [{date, count}]      week strip
GET    /api/gallery/day?date=         -> [{name, mac, time, bytes}]
GET    /api/gallery/img?date=&name=   -> image/jpeg           original
GET    /api/gallery/thumb?date=&name= -> image/jpeg           generated + cached
DELETE /api/gallery/photo?date=&name= -> 204                  permanent
```

## Security requirements (not optional)

Remote-controlled filesystem deletion is the risk in this feature. Before
either parameter reaches a path:

- `date` must match `^\d{4}-\d{2}-\d{2}$`.
- `name` must match `^\d{2}-\d{2}-\d{2}_[0-9a-f]{12}\.jpg$`.
- The joined path must resolve, after `filepath.Clean`, to a path still
  inside the capture root.
- Symlinks under the capture root must not be followed out of it.

Tests must attempt `../../.ssh/authorized_keys`, an absolute path, a
NUL byte, a crafted symlink, and a name with a valid-looking prefix but
trailing junk.

## Correctness defect found during exploration (fix in W1)

`services/workers/internal/jobs/surveillance/storage.go:15-17` documents
that `os.WriteFile` *"atomically replaces the destination"*. That is
false. `os.WriteFile` opens `O_TRUNC` and writes in place; there is no
temp file and no rename, and the stdlib documents no such atomicity.

It is latent today because nothing reads those files concurrently. Once
the gallery polls for new photos it becomes a real torn-read bug: an
`<img>` can receive a truncated JPEG. W1 replaces the write with
temp-file + `rename`, and corrects the comment. The thumbnail writer in
W4 uses the same pattern from day one.

## Carried-over lesson

An earlier gallery attempt declared `cameras` as both `int` and
`string[]` under one JSON tag. `encoding/json` silently dropped **both**
fields and the UI just looked empty with no error. Any summary/detail
type pair here gets a test asserting the field actually survives
marshalling.

## Tasks

### W1 — make capture writes atomic, and fix the false comment
`storage.go`: write to a temp file in the same directory, `fsync`, then
`rename`. Correct the doc comment. Test that a concurrent reader never
observes a partial file.

**Status: done.** Commit `2c6cb92` —
`fix(surveillance): write captures atomically, correcting a false comment`.

Evidence: RED observed first — `TestStorage_WriteFile_ReplacesAtomically`
failed with `held reader saw "second-image-bytes-clearly-longer-than-the-first"
after rewrite, want "first-image-bytes" — write was not atomic`. After the
change the surveillance package and the whole `workers` suite pass, and
`go vet ./...` is clean.

How the test proves it: it opens a read handle on the destination, rewrites
the file, then asserts the held handle still reads the complete original
content. That is precisely what the gallery does when it serves a photo the
worker is rewriting. Deterministic — no goroutines, no sleeps, no flakiness.
`TestStorage_WriteFile_LeavesNoTempFiles` additionally pins that atomic
writes do not leak temp files into the day directory the gallery lists.

### W2 — the read side of `Storage`
`ListDays` and `ListPhotos`, reusing the existing layout knowledge and
`normalizeMAC`. `ListPhotos` returns name, MAC, capture time and byte
size. Tests with `t.TempDir()`, `t.Parallel()`, plain stdlib
assertions — matching the existing conventions in `storage_test.go`.

**Status: done.** Commit `c76ae1d` —
`feat(surveillance): read side of Storage for the gallery`.

Evidence: RED first — the package failed to build with
`s.ListDays undefined (type Storage has no field or method ListDays)`
and the same for `ListPhotos`. After the change the surveillance package
reports **48 passing, 0 failing**, the whole `workers` suite is green
under `-count=1`, and `go vet ./...` is clean.

What the tests pin:

- `ListDays` returns ISO dates sorted ascending, ignoring directories
  that are not capture days and a stray file at the root.
- **Empty is empty, never nil.** `ListDays_EmptyIsEmptySliceNotNil` and
  `ListPhotos_EmptyDayIsEmptySliceNotNil` exist because the previous
  attempt answered `null` for a real-but-empty archive and the calendar
  rendered blank with no error anywhere.
- A missing root, and a day with no directory, are empty rather than
  errors — the operator should see "no photos captured on this day",
  not a failure page.
- **`ListDays_SkipsDaysWithNoCapturesLeft`**: deleting the last photo of
  a day leaves an empty directory. The calendar must not then show a
  permanent blank cell for it.
- **`ListPhotos_SkipsTempFilesAndJunk`**: the `.<name>.tmp-*` file that
  W1's atomic write creates mid-flight is skipped, along with a
  `.bak`, a `notes.txt`, a non-hex MAC, and a subdirectory.
- A file vanishing between the directory read and the `Info` call is
  skipped, not fatal — that is the delete/retention race.

`ValidCaptureDay` and `ValidCaptureName` are exported now, because W3 and
W4 need them to reject a request-supplied day and name before either is
ever joined onto the capture root.

### W3 — HTTP server in `workers`, gallery read endpoints
New `internal/infrastructure/httpserver`. `GET days`, `GET day`,
`GET img`. Path validation with the hostile-input tests above.
`GALLERY_PORT` config (default 8082). Wire into `cmd/workers/main.go`.

### W4 — thumbnails and deletion
On-demand thumb generation with atomic cache writes. `DELETE`
with strict validation. Handle the ENOENT race between listing and
deleting. Test unremovable files and symlink escapes.

**Status: done.** Commit `be39567` —
`feat(gallery): thumbnail cache and permanent delete`.

Evidence: 37 tests in `internal/infrastructure/httpserver`, whole
`workers` suite green under `-count=1`, `go vet` and `go build` clean.

Thumbnails are 320px wide, cached at `~/.witsaba/thumbs/<date>/<name>.jpg`,
derived as a sibling of the capture archive. The derivation is load-bearing:
the unit sets `ProtectHome=read-only` with `ReadWritePaths=$HOME/.witsaba`,
so a cache elsewhere under `$HOME` would be denied at write time. Standard
library only — `image/jpeg` decodes and the downscale is a box filter, so no
dependency was added. Box boundaries use integer arithmetic; float rounding
there produces a visible one-pixel seam.

`TestThumbnail_CorruptSourceLeavesNoCacheEntry` guards a cache-poisoning trap:
a corrupt capture must leave no entry behind, or every later request serves
that broken file. `TestThumbnail_ConcurrentGetsAreSafe` covers the same
torn-read hazard W1 fixed on the capture side.

Deletion reuses `resolveCapture`, so it carries both gates. Deleting a photo
also drops its cached thumbnail — a thumbnail that outlives its photo is a
photo that is still reachable. Deletion deliberately does **not** live on
`Storage`: a `Storage.Remove(day, name)` could only re-validate the two
components and would have no containment check, putting a weaker gate beside a
stronger one.

### W5 — the gallery page
`gallery.html` plus the nav link in `index.html`, `devices.html` and
`stream.html`. Week strip, day grid, inline delete confirmation. Reuse
`witsaba.api`, `witsaba.el`, `showState`, `onVisible`. Existing CSS
tokens only; dark mode, `prefers-reduced-motion`, focus-visible rings,
40px tap targets, icon+label status, specific empty states.

### W6 — nginx, systemd, docs
Third upstream and `location /api/gallery/`. `ReadWritePaths` for the
thumb cache. Update this document and `frontend/web_ui/README.md`.

**Status: W5 and W6 both committed.** W5 `1e59107`
`feat(web_ui): gallery page with week strip and inline delete`; W6 wires
nginx, `witsaba.env`, and the docs.

`api()` gained `options.method` and now resolves `204` to `null`. It
previously always issued a GET and always called `res.json()`, so neither the
DELETE verb nor the delete route's 204 response was reachable.

**Verification is weaker here and is stated as such.** There is no front-end
test runner in this repo. What was actually run: `node --check` on the shared
helper and the page's inline script, an HTML tag-balance pass, undefined-CSS
token detection, and direct execution of the calendar helpers — 15 assertions
covering the year boundary and a DST week, where off-by-one errors live. All
passed. **The page has not been loaded in a browser and has not been exercised
through nginx.**

`test-nginx-config.sh` gained four assertions: a longer-prefix
`location /api/gallery/`, the `witsaba_gallery` upstream, that upstream being
loopback-bound, and `gallery.html` present in the document root. The live layer
skips without a running install, so those four are unproven until run on the
Pi. `bash -n` passes on every install script.

No `ReadWritePaths` change was needed: `ReadWritePaths=$INSTALL_DIR` already
covers `~/.witsaba/thumbs`, because `INSTALL_DIR=$HOME/.witsaba`. An earlier
note in this document predicted a systemd change here; that prediction was
wrong and the finding is what drove the cache-root derivation in W4.

## Verification

Go tests are deterministic and apply to W1-W4: `cd services/workers &&
go test ./...`. For W5 there is **no front-end test runner** — per
`PRODUCT.md` there is no toolchain in the serving path — so verification
is structural plus manual: nginx config test, page load, dark mode,
keyboard traversal, and the delete round trip. W6 runs
`scripts/install/test-nginx-config.sh`. No lifecycle evidence will be
invented for the parts that cannot be run here.

## Anti-goals

- No Docker work. The operator target is native systemd/nginx.
- No build step, bundler, framework or third-party JS.
- No sparklines, progress rings, gradients, glass, blur, emoji icons or
  display fonts.
- No modal dialogs.
- No database table or manifest.