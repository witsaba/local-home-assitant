# witsaba-web-ui

Front-end for the [witsaba local-home-assistant](../..) stack.

Qwik + Qwik City, scaffolded with the official `empty` starter.
The repo is intentionally minimal: one home route, one placeholder
component (`SkeletonCard`), and a Vitest config wired up for the
official `@builder.io/qwik/testing` layer.

## Stack

- **Qwik / Qwik City** — resumable UI, server-rendered first.
- **Vite 7** — Qwik's build pipeline.
- **Vitest + jsdom** — unit / component tests, official Qwik testing API.
- **pnpm** — package manager (Linux/macOS/Windows arm64 supported).
- **Node.js 18.17+** — required at build and run time.

## Scripts

```sh
pnpm install                  # install dependencies
pnpm dev                      # vite dev server (SSR mode)
pnpm build                    # production build (dist/ + server/)
pnpm preview                  # build + serve the production preview
pnpm test                     # one-shot vitest run
pnpm test.watch               # vitest watch mode
pnpm test.coverage            # vitest with v8 coverage
pnpm lint                     # eslint
pnpm fmt                      # prettier --write
```

## Layout

```
frontend/web_ui/
├── src/
│   ├── components/
│   │   └── skeleton-card/    # placeholder card used on the home page
│   ├── routes/
│   │   └── index.tsx         # the only route — home page wireframe
│   ├── entry.dev.tsx         # dev-mode entry
│   ├── entry.preview.tsx     # `vite preview` entry
│   ├── entry.ssr.tsx         # SSR entry (used by build + adapter)
│   ├── root.tsx              # <html>/<head>/<body> shell
│   └── global.css            # base reset + design tokens (placeholder)
├── public/                   # static assets served as-is
├── vite.config.ts            # Vite + Qwik plugins
├── vitest.config.ts          # separate Vitest config (per Qwik docs)
└── package.json
```

## Docker

This service is wired into the **root** `docker-compose.yml` as
`web_ui`. The container:

- listens on the host network on port `5173` (dev) / `4173` (preview),
- `depends_on: postgres: condition: service_healthy` — the UI never
  starts before Postgres is accepting connections.

See the [top-level `docker-compose.yml`](../../docker-compose.yml) and
[`odd/tasks/qwik-web-ui-scaffold.md`](../../odd/tasks/qwik-web-ui-scaffold.md)
for the full integration plan.

## Notes

- This is a **skeleton**, not a finished product. The `SkeletonCard`
  action buttons are intentionally `disabled` until a real feature
  wires them up.
- The design system is **not** chosen yet. Tailwind, UnoCSS, and
  vanilla-extract are all options; that decision is a separate task.
- Bun is **not** used as the runtime — the Qwik Bun adapter has known
  `routeAction$` bugs. pnpm + Node is the supported path.
