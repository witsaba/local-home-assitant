# Qwik Web UI Scaffold - Task Tracking

## Feature: Qwik Web UI Scaffold + Docker Integration

### Goals
1. Scaffold a Qwik + Qwik City project at `frontend/web_ui/`
2. Use pnpm as the package manager (user preference, Qwik docs default)
3. Add Vitest with the official `@builder.io/qwik/testing` layer (Alternative A from research)
4. Clean the Qwik starter down to a single skeleton home page (front-engineer preview style)
5. Add a multi-stage Dockerfile for the Qwik dev server (Node 22 alpine, linux/arm64-friendly)
6. Wire the new `web_ui` service into the existing root `docker-compose.yml` with `condition: service_healthy` on Postgres so the dev server only starts once the DB is up
7. Verify: `pnpm build`, `pnpm test`, `docker compose config` validation

### Non-Goals
- E2E tests (Playwright) — added later in a follow-up
- Production SSR adapter (Bun/Node/Cloudflare) — out of scope for this scaffold
- Auth, routing depth, design system, real API integration — placeholder skeleton only
- Bun runtime — pnpm + Node only (Bun adapter has known `routeAction$` bugs; flagged in research)

### Stack Decisions (from prior research)

| Layer | Choice | Why |
|---|---|---|
| Package manager | `pnpm` | User preference, Qwik docs lead with pnpm, ~3× faster than npm, disk-cheap |
| Framework | `@builder.io/qwik` v1.x + `@builder.io/qwik-city` | Stable v1, all official docs target v1, v2 is still `@qwik.dev/core` beta |
| Build/dev | Vite (Qwik's Vite plugin) | Qwik is Vite-native; no realistic alternative |
| Unit test | Vitest + `@builder.io/qwik/testing` | "De facto" per Qwik docs; jsdom fast; no real-browser cost on every test |
| Container | `node:22-alpine` multi-stage, distroless-style runtime | Matches existing Go service Dockerfile discipline |
| Orchestration | Extend root `docker-compose.yml` with `web_ui` service | Keep one source of truth for the witsaba stack |

### Implementation Tasks

#### T1. Scaffold Qwik project
- Run `pnpm create qwik@latest` interactively (or equivalent non-interactive flags) inside `frontend/web_ui/`
- Pick "Empty App" starter (no sample routes / no demo data) so the skeleton stays minimal
- Accept defaults otherwise (TypeScript yes, ESLint yes, Prettier yes, Vitest deferred to T2)

#### T2. Add Vitest + official Qwik testing
- `pnpm run qwik add vitest` (adds Vitest config, sample spec, jsdom)
- Replace the generated `src/components/example` with a hand-written skeleton component (see T4)
- Verify `pnpm test` runs the placeholder test green
- Add a separate `vitest.config.ts` (NOT in `vite.config.ts`) per Qwik docs guidance

#### T3. Clean starter to single home page
- Delete unused routes: keep only `src/routes/index.tsx`
- Delete the demo routes (`/flower`, `/demo/*`) and the demo component
- Replace `src/routes/index.tsx` with a minimal skeleton: header, hero placeholder, two empty card placeholders, footer
- Layout kept (root.tsx, layout.tsx) — only the home page is filled in
- No "Learn Qwik" or "Get Started" boilerplate copy anywhere

#### T4. Home page skeleton (front-engineer preview)
- Visual structure: top nav, hero section, 2×2 grid of feature-card placeholders, footer
- Pure CSS, no Tailwind yet (placeholder for design system decision in a later task)
- Each card has a title + 1-line description placeholder
- Comment at top of file explains: "Skeleton — replace with real content in feature task"
- Respects Qwik's `$` lazy-boundary contract: every event handler is `onClick$` (etc.)

#### T5. Dockerfile for Qwik dev server
- Multi-stage: `node:22-alpine` build → `node:22-alpine` runtime
- Build stage runs `pnpm install --frozen-lockfile` then `pnpm build`
- Runtime stage: `pnpm install --prod --frozen-lockfile`, copy built `dist/` and `server/`
- Default `CMD` runs `pnpm run preview` (production preview on port 4173)
- For dev, override `command:` in compose to `pnpm run dev --host 0.0.0.0`
- `.dockerignore` to keep `node_modules`, `.git`, `dist`, `tmp` out of the build context

#### T6. Wire `web_ui` into root docker-compose.yml
- New service `web_ui` at the bottom of `docker-compose.yml`
- `network_mode: host` (matches existing pattern, Linux target only)
- `depends_on: postgres: condition: service_healthy` — web UI only starts when Postgres is healthy
- `build: context: ./frontend/web_ui`
- Dev-friendly defaults: bind 5173, HMR over host network
- Document in the file's header comment why we don't add a `web_ui → messaging-core` dependency yet (messaging-core has no healthcheck; will be revisited)

#### T7. Verify
- `pnpm install` clean
- `pnpm lint` clean
- `pnpm test` green (placeholder test)
- `pnpm build` produces `dist/` and `server/`
- `docker compose config` validates (no service start needed since Postgres is already running on host)
- Inspect produced `docker-compose.yml` for the new service block

### Verification Plan
- Static: ESLint, TypeScript via `pnpm build`
- Unit: Vitest placeholder spec runs and passes
- Container: `docker compose config` parses; `docker compose build web_ui` builds image (image-only, no service start)
- Manual on Linux host (out of scope here, mentioned in PR description): `docker compose up web_ui` should wait for Postgres to be healthy before starting

### Risks / Follow-ups
- **Mac dev:** `network_mode: host` on Docker Desktop Mac means the container is on the VM's netns, not the LAN. Same caveat already documented in the root compose header. The Qwik dev server is still reachable at `localhost:5173` from the Mac because Docker Desktop forwards host ports on the VM. Acceptable for local-only UI dev.
- **Bun as runtime:** Not used. Bun adapter has known `routeAction$` bug (issue #5362). pnpm + Node only.
- **E2E:** Playwright wiring is a separate follow-up. Not bundled here.
- **Design system:** Placeholder cards are bare. Real design system (Tailwind? UnoCSS? Vanilla-extract?) is a separate decision.
- **CI:** No CI change in this PR. Future PR adds a workflow step that runs `pnpm test` and `pnpm build` for `frontend/web_ui/`.

### Evidence
- Work-unit commits will be added on this branch as tasks complete. Each commit message follows Conventional Commits (`feat:`, `chore:`, `test:`, `build:`, `docs:`).
- Final merge via PR — never push to main directly (user preference).
