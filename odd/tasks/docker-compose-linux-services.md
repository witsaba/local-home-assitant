# Feature: Docker Compose for messaging-core + workers (Linux target)

## Goal

Add Docker Compose for the two existing Go services (`services/messaging-core`,
`services/workers`) so they can run on a **Linux** target. Validate that, once
running, the `workers` container can find the witsaba camera at `192.168.1.51`
via its existing `discovery` job.

This branch is **Docker artifacts only**. It does not change Go source code in
either service. It does not add NATS auth, TLS, producers, consumers, or
workers→messaging-core wiring — those are existing follow-ups in each
service's README.

## Design decisions (locked with user on 2026-09-27)

| #  | Decision                         | Value                                                                                                       |
|----|----------------------------------|-------------------------------------------------------------------------------------------------------------|
| 1  | Compose file                     | `docker-compose.yml` at repo root (modern Compose spec, no `version:` key)                                  |
| 2  | Target platform                  | **Linux only** (user decision). Mac dev deferred.                                                           |
| 3  | Networking                       | `network_mode: host` for both services. Worker keeps its existing auto-subnet detection — no code change.   |
| 4  | Build image                      | `golang:1.26.3-alpine3.23` — verified 66.36 MB compressed on Docker Hub, smallest official Go 1.26 image.   |
| 5  | Runtime image                    | `alpine:3.23` (matches build libc = musl; smaller than `scratch` and easier to `docker exec` for debug).   |
| 6  | CGO                              | `CGO_ENABLED=0` — required for clean musl-based static binaries; both services are pure-Go safe.           |
| 7  | Version stamping                 | `-ldflags "-X main.version=$VERSION-$GIT_HEAD"` via Docker `ARG`s, matching each service's existing Makefile.|
| 8  | Env configuration                | `.env` (gitignored) + `.env.example` (committed) at repo root                                               |
| 9  | Image names                      | `witsaba/messaging-core:local`, `witsaba/workers:local` (built locally, not pushed)                          |
| 10 | depends_on                       | `workers` waits for `messaging-core` (documents intent — even though v1 worker does not talk to NATS yet)   |
| 11 | Restart policy                   | `unless-stopped` for both                                                                                   |
| 12 | Healthchecks                     | Not added. Out of scope; flagged for follow-up. Neither service exposes `/healthz` today.                   |
| 13 | Mac dev workflow                 | Not supported on this branch. `network_mode: host` on Docker Desktop puts the container on the VM's network, not the Mac LAN. The worker will NOT see `192.168.1.51` from this machine. Documented; defer Mac support. |

## Rationale for build image choice

User asked to pick based on "lower power consumption and lower footprint keeping
capabilities". Evidence:

- `golang:1.26.3-alpine3.23` = **66.36 MB compressed** on Docker Hub (verified
  via the official layers page). By contrast the default `golang:1.26` (Debian
  full) is ~300+ MB compressed, and `golang:1.26.3-bookworm` is ~80–100 MB.
- Same libc (musl) and same package manager (apk) as the runtime stage →
  cross-stage binary compatibility without surprise, and the option to add
  build deps with `apk add --no-cache` if a future module needs them.
- Capabilities preserved: full Go 1.26.3 toolchain (matches `go 1.26.3` in
  `go.mod`), CGO disabled for both services, no musl/glibc ABI mismatch.

## Public surface (this branch)

No new Go types. New filesystem surface:

```
docker-compose.yml                       ← Compose definition, two services
.env.example                             ← Documented env vars (committed)
.gitignore                            ← adds `.env` line (ignore, not track)
services/messaging-core/Dockerfile       ← Multi-stage build, linux/amd64+arm64
services/messaging-core/.dockerignore    ← Ignore bin/, coverage.*, *.test
services/workers/Dockerfile              ← Multi-stage build, linux/amd64+arm64
services/workers/.dockerignore           ← Ignore bin/, coverage.*, *.test
services/messaging-core/README.md        ← Add "Docker" section
services/workers/README.md               ← Add "Docker" section
```

## Tasks (one work-unit commit each)

- [ ] 1. Plan file committed (`odd/tasks/docker-compose-linux-services.md`)
- [ ] 2. `docker-compose.yml` at repo root (two services, network_mode: host)
- [ ] 3. `.env.example` at repo root (every env var consumed by either service)
- [ ] 4. `.gitignore` — add `.env` line so `.env` is ignored, `.env.example` stays
- [ ] 5. `services/messaging-core/.dockerignore`
- [ ] 6. `services/messaging-core/Dockerfile` (multi-stage, `golang:1.26.3-alpine3.23` → `alpine:3.23`)
- [ ] 7. `services/messaging-core/README.md` — add "Docker" section
- [ ] 8. `services/workers/.dockerignore`
- [ ] 9. `services/workers/Dockerfile`
- [ ] 10. `services/workers/README.md` — add "Docker" section

## Out of scope (follow-ups)

- **Linux host validation**: deferred. Cannot run on this Mac dev machine.
- **NATS auth / TLS**: tracked in `services/messaging-core/README.md`.
- **Producer / consumer in messaging-core**: same doc.
- **Workers → messaging-core integration**: tracked in `services/workers/README.md`.
- **Health endpoints (`/healthz`)**: same docs.
- **CI pipeline to build images**: not in scope.
- **Mac dev workflow with `ipvlan` / `macvlan`**: not in scope for this branch.

## Acceptance criteria

- `docker compose config -q` exits 0 (validates the compose file).
- `docker compose build` succeeds for both services.
- Both images are reproducible (pinned base tags, build ARGs default to
  `0.1.0-dev` / `unknown` when not provided).
- `.env.example` documents every env var the services consume with their
  defaults.
- Every work-unit commit is a single Conventional Commit scoped to one
  logical change.
- After every commit the worktree `git status` is clean.
- On a Linux target the user can run `docker compose up -d` and observe, in
  `docker compose logs workers`, the line `witsaba device found` with
  `source_ip: 192.168.1.51`. **This validation cannot run on this Mac dev
  machine** — explicitly documented in both service READMEs.

## Tracking

- Worktree: `~/workspace/witsaba/repositories/local-home-assitant-worktrees/docker-compose-linux-services/`
- Branch: `feat/docker-compose-linux-services`
- odd/tasks file: `odd/tasks/docker-compose-linux-services.md` (this file)
- Visible `todo` list mirrors the task checklist above

## Closed-by commits

(populated as each work-unit commit is made)

| # | SHA       | Subject                                              |
|---|-----------|------------------------------------------------------|
|   |           |                                                      |