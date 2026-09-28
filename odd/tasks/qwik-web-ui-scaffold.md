# Qwik Web UI Scaffold - Task Tracking

## Feature: Qwik Web UI Scaffold + Docker Integration

### Status
✅ Scaffold complete, all work-unit commits landed, verification matrix green
on macOS arm64 (Node 22.11.0, pnpm 11.8.0).

### Goals
1. ✅ Scaffold a Qwik + Qwik City project at `frontend/web_ui/`
2. ✅ Use pnpm as the package manager (user preference, Qwik docs default)
3. ✅ Add Vitest with the server-side `renderToString` path (chosen over
   `createDOM` — see "Testing" below for the reason)
4. ✅ Clean the Qwik starter down to a single skeleton home page
   (front-engineer preview style)
5. ✅ Add a multi-stage Dockerfile for the Qwik dev server (Node 22 alpine,
   linux/arm64-friendly)
6. ✅ Wire the new `web_ui` service into the existing root
   `docker-compose.yml` with `condition: service_healthy` on Postgres so
   the dev server only starts once the DB is up
7. ✅ Verify: `pnpm build`, `pnpm test`, `pnpm lint`, `docker compose config`

### Non-Goals
- E2E tests (Playwright) — added later in a follow-up
- Production SSR adapter (Bun/Node/Cloudflare) — out of scope for this scaffold
- Auth, routing depth, design system, real API integration — placeholder skeleton only
- Bun runtime — pnpm + Node only (Bun adapter has known `routeAction$` bugs; flagged in research)

### Stack Decisions (from prior research)

| Layer | Choice | Why |
|---|---|---|
| Package manager | `pnpm` | User preference, Qwik docs lead with pnpm, ~3× faster than npm, disk-cheap |
| Framework | `@builder.io/qwik@1.19.2` + `@builder.io/qwik-city@1.19.2` | Pinned to 1.19.2 — see "Pinning note" below |
| Build/dev | Vite 7 (Qwik's Vite plugin) | Qwik is Vite-native; no realistic alternative |
| Unit test | Vitest + `renderToString` from `@builder.io/qwik/server` | SSR path; sidesteps an upstream testing-layer regression |
| Container | `node:22-alpine` multi-stage | Matches existing Go service Dockerfile discipline |
| Orchestration | Extend root `docker-compose.yml` with `web_ui` service | Keep one source of truth for the witsaba stack |

### Pinning note (Qwik 1.19.2)

The empty starter scaffolded with `pnpm create qwik@latest` pulls
`@builder.io/qwik@1.20.1`. We pin both `@builder.io/qwik` and
`@builder.io/qwik-city` to `1.19.2` to avoid the 1.20 release's
additional refactor on top of an existing testing-layer regression.
**Track Qwik 1.20.x** and re-test against the latest patch; once
`createDOM` works on a stable environment, remove the pin and
re-evaluate.

### Testing — what works today, what doesn't

`@builder.io/qwik/testing.createDOM` has a regression that affects
1.19.x AND 1.20.x: the testing layer wraps the host in
`HTMLUnknownElement2` which polyfills internal DOM methods like
`isAncestor` on its own wrapper class. When Qwik's renderer then
tries to insert raw child nodes (from jsdom / happy-dom), the
wrapped parent calls `node.isAncestor(parent)` on a raw DOM child
that does not have the polyfill installed, throwing
`TypeError: node.isAncestor is not a function`.

Attempted workarounds and their outcomes:

1. **Downgrade jsdom to 24** — fails (same error).
2. **Switch to happy-dom** — fails (same error; not a DOM env issue).
3. **Polyfill `Node.prototype.isAncestor` in a setup file** — fails
   (Qwik's wrapper class doesn't inherit from the polyfilled
   Node.prototype the way the renderer expects).
4. **Switch to `renderToString` from `@builder.io/qwik/server`** —
   **works**. The SSR path never touches a live DOM, so the broken
   `createDOM` layer is bypassed entirely. The component still
   renders to HTML and the assertions hold.

Decision: ship with `renderToString`. When the upstream regression
is fixed, add a DOM-based happy-path test alongside the SSR test
for behaviour assertions (event handlers, lifecycle).

### Implementation Tasks

#### T1. Scaffold Qwik project ✅
- `pnpm create qwik@latest empty frontend/web_ui` (non-interactive)
- Empty starter, ESLint + Prettier configs preserved
- Commit: `3ffadab feat(web_ui): scaffold Qwik + Qwik City empty starter`

#### T2. Add Vitest ✅ (adapted)
- Vitest 2.1.x, @vitest/coverage-v8 2.1.x
- Separate `vitest.config.ts` (per Qwik docs guidance)
- jsdom environment (matches official docs)
- Test specs use `renderToString`, not `createDOM` (see "Testing" above)
- Commit: `dd6ddde feat(web_ui): skeleton home page, SkeletonCard component, Vitest`
- Commit: `3885449 test(web_ui): pin Qwik 1.19.2, switch specs to renderToString`

#### T3. Clean starter to single home page ✅
- Demo routes / components: none present in the empty starter, so
  nothing to delete beyond the original `Hi 👋` placeholder
- Keep only `src/routes/index.tsx` as the only top-level route
- Commit: `dd6ddde feat(web_ui): skeleton home page, SkeletonCard component, Vitest`

#### T4. Home page skeleton (front-engineer preview) ✅
- Visual structure: top nav, hero section, 2×2 grid of
  `SkeletonCard` placeholders, footer
- Pure CSS via `useStylesScoped$` + `?inline` imports
- No Tailwind yet (placeholder for design system decision in a later task)
- Each card has a title + 1-line description placeholder
- All event handlers use Qwik's `$` boundary convention
- Commit: `dd6ddde feat(web_ui): skeleton home page, SkeletonCard component, Vitest`

#### T5. Dockerfile for Qwik dev server ✅
- Multi-stage: `node:22-alpine` build → `node:22-alpine` runtime
- `deps` stage reuses install for `build` and `runtime`
- Build stage runs `pnpm install --frozen-lockfile` then `pnpm build`
- Runtime stage re-installs with `--prod` and adds `wget` for the
  docker compose healthcheck
- Default `CMD` runs the Vite dev server on `0.0.0.0:5173`
- `.dockerignore` keeps `node_modules`, `.git`, `dist`, `tmp` out of
  the build context
- Commit: `0cd8c4c build(web_ui): multi-stage Dockerfile + .dockerignore`

#### T6. Wire `web_ui` into root docker-compose.yml ✅
- New `web_ui` service at the bottom of `docker-compose.yml`
- `network_mode: host` (matches existing pattern, Linux target only)
- `depends_on: postgres: condition: service_healthy` — web UI only
  starts when Postgres is healthy
- `build: context: ./frontend/web_ui`
- Dev-friendly defaults: bind `5173`, HMR over host network
- `healthcheck: wget http://127.0.0.1:5173/` every 15s, 5 retries,
  30s `start_period` to absorb the dev server warm-up
- Header comment block updated with a service-order graph
- Commit: `a248d09 feat(compose): add web_ui service with healthcheck-based Postgres gate`

#### T7. Verify ✅
- `pnpm install` clean (native binaries built via `pnpm-workspace.yaml` allowBuilds)
- `pnpm lint` clean
- `pnpm test` green: 3/3 SkeletonCard SSR specs pass
- `pnpm build` successful: type-checked, lint-checked, 195ms
- `docker compose --env-file env.example config` renders the
  `web_ui` service block with the correct healthcheck + depends_on
- Commit: `3885449 test(web_ui): pin Qwik 1.19.2, switch specs to renderToString`

### Verification Plan — Results

| Check | Result | Evidence |
|---|---|---|
| `pnpm install` | ✅ | `pnpm-workspace.yaml` allowBuilds for esbuild / @parcel/watcher / sharp; native binaries built |
| `pnpm lint` | ✅ | Clean (no warnings, no errors) |
| `pnpm test` | ✅ | 3/3 SkeletonCard SSR specs pass in 16ms |
| `pnpm build` | ✅ | 195ms build, TypeScript clean, ESLint clean during build, all chunks emitted |
| `docker compose config` | ✅ | `web_ui` service block renders; `depends_on.postgres.condition=service_healthy`; healthcheck with wget |
| Linux host validation | ⏳ | Out of scope on Mac; will be re-run on the witsaba Linux host (Pi) as a follow-up |

### Risks / Follow-ups
- **Mac dev:** `network_mode: host` on Docker Desktop Mac means the
  container is on the VM's netns, not the LAN. Same caveat already
  documented in the root compose header. The Qwik dev server is
  still reachable at `localhost:5173` from the Mac because Docker
  Desktop forwards host ports on the VM.
- **Bun as runtime:** Not used. Bun adapter has known `routeAction$`
  bug (issue #5362). pnpm + Node only.
- **E2E:** Playwright wiring is a separate follow-up. Not bundled here.
- **Design system:** Placeholder cards are bare. Real design system
  (Tailwind? UnoCSS? Vanilla-extract?) is a separate decision.
- **Qwik 1.20.x upgrade:** Re-evaluate when the upstream
  `createDOM` regression is fixed. Until then, pin to 1.19.2.
- **CI:** No CI change in this PR. Future PR adds a workflow step
  that runs `pnpm test` and `pnpm build` for `frontend/web_ui/`.

### Evidence

Work-unit commits on `feat/qwik-web-ui-scaffold` (base: `cb67a23` on `main`):

| SHA | Conventional commit |
|---|---|
| `3ffadab` | feat(web_ui): scaffold Qwik + Qwik City empty starter |
| `dd6ddde` | feat(web_ui): skeleton home page, SkeletonCard component, Vitest |
| `0cd8c4c` | build(web_ui): multi-stage Dockerfile + .dockerignore |
| `a248d09` | feat(compose): add web_ui service with healthcheck-based Postgres gate |
| `3885449` | test(web_ui): pin Qwik 1.19.2, switch specs to renderToString |

This `odd/tasks/qwik-web-ui-scaffold.md` is committed in the same
PR, per the repo convention (`odd/tasks/<feature>.md` ships with
the feature PR).

Final merge via PR — never push to main directly (user preference).
