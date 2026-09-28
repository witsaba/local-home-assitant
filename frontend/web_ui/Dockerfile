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

ARG NODE_VERSION=22.13.0

# ----------------------------------------------------------------------------
# deps: install the full dependency tree once. Reused by build and
# runtime stages as a starting point for each.
# ----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS deps
WORKDIR /app

# pnpm installed via npm (corepack has outdated keys that fail signature
# verification for pnpm >= 9 in node:22-alpine images).
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
RUN npm install -g pnpm@11.8.0

# Copy only manifests first so Docker can cache the install layer
# independently of source changes.
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
RUN pnpm install

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
# runtime: minimal image. Copies node_modules from the build stage
# and brings in the built artefacts. No network calls required.
# ----------------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS runtime
WORKDIR /app

ENV NODE_ENV=production
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
ENV HOST=0.0.0.0
ENV PORT=5173

RUN npm install -g pnpm@11.8.0

# wget is used by the docker compose healthcheck. node:22-alpine does
# not ship it, and we want a small, deterministic install.
RUN apk add --no-cache wget

# Copy manifest files so pnpm can read scripts from package.json.
# Do NOT run pnpm install here - node_modules is already in the build
# stage and copied below. This avoids network calls to npmjs.org.
COPY --from=build /app/package.json /app/pnpm-workspace.yaml /app/pnpm-lock.yaml ./

# Copy built artefacts and node_modules from the build stage.
# Note: there is intentionally no `server/` copy. Qwik only emits a
# server/ directory when an SSR adapter is installed via
# `pnpm qwik add` (e.g. qwik-city-node), which this scaffold does not
# have. The runtime CMD runs the Vite dev server against /app/src and
# does not need a prebuilt server bundle.
COPY --from=build /app/dist ./dist
COPY --from=build /app/public ./public
COPY --from=build /app/node_modules ./node_modules

EXPOSE 5173

# Default to the dev server because this image is consumed by the
# docker-compose service. Override the command for preview/prod.
CMD ["pnpm", "run", "dev", "--host", "0.0.0.0"]
