# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Plain static HTML, CSS and JavaScript in `frontend/web_ui/static/` — three
pages, one stylesheet, one script. No framework, no build step, no
`node_modules`, and no JavaScript runtime on the serving path. nginx copies
the tree to `~/.witsaba/nginx/html` and serves it from disk
(`scripts/install/13-nginx.sh`), supervised as `witsaba-nginx.service` in
user space. It also reverse-proxies `/api/*` to messaging-core on 8081 and
`/stream/*` to the WebSocket gateway on 8080, so the browser makes one
same-origin request and nothing needs CORS.

The web UI is a sibling of the Go services in `services/workers` and
`services/messaging-core`; it reads the same Postgres database through those
services. The backend runs containerized via the root `docker-compose.yml`
(postgres, messaging-core, workers). Deployed on a Raspberry Pi on a home
LAN; the UI is reachable at `http://<pi-hostname>:4173/`.

## Users

The primary user is a single self-hosting operator who owns and
maintains the witsaba stack on a Raspberry Pi connected to their
home LAN. They are technically literate — comfortable with Docker
Compose, Go services, Postgres roles, and reading logs. They run the
stack on a closed network (no public exposure) and want a calm,
reliable, daily-driver surface that they can check in on without
friction. They are not a casual home user and they are not a large
team; they are a "build-it-once-and-keep-it-tidy" operator.

## Product Purpose

Witsaba is a custom home assistant stack for IoT cameras, sensors,
and actuators on a home LAN. Its purpose is to discover what is on
the network, persist what is found, route messages between services,
and give the operator a clear, trustworthy view of the whole system.
The web UI is the operator's window into that system — a single page
that tells them, at a glance, "is everything up, what changed, and
where do I go next?"

## Positioning

Witsaba is built from the device-discovery primitive up. The
`workers` service actively probes the LAN for cameras, while most
home-assistant stacks wait for devices to announce themselves. This
matters because the operator's cameras (e.g. ESP-based IoT cams on
192.168.1.x) do not advertise themselves; they need to be found.
The web UI's job is to make that discovery legible and trustworthy.

## Operating Context

The operator runs the stack on a Raspberry Pi on a home network,
typically from a kitchen counter, a desk, or a closet. They open
the UI in a desktop browser while sitting at the same network. The
ambient light varies (day, evening, lamplight) so the surface should
hold up across lighting conditions. They check the UI in short
sessions (a few minutes) to confirm everything is up after a
restart, to watch a new device appear, or to read recent logs. They
do not want to be entertained; they want to be reassured and
informed.

## Capabilities and Constraints

Confirmed capabilities on the path to a usable home page:

- Workers service performs active LAN discovery and writes to the
  `witsaba.devices` table in Postgres.
- Messaging-core is a NATS-based message bus between services.
- Postgres is the system of record; roles are scoped
  (`pg-admin`, `pg-worker`, `pg-messaging-core`).
- Docker Compose runs the three backend services with healthcheck-based
  service ordering (`workers` waits for `postgres` to be healthy). The
  front-end is not a container: nginx runs in user space beside them.
- The web UI is three pages: home, device list, camera stream. Two of
  the four feature cards work; Discovery, Logs and Settings are
  placeholders with no page behind them yet.

Constraints:

- Linux host networking (the stack assumes the Pi, not Docker
  Desktop Mac, is the deployment target).
- There is no front-end toolchain. Changing the UI means editing HTML,
  CSS or JavaScript by hand and re-running `13-nginx.sh`; there is no
  package manager, no bundler and no test runner in the serving path.
- The operator does not want noise: no orchestrations, no
  decorative motion, no gamified feedback.

## Brand Commitments

The product name is **witsaba** (lowercase). Tone is calm,
technical, and direct; copy is operator-grade, not marketing.
No mascot, no friendly illustrations, no marketing language on
operator surfaces.

## Evidence on Hand

- The witsaba repository at the current commit (see git log) with
  the full Go service stack in `services/` and the static front-end
  in `frontend/web_ui/static/`.
- The root `docker-compose.yml` showing the three-service backend
  topology.
- `scripts/install/13-nginx.sh` and `12-systemd-services.sh`, which
  deploy and supervise the front-end; `test-nginx-config.sh` asserts
  the generated config and the deployed tree.
- `env.example` documenting the environment variables, including
  `DISCOVERY_INTERVAL_SECONDS` and `POSTGRES_*`.
- `odd/tasks/plain-frontend-nginx.md` and `odd/tasks/camera-stream-ui.md`
  documenting how the static front-end replaced the framework one and how
  the stream viewer was built.

State of evidence the front end cannot fabricate:

- No real device inventory yet — the `witsaba.devices` table is
  empty in the user's local environment (Pi validation pending).
- No real logs to display yet — only the workers boot log.
- No real user accounts, no auth, no multi-tenant model.

The first surface must be honest about this: show real,
ground-truth data and label anything synthetic as such.

## Product Principles

1. **Show the system, don't narrate it.** The home page is a
   status board, not a story. Numbers, states, and the names of
   services speak for themselves.
2. **One way to do the common thing.** A device, a discovery run,
   a log line: one path each, no modal detours.
3. **The operator is technical.** The UI assumes comfort with
   concepts like "service", "healthy", "role", "discovery probe".
   No glossary, no hand-holding.
4. **Quiet by default.** No orchestrated page-load sequences, no
   ambient animation. Motion is reserved for state change.
5. **Honest about what's wired up.** A placeholder card looks
   like a placeholder card, not a fake feature. A "not yet
   connected" state is labelled, not hidden.

## Accessibility & Inclusion

- Keyboard-first navigation; visible focus states on every
  interactive element.
- Color is never the only signal: status uses icon + label, not
  only color.
- Body text and placeholder text must meet WCAG AA contrast (4.5:1
  for body, 3:1 for large text).
- Honors `prefers-reduced-motion`; motion is suppressed entirely
  when the user opts out.
- Honors `prefers-color-scheme`; a dark variant is part of the
  delivered surface from day one (operators configure at night).
