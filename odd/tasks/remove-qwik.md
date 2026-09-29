# Feature: Remove the Qwik project

## Goal

Delete the paused Qwik + Qwik City front-end from the repository and from
`docker-compose.yml`, and remove the Node/pnpm install machinery that existed
only to build and serve it. The surviving product is the plain static site at
`frontend/web_ui/static/`, served by nginx from `scripts/install/13-nginx.sh`
and supervised by `scripts/install/12-systemd-services.sh`.

The Pi is the only target that matters. After this change the install path is
Go + Postgres + nginx, and nothing in it needs Node.

## Why

`odd/tasks/plain-frontend-nginx.md` replaced the framework front-end on the Pi
because `vite preview` cost 143MB of RAM and 308MB of `node_modules` to serve
a page that is 52KB on disk, on a host with 899MB total. Qwik was left in the
repo as an opt-in path (`WITSABA_WITH_QWIK=1`) and then paused by explicit
instruction. The two trees have since diverged: `static/stream.html` shipped in
PR #34 and has no Qwik counterpart, so the scaffold is now one page behind
rather than an equal alternative.

It also carries known defects that no longer have anything to protect:

- `package.json` declares `engines.node: ^18.17.0 || ^20.3.0`, which contradicts
  the `>=22.19.0` floor of `undici@8.11.2` pulled in by `qwik-city@1.19.2`.
  `Dockerfile` pins `22.13.0`, also below that floor.
- `docker-compose.yml` still carries a `web_ui` service that nothing deploys.
- `pnpm test` globs `src/**/*.ts*`, so it covers none of the site that ships.

## Scope decisions (user-confirmed)

| # | Decision | Rationale |
|---|---|---|
| 1 | `frontend/web_ui/static/` keeps its exact path | `13-nginx.sh:318` hardcodes it and is verified on the Pi. A rename is pure churn against a verified deploy path. |
| 2 | Delete `03-node.sh`, `11-build-frontend.sh` and the Node helpers in `_lib.sh` | Nothing in the install path needs Node or pnpm once the framework is gone. |
| 3 | `odd/tasks/**` is an append-only log, untouched | It records what happened and why, including the decision to replace Qwik. Rewriting history destroys the 143MB measurement and the pinning investigation. |
| 4 | No new tests in this change | Strictly a removal. The coverage gap on `static/` is reported as a follow-up, not silently closed. |
| 5 | `DESIGN.md` stays as-is | Verified: 220 lines, zero references to Qwik, `src/`, components or `global.css`. It documents the design language that `static/assets/app.css` inherited. |

## Confirmed existing behaviour (read, not assumed)

Read on `feat/remove-qwik` at base `d230ad2`:

| Layer | Fact | Source |
|---|---|---|
| Static site | 5 files, 72KB. No reference to `qwik`, `dist/`, `node_modules` or `vite` | `git grep` over `frontend/web_ui/static/` |
| Deploy | Copies **only** `static/`, then asserts `index.html`, `devices.html`, `assets/app.css`, `assets/app.js` exist | `13-nginx.sh:318-345` |
| Runtime unit | `ExecStart`s nginx. No node, vite or pnpm | `12-systemd-services.sh:226-240` |
| Data path | `browser -> nginx :4173 -> messaging-core :8081/:8080 -> Postgres` | `13-nginx.sh` proxy blocks |
| Compose graph | No service `depends_on` `web_ui`; it is a leaf | `docker-compose.yml`, only 3 comment refs + the block |
| `public/` | `static/` references neither `manifest.json` nor `robots.txt`; both favicons are byte-identical | `cmp` and grep |
| Root `README.md` | No mention of Qwik, pnpm, nginx or a UI port | grep returned nothing |

## Exact removal surface

**Deleted outright (23 tracked entries under `frontend/web_ui/`, all except
`static/`):** `src/` (20 files), `public/` (3), `Dockerfile`, `package.json`,
`pnpm-lock.yaml`, `pnpm-workspace.yaml`, `qwik.env.d.ts`, `tsconfig.json`,
`vite.config.ts`, `vitest.config.ts`, `eslint.config.js`, `.prettierignore`,
`.npmrc`, `.gitignore`, `.dockerignore`, `.vscode/` (3).

**Deleted outright (scripts):** `scripts/install/03-node.sh`,
`scripts/install/11-build-frontend.sh`.

## Edit map

| File | Edit |
|---|---|
| `docker-compose.yml` | delete 165-225 (the `web_ui` block, runs to EOF); rewrite the header graph at 30-35 |
| `scripts/install/_lib.sh` | delete 365-518 (Node toolchain section: header, 3 constants, `setup_node_env`, `node_path_matches_node_bin`, `node_bin`, `pnpm_bin`, `node_version_ok`, `preflight_node`) |
| `scripts/install/install-ubuntu.sh` | drop 52 from `STEPS`; delete 65-73 (the `WITSABA_WITH_QWIK` gate); fix 173 (closing summary claims Node installed) |
| `scripts/install/test-scripts-load.sh` | drop `03-node.sh` and `11-build-frontend.sh` from 43-44; fix the 61 comment |
| `scripts/install/test-password-gen.sh` | delete section 14, lines 329-374. **Discovered independently of the explore pass:** this suite asserts `setup_node_env`, `node_path_matches_node_bin`, `NODE_BIN` and `PNPM_BIN`. It is 39 assertions and it would fail the moment `_lib.sh` loses those functions. |
| `scripts/install/uninstall-ubuntu.sh` | line 41 `~/.witsaba/frontend`; line 72 stale `witsaba-web-ui` unit |
| `scripts/install/13-nginx.sh` | lines 5, 7 — stale `vite preview` comments only. **No behaviour change.** |
| `scripts/install/12-systemd-services.sh` | lines 49, 221 — stale `pnpm preview` / `vite preview` comments only. **No behaviour change.** |
| `scripts/install/test-nginx-config.sh` | line 60 message; 63-76 the "independence from the Qwik tree" check goes vacuous |
| `scripts/install/README.md` | 21, 31, and the "Two frontend paths" section 57-75 |
| `PRODUCT.md` | 11-18, 72, 80, 82, 97, 99, 102, 104 |

## Design decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Keep the `witsaba-web-ui` entry in the uninstall stop loop | A Pi upgraded from a pre-nginx install may still carry that unit file. Stopping it is correct on the way out. A fresh install never creates it, so `systemctl` on an unknown unit is already tolerated. |
| 2 | Replace the vacuous Qwik-independence check rather than just delete it | Lines 67-76 would always take the `else` branch. Replace with an assertion that the document root holds exactly the expected static files and that `nginx.conf` references no `node_modules` path. Keeps a real guarantee. |
| 3 | Add a short `frontend/web_ui/README.md` | After the purge that directory contains only `static/` and would be undocumented. Describes the site, the deploy path and the origin rules. |
| 4 | Do not touch `DESIGN.md` | Verified free of framework references. The tokens it defines are the ones `app.css` uses. |
| 5 | Do not rename `static/` to `frontend/web_ui/` | The nesting is odd but the path is load-bearing in a script verified on the target. Cosmetic churn against a verified deploy is a bad trade. |
| 6 | Comment-only edits in `13-nginx.sh` and `12-systemd-services.sh` | Both are verified on the Pi. They stay byte-identical in behaviour; only their stale rationale text changes. |

## TDD / verification mode

| Field | Value |
|---|---|
| Mode | **off** |
| Source | No test runner will cover `frontend/web_ui/static/` after this change, and none covers the removal itself. Test presence does not enable TDD. |
| Runner | n/a — the repository's own install suites plus `docker compose config` |

## Public surface

Deleted: the Qwik scaffold, the `web_ui` compose service, the Node install
step, the Qwik build step, the Node helper library.
Changed: `docker-compose.yml`, `scripts/install/*` (comments, step lists,
assertions, docs), `PRODUCT.md`.
Untouched: `frontend/web_ui/static/**`, `13-nginx.sh` and `12-systemd-services.sh`
behaviour, `odd/tasks/**`, `DESIGN.md`, every Go service, `embedded/`.

## Tasks

- [x] **T1** Delete the Qwik tree under `frontend/web_ui/`, keeping only
  `static/`. Add a short `frontend/web_ui/README.md` for what remains.
- [x] **T2** Remove the `web_ui` service from `docker-compose.yml` and fix the
  header service-order graph.
- [x] **T3** Remove the Node install path: `03-node.sh`,
  `11-build-frontend.sh`, the `_lib.sh` Node section, the `WITSABA_WITH_QWIK`
  gate, and the assertions in `test-scripts-load.sh` and
  `test-password-gen.sh` that cover them. Rewire `uninstall-ubuntu.sh`.
- [x] **T4** Neutralise the stale framework comments in `13-nginx.sh` and
  `12-systemd-services.sh` (comments only), and replace the vacuous
  Qwik-independence check in `test-nginx-config.sh` with a real one.
- [x] **T5** Correct `PRODUCT.md` and `scripts/install/README.md` to describe
  the stack that actually exists.
- [x] **T6** Verify and close.

## Acceptance criteria

- `docker compose config` renders and lists exactly `postgres`,
  `messaging-core`, `workers`.
- `git grep -ri qwik` returns nothing outside `odd/tasks/**`.
- `git grep -rn "node\|pnpm\|vite" -- scripts/` returns nothing outside
  `odd/`, excluding the word "node" in unrelated prose.
- `test-scripts-load.sh` green.
- `test-password-gen.sh` green, with the node section gone.
- `test-nginx-config.sh` no worse than baseline, with a real independence check.
- `bash -n` clean on every remaining script.
- `frontend/web_ui/static/` byte-identical to `d230ad2`.
- No change to nginx routing, ports, units or the Go services.

## Out of scope

- Committing the `/tmp` behaviour harnesses for `static/` (user decision 4).
- Filling in the missing `/discovery`, `/logs`, `/settings` pages.
- Renaming `static/`.
- Touching the Pi. This change is workstation-side; deployment is the user's
  call after merge.

## Delivery forecast

~6 work-unit commits. Mostly deletions plus surgical edits. Each commit is
small; the reviewable risk is entirely in T2-T5, where the diff is a handful of
lines per file. Strategy: `single-pr`.

## Route

Delegated for the reference map (`gentle-ai-explore`) and parent-executed for
the edits, because each file change is a precise, already-determined edit
rather than an open implementation. Parent owns every commit and every check.

## Progress

Complete. Six work-unit commits on `feat/remove-qwik`, base `d230ad2`.

| Commit | Task | What |
|---|---|---|
| `4124ffa` | T1 | 41 files deleted, 9166 lines; README added for `static/` |
| `493880e` | T2 | `web_ui` service removed from compose; header graph rewritten |
| `bfc0574` | T3 | Node install path removed across 8 files |
| `63eedd7` | T4 | comment-only in the two Pi-verified scripts; real test replaces the vacuous one |
| `d43cec6` | T5 | `PRODUCT.md`, `scripts/install/README.md`, `messaging-core/README.md` |

## Verification evidence

Every command below was run on `feat/remove-qwik`, not inferred.

| Check | Result |
|---|---|
| `docker compose config` | renders; services are `postgres`, `messaging-core`, `workers`. Main still lists four. |
| `bash -n` on all 13 remaining scripts | clean |
| `test-scripts-load.sh` | 27 passed, 0 failed (main: 30 - two scripts removed, one added, 3 assertions each) |
| `test-password-gen.sh` | 35 passed, 0 failed (main: 39 - exactly the 4 node assertions in section 14) |
| `test-nginx-config.sh`, no install here | 0 passed, 1 failed, 3 skipped. The failure is `document root missing: ~/.witsaba/nginx/html`, identical on main. The new node check correctly SKIPs instead of passing vacuously. |
| `test-nginx-config.sh` vs a synthetic install | **9 passed, 0 failed, 2 skipped** |
| `git diff d230ad2 -- frontend/web_ui/static/` | empty - the shipped site is byte-identical |
| non-comment changed lines in `13-nginx.sh` | 0 |
| non-comment changed lines in `12-systemd-services.sh` | 0 |
| changed lines inside any heredoc in those two files | 0, so the generated `nginx.conf` and unit are byte-identical |
| changed `.go` files | 0 |
| changed files under `services/*/internal` and `embedded/` | 0 |
| remaining caller of any deleted `_lib.sh` symbol | none, repo-wide |

### The new assertion was proven, not assumed

`test-nginx-config.sh` gained two checks. Each was run against a synthetic
install built in `/tmp`:

| Scenario | Expected | Observed |
|---|---|---|
| `nginx.conf` names a `node_modules` path | fail | `FAIL nginx.conf references a node tree` |
| clean `nginx.conf` | pass | `PASS`, suite 9/0/2 |
| `node_modules/` present in the document root | fail | `FAIL document root contains a node_modules directory` |

This matters because the check it replaced was guarded on
`$INSTALL_DIR/frontend/node_modules` existing. With the tree deleted that
directory can never exist, so the old check would have taken its `else` branch
forever and asserted a guarantee it never tested. The first version of the
replacement had the same defect in a different shape - it passed when
`nginx.conf` was absent - which is why it now skips unless the config exists.

### Known remaining references (deliberate)

Three comment lines in the shipped site still say the tokens and wording were
ported from the Qwik app:

- `frontend/web_ui/static/assets/app.css:4` and `:65`
- `frontend/web_ui/static/assets/app.js:53`

They are true provenance statements, and `static/` is deployed to the Pi and
kept byte-identical by decision. Rewriting them would be cosmetic churn against
a verified path. Flagged, not silently ignored.

Two other mentions are the new test's own search strings and the single
`Vite-style hashed names` note inside the `nginx.conf` heredoc, which explains
why assets are deliberately not fingerprinted.

`grep -ri qwik` outside `odd/` also matches two references to this document's
own filename, `odd/tasks/remove-qwik.md`.

### Incidents during the work

The worktree path failed to resolve intermittently, twice producing an apparent
"worktree vanished". Diagnosis: two near-identical directory names exist,
`local-home-assitant` and `local-home-assitant-worktrees`, differing by one
missing `c`. Hand-typed long absolute paths were mistyped. Nothing was ever
deleted. Every later command derives the path and asserts the branch and
toplevel before mutating. One commit attempt ran in the main checkout because
its `cd` failed; it staged nothing because that tree was clean, and main was
verified untouched at `d230ad2` immediately after.

## Follow-ups

- `static/` has no committed test suite. The `witsaba.stream` harnesses from
  PR #34 still live only in `/tmp`. Explicitly deferred, not closed.
- `index.html` links `/discovery`, `/logs` and `/settings`. No such pages exist
  in either tree, so all three 404 through `try_files`.
- A stale separate clone remains at
  `../local-home-assitant-worktrees/native-install` on `feat/native-linux-install`
  (merged as `e75af53`). Unrelated to this change, left untouched.
