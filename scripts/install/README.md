# Witsaba native Linux installation

User-space installation for a small Linux host (target: 1GB Raspberry Pi).
Everything lands under `~/.witsaba`; no root is required, and no port below
1024 is bound.

## Running it

```bash
./scripts/install/install-ubuntu.sh          # everything
./scripts/install/install-ubuntu.sh --skip-build
```

Steps are also runnable on their own, in order:

| Step | What it does |
|---|---|
| `00-brew.sh` | locate or install Homebrew |
| `01-postgresql.sh` | PostgreSQL 16 in `~/.witsaba/postgres`, 1GB-RAM profile, generated credentials |
| `02-go.sh` | Go toolchain |
| `03-node.sh` | Node 22 + pnpm (only needed for the optional Qwik path) |
| `04-postgres-init.sh` | database, roles, schema, grants, privilege assertions |
| `13-nginx.sh` | nginx, unprivileged config, deploy the static UI |
| `12-systemd-services.sh` | the five user units |

Build and deploy:

| Step | What it does |
|---|---|
| `10-build-go.sh` | build the two Go services. **Skipped automatically under ~1.4GB RAM** |
| `11-build-frontend.sh` | build the Qwik frontend. **Opt-in**, see below |

## Cross-compiling the Go services

A cold build of the messaging-core dependency tree does not fit in 1GB of RAM.
On the Pi it becomes I/O bound on SD-card swap rather than CPU bound:

```
%Cpu(s):  4.5 us, 7.3 sy, 41.8 id, 46.4 wa
Mem: 828MB used of 899MB, 567MB in swap
concurrent compiles: 446MB RSS (ugorji/go/codec) + 185MB (nats-server)
```

Serialising to `-p 1` with `GOMAXPROCS=1` raised compile CPU from 19% to 99%,
but the single `ugorji/go/codec` compile is still ~408MB RSS. So build on a
workstation and copy the binary — 30 seconds there:

```bash
# on the workstation
./scripts/install/10-build-go.sh --target arm64 --out ./build
scp ./build/messaging-core ./build/workers \
    liwaisi@192.168.1.115:~/.witsaba/bin/
```

`CGO_ENABLED=0` is already set, so no target toolchain or sysroot is involved.

## Two frontend paths

The Pi serves a plain static frontend. The Qwik scaffold is still in the repo,
because it is still useful for framework work on a workstation, but it is not
what runs on the Pi.

| | nginx + static HTML (default on the Pi) | Qwik / Vite (opt-in) |
|---|---|---|
| Runtime RAM | **8 MB** | 143 MB |
| Disk | 52 KB | 308 MB of `node_modules` |
| Build step | none | ~1.5 min |
| Needs Node at runtime | no | yes |
| Feature parity | home + device list | same, plus the Qwik test suite |

Enable the Qwik path with `WITSABA_WITH_QWIK=1`. It does not change what nginx
serves; it just also populates `~/.witsaba/frontend`.

Both paths target the same two pages and the same endpoint. The Qwik app used
`routeLoader$` to fetch `/api/devices/active`; the static version does one
`fetch` from the browser instead.

## One origin, no CORS

nginx serves the files and reverse-proxies the backend:

```
/            -> files in ~/.witsaba/nginx/html
/api/*       -> 127.0.0.1:8081   messaging-core REST
/stream/*    -> 127.0.0.1:8080   messaging-core WebSocket gateway
```

The browser only ever talks to port 4173, so there is no CORS preflight and
`messaging-core` needs no change. `/stream/` carries the `Upgrade` and
`Connection` headers a WebSocket needs, and `proxy_buffering off` keeps
camera frames from being held back.

## Credentials

`01-postgresql.sh` generates the database passwords rather than shipping
literals: 48 lowercase hex characters from `/dev/urandom`, 192 bits.

Hex specifically, because the value is parsed by three grammars that disagree —
a single-quoted SQL literal, `KEY=VALUE` sourced by bash, and systemd
`EnvironmentFile`. `[0-9a-f]` is inert in all three and has no visually
ambiguous characters. The smaller alphabet is paid for by length.

An existing strong pair is preserved on re-run, because rotating underneath a
running deployment would leave the services holding credentials the database no
longer accepts. Known placeholders and low-entropy values *are* replaced in
place. To force a rotation:

```bash
./scripts/install/01-postgresql.sh --rotate-passwords
./scripts/install/04-postgres-init.sh      # applies them to the roles
```

Both must run together. `04` re-applies the passwords on every run and asserts
the read-only role is actually rejected on write.

## Memory

Measured with the full stack running on the Pi:

| Process | RAM |
|---|---|
| postgres (9 processes) | ~78 MB |
| messaging-core | ~21 MB |
| workers | ~17 MB |
| nginx | ~8 MB + 4 MB worker |
| **witsaba total** | **~112 MB** |
| OS baseline | ~240 MB |

Tuning that matters: `shared_buffers=128MB`, `work_mem=4MB` (16MB × 20
connections was a 320MB worst case), parallel query workers disabled, and
`synchronous_commit=off` for SD-card wear.

## Tests

None of these need a database, a network, or root.

```bash
./scripts/install/test-password-gen.sh   # 42 assertions: generator, PATH invariants
./scripts/install/test-scripts-load.sh   # 30 assertions: every helper resolves
./scripts/install/test-nginx-config.sh   # 25 assertions: site, config, live proxy
```

`test-scripts-load.sh` exists because `bash -n` only validates syntax. A script
can pass it and still call a function that was never defined, which only
surfaces at runtime on the target.

## Operating

```bash
systemctl --user status witsaba-nginx
journalctl --user -u witsaba-nginx -f
journalctl --user -u witsaba-messaging-core -f

systemctl --user restart witsaba-messaging-core
systemctl --user reload witsaba-nginx      # after editing nginx.conf

curl http://localhost:4173/api/devices/active
```

Services start at boot because `loginctl enable-linger` is set. Without it the
user manager is not started at boot and nothing here runs unattended:

```bash
sudo loginctl enable-linger $USER
```

## Removing

```bash
./scripts/install/uninstall-ubuntu.sh
```

Stops and removes the units, deletes `~/.witsaba`, optionally removes the
repository, and restores `~/.bashrc` from a backup. Homebrew is left in place.
