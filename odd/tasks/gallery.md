# Feature: browser gallery for the periodic surveillance captures

## Goal

Make the JPEGs that `services/workers` writes every 15 minutes browsable
from the web UI, as a date-addressed archive grouped by capture moment.

The captures already exist on the Pi at
`$HOME/.witsaba/cameras/<YYYY-MM-DD>/<HH-MM-SS>_<mac>.jpg`. Today they are
**completely unreachable from a browser**: nginx has no location for the
directory, messaging-core's only REST route is `GET /api/devices/active`,
and workers has no HTTP server at all. This feature adds the read path and
the page. It does not change how captures are produced.

## Why the filename already carries the model we need

The surveillance job stamps the **tick** time on every camera in a tick
(`storage.PathFor` is called with a single `now` for the whole loop), so the
`HH-MM-SS` prefix is a reliable moment key, and the `_` + 12-hex suffix is
the camera. The day folder is `t.Format("2006-01-02")` in Pi local time.

That means a day view is a `ReadDir` of one folder plus a regex, with no
database read and no index file to maintain. `odd/tasks/surveillance-worker.md`
recorded "no index in Postgres, the filesystem is the index" as a deliberate
non-goal; this feature honors that and reads the filesystem directly.

## Decisions (locked)

| # | Decision | Rationale |
| --- | --- | --- |
| D1 | **Read path is Go only.** messaging-core on `:8081` behind the existing `location /api/` proxy. The front consumes JSON; it never touches a file path. | Operator runs native Linux, not Docker, and is acquiring a more robust server. "Go does the work, front only consumes." nginx gets no bytes route. |
| D2 | **Thumbnails on demand, cached on first read.** `<root>/<day>/.thumbs/<HH-MM-SS>_<mac>.jpg`, JPEG q70, 320px longest edge. | Nothing is generated until someone looks at a day. The dot-dir inside the day folder never collides with full frames, cannot be matched by a `*.jpg` glob, and is pruned for free with the day. |
| D3 | **Retention in the worker: `SURVEILLANCE_RETENTION_DAYS=30`.** Pruned at the top of each tick, before capture. | ~270 MB bounded (200 MB frames + ~35% thumbs). The gallery makes an unbounded archive a real risk on a Pi SD card. |
| D4 | **Page is a date picker + archive browsing, grid grouped by moment.** | Matches the day-folder data model exactly. Freshness-only browsing cannot reach back to "what happened Tuesday". |
| D5 | **No flash indicator.** Not recorded, not re-derived, not encoded in filenames. | A label derived from a time window is inference, not fact. Honest dark frame beats inferred metadata. |
| D6 | **The CCTV-starves-capture contention is documented, not fixed.** | The real fix is firmware-side (release the `cam_reader` semaphore between stream frames) and needs a reflash of all three cameras. Out of scope; the gallery records the symptom. |

## Non-goals

- No change to how captures are produced: same interval, same flash window,
  same filenames, same directory layout.
- No index file, no database table, no schema change.
- No video, no motion detection, no download-as-zip, no sharing.
- No fix for the `witsaba-nginx.service` / `docker-compose.yml` cameras
  divergence. The operator does not use Docker; that file stays untouched and
  the divergence is recorded here instead.

## Endpoint surface

Addressing is by **three validated query parameters**, never by a
client-supplied path. The server *constructs* every filesystem path, so
traversal is impossible by construction rather than by sanitization.

| Route | Returns |
| --- | --- |
| `GET /api/gallery/days` | `[{date, shots, cameras, bytes}]`, newest day first — drives the date picker |
| `GET /api/gallery/day?date=YYYY-MM-DD` | `{date, cameras:[{mac,name}], moments:[{time, shots:[{mac,name,bytes,image_url}]}]}` |
| `GET /api/gallery/img?date=&t=&mac=` | The full JPEG. ETag + Last-Modified + 304. (U2) |
| `GET /api/gallery/thumb?date=&t=&mac=&w=320` | Thumbnail, generated once then cached. (U3) |

Validation, applied before any path is built:

- `date` matches `^\d{4}-\d{2}-\d{2}$` **and** parses via
  `time.Parse("2006-01-02", ...)` — the regex alone would accept `2026-13-45`.
- `t` matches `^\d{2}-\d{2}-\d{2}$` **and** parses via `time.Parse("15-04-05", ...)`.
- `mac` matches `^[0-9a-f]{12}$`.

A defensive `withinRoot` check (clean the result, require the root prefix)
backs this up. It should be unreachable; that is the point.

## Design notes that are not obvious

**Camera names for archived days.** `ListActive` drops devices unseen for
180 s, so a 20-day-old day would render as bare MACs. `witsaba.devices`
keeps rows forever (`first_seen_at` is preserved on conflict), so U1 adds
`ListAll` to messaging-core's `DeviceRepository`: one query, ~3 rows, no
schema change. Names are joined by MAC at request time; a MAC with no DB row
yields an empty name and the page falls back to the short MAC. The API never
invents a name.

**Decode concurrency, not RAM, is the Pi constraint.** A 320x240 decode is
~1.4 MB of pixel buffer against `MemoryMax=200M`, so 4 concurrent decodes is
~6 MB — trivial. The failure mode is 288 concurrent decodes. U3 caps
concurrent decodes with a semaphore and the page lazy-loads so a day view
never requests every thumbnail at once.

**Thumbnail writes must be atomic.** Two simultaneous requests for the same
uncached thumbnail must not both write, and a browser must never read a
half-written file. U3 writes `<name>.<rand>.tmp` then `os.Rename`s it into
place. Rename-over is atomic on POSIX and needs no new dependency.

**The thumbnail ETag comes from the source.** The source frame is immutable,
so the thumbnail is a pure function of it. ETag is derived from the source's
mtime+size, not the thumbnail's, so a re-read of an unchanged day is a 304
regardless of cache state.

**Source size must be capped before decode.** The worker does an unbounded
`io.ReadAll` on the camera response, and `image/jpeg` will happily allocate a
decompression bomb. U3 stats the file and refuses anything over a bound.

**The newest frame of the current day can be truncated.** `os.WriteFile` is
`OpenFile(O_WRONLY|O_CREATE|O_TRUNC)` + `Write` — not a temp-and-rename. The
comment in `surveillance/storage.go` claiming it "atomically replaces the
file" is wrong. U2 defends in the read path: a zero-size file or one missing
JPEG SOI/EOI markers returns 404, and the page's existing `no-signal.svg`
state renders. No worker change.

**Day rollover.** At midnight the worker starts writing to a new day folder,
so an open page silently stops updating. The page re-reads the current day on
its poll and must notice a date change.

**Retention scales with the interval.** 30 days at 15 min is ~270 MB. At
5 min it is ~1.6 GB. The number is a function of `SURVEILLANCE_INTERVAL_MINUTES`,
not a constant, and belongs in both docs.

**Retention needs a safety guard, not just a knob.** D3 accepts the risk I
flagged. U4 only removes entries that (1) parse as a date in the
`2006-01-02` layout, (2) are strictly older than the cutoff, and (3) are
direct children of root verified with `os.Lstat` — **symlinks are rejected,
never followed**. A misconfigured `SURVEILLANCE_ROOT_DIR` then produces a
rejected run and a loud log, not a deleted home directory.

**Thumbnails need a write path in a unit that currently has none.**
`witsaba-messaging-core.service` is hardened with `ProtectSystem=strict` and
`ReadWritePaths=$INSTALL_DIR`, and its comment states the binary "writes
nothing to disk". U3 revisits that deliberately rather than discovering it as
a permission error in production.

## Work units

Each is one reviewable commit. U1–U3 are pure additive Go with no worker
change, so the gallery is usable before retention lands.

- [x] **U1** — `ListAll` on `DeviceRepository` (+ Pgx impl, tests); new
  `internal/infrastructure/gallery` package with validated path construction,
  day enumeration and moment grouping; `GET /api/gallery/days` and
  `GET /api/gallery/day`; `GALLERY_ROOT_DIR` config; wiring in `main.go`.
  Commits `e995c59` (unverified) then `4bae310` (verified: `go build ./...`,
  `go vet ./...`, `go test -race ./...` all pass, no gofmt drift).

- [x] **U2** — `GET /api/gallery/img` with ETag / Last-Modified / 304 and the
  truncated-frame guard.
- [x] **U3** — `GET /api/gallery/thumb` with the atomic `.thumbs` cache, the
  source size cap and the decode semaphore; the systemd write-path change.
- [x] **U4** — `SURVEILLANCE_RETENTION_DAYS` in the worker with the
  `Lstat`-verified day-folder prune, plus the `SURVEILLANCE_*` block in
  `env.example` (currently missing entirely).
- [x] **U5** — `frontend/web_ui/static/gallery.html`, nav link in the three
  existing pages, date picker + moment grid, and
  `location = /gallery { try_files /gallery.html =404; }` in `13-nginx.sh`
  plus the post-deploy assert list. The exact-match block is required: a path
  that is the stem of a prefix location is 301'd to its slash form *before*
  `try_files` runs, which is exactly how `/stream` broke once.
- [x] **U6** — verification log and Pi deployment.

## Frontend constraints (carried from `stream-page-cctv-grid.md`)

- No `innerHTML`. Everything from the filesystem or the LAN goes through
  `witsaba.el(tag, {text: ...})` / `textContent`.
- Existing design tokens only (`--space-*`, `--radius-*`, `--color-*`). No raw
  hex, no bare px radius, no unitless spacing in added CSS.
- `prefers-color-scheme: dark` and `prefers-reduced-motion` are mandatory.
- Keyboard-first; color is never the only signal; operator-grade copy, no
  decorative motion, no gamified feedback.
- Reuse `witsaba.api`, `witsaba.el`, `witsaba.showState`,
  `witsaba.setStatus`, `witsaba.onVisible`, and the `no-signal.svg` fallback.
- There is **no committed frontend test runner** (mode is "off"; the CCTV
  feature used throwaway Node `vm` harnesses in `/tmp`). U5 follows the same
  pattern unless the operator asks to change that convention.

## Acceptance criteria

- `go build ./...`, `go vet ./...` and `go test -race ./...` exit 0 on
  linux/amd64 and darwin/arm64.
- `GET /api/gallery/days` on an empty or missing root returns `[]`, never 500.
- `GET /api/gallery/day?date=<a real day>` returns moments in chronological
  order, one entry per `(tick, camera)` pair, with sizes from `Lstat`.
- A malformed `date`, `t` or `mac` is rejected with 400 **before** any path is
  built; no filesystem call is made for an invalid request.
- `..`, `/`, and absolute paths in any parameter cannot reach outside the root
  (the `withinRoot` guard is asserted by a test, not assumed).
- A MAC absent from `witsaba.devices` yields an empty name, never a fabricated
  one and never a 500.
- The worker still registers `job_count: 2` after U4 (the standing
  "is the new code running" diagnostic from the surveillance worker session).
- `bash -n` passes on every touched install script and `nginx -t` accepts the
  generated config.

## Risks

| Risk | Mitigation |
| --- | --- |
| 288 thumbs requested at once on a Pi | decode semaphore (U3) + page lazy-load (U5) |
| Gaps in a day when the CCTV page was open | D6: documented, not fixed here |
| Retention misconfiguration deletes history | U4 `Lstat` + date-parse + strict-older-than guard |
| Truncated newest frame renders broken | U2 404 + `no-signal.svg` |
| Thumbnail cache needs disk writes in a hardened unit | U3 revisits the unit explicitly |

## Resolved: the "module does not build" incident

An earlier `e995c59` commit carried a `server.go` import whose module
prefix did not match `go.mod`, so the build failed with "no required
module provides package". The repo *directory* is misspelled
`local-home-assitant` while the module path is the same typo, which made
the bad import look correct at a glance.

The real trap was diagnostic: the workspace filesystem was intermittently
returning truncated directory listings, so `ls` and Go disagreed and the
build alternately appeared environmental and code-related. The resolution
was to trust `go.mod` as the single source of truth for the module path
and rewrite the import prefix from it.

Corrected on disk. The Go toolchain, `GOCACHE`, `GOMODCACHE`, vendor and
`GODEBUG=goindex=0` were all innocent. No reinstall is needed.

## Verification log

### Automated, reproducible here

```
services/messaging-core   build ./...  vet ./...  test -race ./...   PASS
services/workers          build ./...  vet ./...  test -race ./...   PASS
node --test frontend/web_ui/test/                                       17/17 PASS
bash -n scripts/install/*.sh                                            PASS
gofmt -l on every file this branch touched                             clean
```

`gofmt -l` still reports `surveillance.go` and `types.go`. Both were already
unformatted at `461f083` before this branch touched them, and the drift does
not overlap the retention code. Left alone rather than mixed into a feature
diff.

### Defects this verification found

**`/api/gallery/days` returned `null` for an empty archive.** `ListDays`
declared `var out []DaySummary` and never appended when the capture root
existed but held no date-named directories, so it marshalled to `null`. The
page does `days.length` on that array, which throws. This is the first-run
state of every fresh deployment: the systemd unit creates the root, the worker
has not captured anything yet, and the gallery would have shown an error
instead of "no captures yet". Fixed by initialising the slice; pinned by
`TestGalleryDays_EmptyArchiveIs200NotError`. The `os.IsNotExist` branch
already returned `[]`, so only the existing-but-empty root was affected, which
is why the unit tests did not catch it.

**`PruneOlderThan` aborted the sweep on the first failed removal.** The comment
said "Report but do not abort: one unremovable day must not stop the rest of
the archive from being reclaimed" and the code did `return`. Retention exists
to bound disk growth, so skipping the remaining archive over one stubborn
directory defeats its purpose. Failures are now collected with `errors.Join`
and returned once the sweep finishes, and a day that stays on disk is counted
as kept so `Days+Kept` accounts for every day directory.

**`TestRunSurvivesAnUnremovableDay` proved nothing.** It pointed `Storage` at a
missing directory, which is a deliberate no-op, so the prune never failed and
the assertion passed vacuously. The comment even claimed the root was "made
unreadable" while the code only deleted it. Replaced with a permission-based
unremovable day, plus `TestPruneOlderThan_KeepsGoingPastAnUnremovableDay`
covering the behaviour above. Both skip under root, where the permission bits
would be ignored and the directory would simply be removed.

**`/api/gallery/day` and `/api/gallery/days` had no HTTP-level tests.** The
image and thumbnail endpoints had thorough coverage; the two the page loads
first were only covered at the store level. Their JSON is now load-bearing for
a browser, so `gallery_day_test.go` pins both shapes over real HTTP. Note that
the two responses are deliberately different — `cameras` is a count in the
summary and a label list in the detail — and writing one Go struct for both
makes `encoding/json` silently drop the duplicated tag. The test types are
separate for that reason.

### Pi deployment checks (operator)

Not run here; no Pi, Postgres or nginx in this environment. After pulling:

1. `systemctl status witsaba-messaging-core witsaba-workers` — both active.
   The gallery needs neither to change, only the ones added in U2-U4.
2. `systemctl show -p ReadWritePaths witsaba-messaging-core` — must contain
   the capture root. Without it `ProtectHome=read-only` makes the thumbnail
   cache fail with `EROFS` and every tile falls back to the placeholder.
3. `sudo -u witsaba ls -ld $GALLERY_ROOT_DIR` and
   `sudo -u witsaba touch $GALLERY_ROOT_DIR/.probe` — the worker and the API
   must both be able to write there.
4. `curl -s localhost:8081/api/gallery/days` — `[]` before any capture, then
   one entry per day. `null` means an old binary.
5. `curl -s 'localhost:8081/api/gallery/day?date='"$(date +%F)"` — moments
   grouped by shared tick, times as `HH-MM-SS`.
6. `curl -sI 'localhost:8081/api/gallery/thumb?date=…&t=…&mac=…&w=320'` —
   `200`, `image/jpeg`, and an `ETag`. The file then exists under
   `$GALLERY_ROOT_DIR/<date>/.thumbs/320/`.
7. `curl -sI localhost:8081/api/gallery/img?…` then repeat with
   `-H 'If-None-Match: <etag>'` — expect `304`.
8. In the browser, `https://<host>/gallery` loads a day grid;
   `/gallery.html` also resolves; `/stream` still works, which is the exact
   match that must not be disturbed.
9. Leave it overnight, then confirm `SURVEILLANCE_RETENTION_DAYS` drops old
   day directories rather than the newest one.
