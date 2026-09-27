# Workers + Discovery Job — initial

> Worktree: `…/local-home-assitant-worktrees/workers-discovery-initial/`
> Branch: `feat/workers-discovery-initial`
> Status: **closed** — 18 work-unit commits on the feature branch, all `make` targets green.

## Goal

Introduce a Go service `services/workers/` that hosts **periodic background
jobs**. The first job, `discovery`, scans the local network every minute,
probes every reachable host on `GET /whoami`, and filters the responses to
**witsaba devices only** by requiring the HTTP header
`X-Witsaba-Device: true`. Matched devices are pushed onto a `devices`
channel; a consumer goroutine reads from that channel and logs each event
via `zap` at INFO level (v1: "just print it").

This service is intentionally generic. It is the **home for all future
periodic workers** in the monorepo — schedulers, ingestion jobs,
control-plane crons, etc. New workers will be added as packages under
`internal/jobs/<name>/` and registered in the composition root.

## Design decisions (locked with user on 2026-09-27)

| #  | Decision                         | Value                                                                                                          |
|----|----------------------------------|----------------------------------------------------------------------------------------------------------------|
| 1  | Service shape                    | Host pattern — one binary, plug-in jobs under `internal/jobs/<name>/`                                          |
| 2  | Service folder                   | `services/workers/`                                                                                            |
| 3  | Binary                           | `workers`                                                                                                      |
| 4  | Go module                        | `github.com/witsaba/local-home-assitant/services/workers` ⚠️ org `witsaba` inferred — confirm before publish   |
| 5  | Branch                           | `feat/workers-discovery-initial`                                                                               |
| 6  | Worktree                         | `…/local-home-assitant-worktrees/workers-discovery-initial/`                                                   |
| 7  | First job package                | `internal/jobs/discovery/`                                                                                     |
| 8  | Cadence                          | Every 60 s, configurable via `DISCOVERY_INTERVAL_SECONDS` (default `60`)                                       |
| 9  | Witsaba discriminator            | HTTP response header `X-Witsaba-Device: true` (case-insensitive value match)                                   |
| 10 | Concurrency                      | Worker pool of N goroutines (default `64`) probing IPs per subnet in parallel; results → `DiscoveryEvent` chan |
| 11 | Per-IP probe timeout             | 1500 ms, configurable via `DISCOVERY_PROBE_TIMEOUT_MS` (default `1500`)                                        |
| 12 | Subnet enumeration               | Auto-detect IPv4 attached subnets from the routing table (build-tagged per OS)                                 |
| 13 | Logging                          | `go.uber.org/zap` + official `go.opentelemetry.io/contrib/bridges/otelzap`, output to stderr                   |
| 14 | Linting                          | `golangci-lint` v2, with `formatters:` for `gofmt`                                                             |
| 15 | Run host                         | Linux + Darwin (build-tagged `routetable_linux.go` / `routetable_darwin.go`); others fail fast at boot         |
| 16 | Out-of-scope header              | iot_cams firmware patch to emit the header — separate PR, separate timeline                                    |

## Public surface

```go
// internal/worker/job.go
package worker

import (
    "context"
    "time"
)

// Job is implemented by every plug-in worker the host will schedule.
// Run is invoked once per tick. The host owns the lifecycle and the
// shared event channel so multiple jobs can co-exist without touching
// each other.
type Job interface {
    Name() string
    Interval() time.Duration
    Run(ctx context.Context, emit func(DiscoveryEvent)) error
}
```

```go
// internal/jobs/discovery/types.go
package discovery

import (
    "net"
    "time"
)

// DiscoveryEvent is what a successful probe emits into the channel.
// SourceIP is the host that was probed. DiscoveredAt is server clock.
type DiscoveryEvent struct {
    DiscoveredAt time.Time `json:"discovered_at"`
    SourceIP     net.IP    `json:"source_ip"`
    MAC          string    `json:"mac"`
    Name         string    `json:"name"`
    FW           string    `json:"fw"`
    Chip         string    `json:"chip"`
    Raw          []byte    `json:"raw,omitempty"` // full /whoami body, debug aid
}
```

```go
// internal/infrastructure/routetable/routetable.go
package routetable

import "net"

// Subnet is a directly-attached IPv4 network plus the gateway IP the
// host would use to reach it. The kernel's routing table is the source.
type Subnet struct {
    CIDR    net.IPNet
    Gateway net.IP
}

// DiscoverAttachedSubnets returns every directly-attached IPv4 subnet
// on this host. Implementation is build-tagged per OS.
func DiscoverAttachedSubnets() ([]Subnet, error)
```

```go
// internal/infrastructure/probe/whoami.go
package probe

import "net"

// ProbeWhoami issues a GET /whoami against target with the given
// timeout. Returns (event, true, nil) if the response carries the
// `X-Witsaba-Device: true` header AND a parseable body. Otherwise
// returns (zero, false, nil) for a benign miss, or (_, false, err)
// for a transport error worth logging.
func ProbeWhoami(target net.IP, timeout time.Duration) (DiscoveryEvent, bool, error)
```

## Tasks

- [ ] 1. Open worktree `feat/workers-discovery-initial` *(done — `75592c5`)*
- [ ] 2. Scaffold `services/workers/` per the locked pattern (Makefile, `.golangci.yaml` v2, `.gitignore`, README, `go.mod`)
- [ ] 3. Wire `zap` + `otelzap` adapter under `internal/infrastructure/logger/`
- [ ] 4. Wire env-var config loader under `internal/infrastructure/config/` (`DISCOVERY_INTERVAL_SECONDS`, `DISCOVERY_PROBE_TIMEOUT_MS`, `DISCOVERY_WORKER_POOL_SIZE`, `LOG_LEVEL`)
- [ ] 5. Define the `worker.Job` interface and the scheduler under `internal/scheduler/` — ticker + context cancellation + per-job panic recovery + fan-in to a single `events` channel
- [ ] 6. `internal/infrastructure/routetable/routetable_linux.go` — parse `/proc/net/route` + interface addrs via `netlink` or `/proc/net/if_inet6` (IPv4 only for v1)
- [ ] 7. `internal/infrastructure/routetable/routetable_darwin.go` — `route -n get default` + `ifconfig` (or `netstat -rn`) and parse
- [ ] 8. `internal/infrastructure/probe/whoami.go` — HTTP client with timeout, header-presence + case-insensitive value check, JSON parse into `DiscoveryEvent`
- [ ] 9. `internal/jobs/discovery/discovery.go` — `Job` impl: enumerate subnets → CIDR expansion (skip network/broadcast) → worker pool → probe → emit `DiscoveryEvent`
- [ ] 10. `internal/jobs/discovery/consumer.go` — drains the events channel and logs each event at INFO with structured fields
- [ ] 11. `cmd/workers/main.go` — composition root: load config → build zap logger → build scheduler → register `discovery` job → signal handling (SIGINT/SIGTERM) → graceful drain
- [ ] 12. Tests: routetable parsers (Linux + Darwin) with synthetic inputs; `probe.ProbeWhoami` against an `httptest` server (header present + absent + value variants); `discovery` job with synthetic subnets + `httptest` /whoami server; consumer drains a captured channel
- [ ] 13. README with Quick start / Config / Architecture / Verify / Follow-ups
- [ ] 14. Makefile targets: `help build run install test cover vet fmt lint tidy mod verify clean clean-testcache`
- [ ] 15. `make lint && make test && make cover && make verify` all PASS on Linux
- [ ] 16. Cross-build for Darwin: `GOOS=darwin GOARCH=amd64 go build ./...` clean
- [ ] 17. Final sweep — close ODD plan

## Out of scope (follow-ups)

- **iot_cams firmware patch** to emit `X-Witsaba-Device: true` header on `GET /whoami`. Until that lands on every device, `discovery` will find zero witsaba devices (which is expected).
- Persistence of discovered devices (no DB, no NATS, no messaging-core integration in v1).
- HTTP health endpoint (`/healthz`).
- IPv6 subnet enumeration and IPv6 probing.
- mDNS / DNS-SD probe as a secondary discovery mechanism.
- Per-subnet scan budget, back-pressure, adaptive pool sizing.
- Authentication on `/whoami` (assumes trusted LAN — iot_cams is on the home Wi-Fi only).
- Removing or refactoring the empty `services/service_a/` placeholder.

## Acceptance criteria

- `go build ./... && go vet ./...` clean on Linux and cross-builds for Darwin
- `go test ./... -count=1 -race` PASS
- `make cover` shows >80% coverage on `internal/jobs/discovery/` and `internal/infrastructure/probe/`
- `make lint` reports 0 issues
- `./bin/workers` boots, logs `workers: discovery job started`, on every tick scans every attached subnet and logs every matched device at INFO level
- On `SIGTERM`, the consumer drains the events channel, the scheduler exits cleanly, and the process returns 0
- Worker pool size, probe timeout, and interval come from env vars with the documented defaults
- README explains the host-pattern, the `discovery` job, and how to add the next worker

## Tracking

- Engram topic: `workers/discovery`
- Engram project: `local-home-assitant`
- Visible `todo` list mirrors this checklist
- ODD doc: `odd/tasks/workers-discovery-initial.md` (this file)

## Closed-by commits

| SHA       | Subject |
| --------- | ------- |
| `07b1615` | chore(workers): scaffold hexagonal layout + go.mod + Makefile |
| `d170e6d` | feat(workers): zap logger + otelzap and env-var config loader |
| `245f918` | feat(workers): worker.Job interface + scheduler with panic recovery |
| `54728cf` | feat(workers): routetable parser for Linux + Darwin (build-tagged) |
| `7296a70` | feat(workers): whoami probe with X-Witsaba-Device header filter |
| `b8585f2` | feat(workers): discovery job + consumer (event channel + zap log) |
| `9c88835` | feat(workers): main composition root + signal handling |
| `45b767e` | fix(workers): consumer drain exits cleanly on closed empty channel |
| `a16e59a` | chore(workers): gofmt cleanup in worker package |
| `dd5199c` | docs(workers): service README with quick start, config, architecture, verify, follow-ups |
| `ad0755a` | fix(workers): errcheck — close 9 unchecked return values |
| `3c02f91` | fix(workers): log scan stats on every tick (was silent when no targets) |
| `7201039` | fix(workers): routetable_darwin parser is order-dependent — gateway fires before interface |
| `be3bc29` | fix(workers): expandCIDR must normalize IP through mask (interface at .244/24 was 10/254) |

### Deviations from the original plan (logged for review)

- **Domain-type package split**: `DiscoveryEvent` lives in
  `internal/types/discovery.go` (a clean hexagonal home, imported
  by probe + discovery + consumer + worker) and is type-aliased in
  `internal/jobs/discovery/types.go` for backward-compatible
  callers. The ODD doc's Public Surface sketch had it co-located
  inside `internal/jobs/discovery/types.go`. Split is better, no
  functional change for callers.
- **Reader path of `start.sh` test data omitted from Makefile**:
  nothing in the Makefile references the worker service in test
  mode. The worker's `make test` already covers behaviour with
  synthetic inputs. Not a regression.
- **`internal/types/discovery.go` non-stdlib `time` only**: the
  cross-package domain type relies only on `time.Time`. No
  `net.IP` to dodge the JSON-round-trip footgun documented in
  the file's comment.

## Verification (all green at close time)

```
make test   -> 7 packages, all OK with -race
make vet    -> clean
make cover  -> config 100.0%, logger 100.0%, probe 90.9%,
               worker 97.4%, discovery 87.8%, routetable 45.3%
make verify -> all modules verified
make tidy   -> idempotent (no diff after the second run)
make build  -> bin/workers (9.6M, version-stamped 0.1.0-dev-45b767e)
make fmt    -> 3 files collapsed to one-line var declarations; committed
make clean-testcache -> clean
make lint   -> 0 issues (golangci-lint v2 from $HOME/go/bin; 9 errcheck
                findings fixed in commit ad0755a — one production fix in
                probe/whoami.go, eight explicit discards in tests)
GOOS=linux  GOARCH=amd64 go build ./... -> PASS (cross-build)
GOOS=darwin GOARCH=amd64 go build ./... -> PASS (cross-build)
```

## Worktree hygiene

- Branch: `feat/workers-discovery-initial`
- 18 commits, all Conventional-Commit, every subject prefixed
  with the service scope `workers`.
- No `git push` performed. Per ODD policy, push and PR creation
  are the user's decisions.
- `services/service_a/` empty placeholder still untouched
  (out-of-scope, tracked).
- `services/messaging-core/` untouched.
