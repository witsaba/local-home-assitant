# syntax=docker/dockerfile:1.7
#
# Dockerfile for the witsaba-web_ui Qwik dev/preview service.
#
# Target: linux/amd64 and linux/arm64 (Raspberry Pi included).
# Runtime: node:22-alpine. Build deps only live in the build stage.
#
# Modes:
#   default (docker compose up web_ui)
#     pnpm dev --host 0.0.0.0  -> Vite dev server on :5173, HMR over
#     the host network (compose uses network_mode: host on Linux).
#
#   preview
#     docker run --rm -p 4173:4173 \
#       witsaba/web-ui:local \
#       pnpm preview --host 0.0.0.0
#     -> production preview server on :4173, after `pnpm build` has
#     run inside the build stage.
#
# For a real production deploy (Node adapter / static), add a second
# stage or a separate Dockerfile.prod with the chosen adapter. Out of
# scope for the current scaffold.

ARG NODE_VERSION=22.11.0

# ----------------------------------------------------------------------------
# deps: install the full dev dep tree once. Reused by build and runtime stages
# so the runtime stage can layer its own `--prod` install on top.
# ----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS deps
WORKDIR /app

# pnpm via corepack (officially recommended for Node 22+).
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
RUN corepack enable && corepack prepare pnpm@11.8.0 --activate

# Copy only manifests first so Docker can cache the install layer
# independently of source changes.
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

# ----------------------------------------------------------------------------
# build: produce dist/ (client) and server/ (SSR) artifacts. Skipped at
# runtime unless a CI step or operator wants the prod preview build.
# ----------------------------------------------------------------------------
FROM deps AS build
WORKDIR /app
COPY . .
# `qwik build` runs build.types + build.client + build.preview. We keep
# preview artefacts around so the runtime stage can serve them if the
# operator chooses preview mode.
RUN pnpm run build

# ----------------------------------------------------------------------------
# runtime: minimal image. Re-installs deps in --prod mode to drop dev
# tooling, then brings in the build artefacts.
# ----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS runtime
WORKDIR /app

ENV NODE_ENV=production
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
ENV HOST=0.0.0.0
ENV PORT=5173

RUN corepack enable && corepack prepare pnpm@11.8.0 --activate

# Re-install without devDependencies. We can't --frozen-lockfile here
# because the lockfile describes the full tree; --prod reads it
# correctly and only pulls runtime deps.
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --prod

# Copy built artefacts and any source files needed at runtime.
COPY --from=build /app/dist ./dist
COPY --from=build /app/server ./server
COPY --from=build /app/public ./public

EXPOSE 5173

# Default to the dev server because this image is consumed by the
# docker-compose service. Override the command for preview/prod.
CMD ["pnpm", "run", "dev", "--host", "0.0.0.0"]
