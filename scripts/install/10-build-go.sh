#!/bin/bash
# =============================================================================
# 10-build-go.sh - Build the Go services (messaging-core, workers)
# =============================================================================
# Two modes:
#
#   native       (no args)                    build for this machine
#   cross        --target <goarch> --out DIR build for linux/<goarch> into DIR
#
# Why cross-compile is the recommended path on the target Pi
#
# Measured on the Pi (4 cores, 899MB RAM, 1GB swapfile on an SD card), a native
# build of the messaging-core dependency tree spends its wall clock in iowait
# rather than on the CPU:
#
#     %Cpu(s):  4.5 us,  7.3 sy, 41.8 id, 46.4 wa
#     Mem: 828MB used of 899MB, 567MB of that in swap
#     two concurrent compiles: 446MB RSS (ugorji/go/codec) + 185MB
#                              (nats-io/nats-server/v2/server)
#
# Those two packages alone exceed available RAM, so the kernel evicts pages to
# SD-card swap and the build becomes I/O bound. Serialising to -p 1 with
# GOMAXPROCS=1 did lift compile CPU from 19% to 99%, but the single
# ugorji/go/codec compile is still ~408MB RSS, so it drifts back into swap and
# the build still does not finish in reasonable time.
#
# CGO_ENABLED=0 makes these services trivially cross-compilable: no target
# toolchain, no sysroot, no target libraries. Build on a workstation and copy
# the binary. Seconds there instead of tens of minutes of swap thrashing here.
#
# Everything slow still runs under run_progress, and -v is passed to go build so
# each package appears as it is compiled.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba/bin"

. "$SCRIPT_DIR/_lib.sh"

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; NC='\033[0m'
log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err()  { echo -e "${RED}[x]${NC} $1" >&2; }

usage() {
    cat <<'USAGE'
Usage: 10-build-go.sh [--target <goarch>] [--out <dir>]

  (none)                native build for this machine, installed to
                        ~/.witsaba/bin
  --target <goarch>     cross-compile for linux/<goarch> (arm64, amd64, ...)
  --out <dir>           output directory (default ~/.witsaba/bin)
  -h, --help            this message

Environment:
  WITSABA_HEARTBEAT_SECS     heartbeat interval while building   (20)
  WITSABA_BUILD_PARALLELISM  go build -p, 0 = auto               (auto)
  WITSABA_BUILD_GOMAXPROCS   workers inside each compile, 0 = auto  (1)

On a 1GB Raspberry Pi, prefer cross-compiling from a workstation and copying
the binaries with scp.
USAGE
}

# -----------------------------------------------------------------------------
# Flags
# -----------------------------------------------------------------------------
TARGET_GOARCH=""
OUT_DIR=""
while [ $# -gt 0 ]; do
    case "$1" in
        --target) TARGET_GOARCH="${2:-}"; shift 2 ;;
        --out)    OUT_DIR="${2:-}";       shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) log_err "unknown argument: $1"; usage; exit 1 ;;
    esac
done
if [ -n "$OUT_DIR" ]; then
    # Resolve to an absolute path NOW. build_one() cd's into each service
    # directory, so a relative --out would be interpreted against
    # services/<name>/ instead of the directory you ran the command from --
    # silently scattering binaries into the source tree.
    case "$OUT_DIR" in
        /*) INSTALL_DIR="$OUT_DIR" ;;
        *)  INSTALL_DIR="$(pwd)/$OUT_DIR" ;;
    esac
fi

echo ""
echo "=============================================="
echo "  Step 10: Build Go services"
echo "=============================================="
echo ""

# -----------------------------------------------------------------------------
# Preflight
# -----------------------------------------------------------------------------
if ! command -v go &> /dev/null; then
    log_err "Go not found. Run 02-go.sh first."
    exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
    arm64|aarch64) NATIVE_GOARCH="arm64" ;;
    x86_64|amd64)  NATIVE_GOARCH="amd64" ;;
    *)             NATIVE_GOARCH="$ARCH" ;;
esac

if [ -n "$TARGET_GOARCH" ]; then
    GOARCH_TARGET="$TARGET_GOARCH"
    CROSS=true
else
    GOARCH_TARGET="$NATIVE_GOARCH"
    CROSS=false
fi

BUILD_SLOTS=$(detect_build_parallelism)
VERSION="${VERSION:-0.1.0-dev}"
GIT_HEAD=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS="-s -w -X main.version=${VERSION}-${GIT_HEAD}"
HEARTBEAT="${WITSABA_HEARTBEAT_SECS:-20}"

mkdir -p "$INSTALL_DIR"

if [ "$CROSS" = true ]; then
    log_info "target     : linux/$GOARCH_TARGET (cross-compiling from $ARCH)"
else
    log_info "target     : linux/$GOARCH_TARGET (this machine: $ARCH, native build)"
fi
log_info "ldflags    : $LDFLAGS"
log_info "output dir : $INSTALL_DIR"
echo ""

if [ "$CROSS" = false ]; then
    log_info "Go environment:"
    describe_go_env
    echo ""
fi

# The Go compiler spawns GOMAXPROCS internal workers inside every compile
# process; they show up in the process args as '-c=N'. So 'go build -p 2' on a
# 4-core box is really up to 8 concurrent compilation workers, not 2. That is
# how two ~400MB compiles end up side by side on a 1GB machine. Cap both.
GOMAXPROCS_CAP="${WITSABA_BUILD_GOMAXPROCS:-1}"
case "$GOMAXPROCS_CAP" in
    ''|*[!0-9]*) GOMAXPROCS_CAP=1 ;;
esac
if [ "$CROSS" = true ] || [ "$GOMAXPROCS_CAP" -eq 0 ]; then
    # A cross-build host is not the memory-constrained machine, so let Go use
    # the full core count there.
    unset GOMAXPROCS
else
    export GOMAXPROCS="$GOMAXPROCS_CAP"
    log_info "GOMAXPROCS : $GOMAXPROCS (workers per compile process; -p is $BUILD_SLOTS)"
    log_warn "a cold build of this dependency tree does not fit in 899MB of RAM"
    log_warn "much faster: build elsewhere and copy the binary"
    log_warn "  $0 --target $GOARCH_TARGET --out ./build"
    echo ""
fi

# -----------------------------------------------------------------------------
# build_one <service-dir> <cmd-pkg> <output-name>
# -----------------------------------------------------------------------------
build_one() {
    local dir="$1" pkg="$2" name="$3"
    local started out
    started=$(date +%s)

    log_info "── $name ─────────────────────────────────────────"
    cd "$REPO_DIR/$dir" || { log_err "no such directory: $dir"; return 1; }

    # Modules first. go build would fetch these anyway, but a separate timed
    # step makes the slow part attributable and fills the build cache before
    # the compiler starts competing for RAM.
    run_progress "go mod download ($name)" "$HEARTBEAT" \
        go mod download

    # -v prints each package as it finishes compiling, so the log is itself the
    # progress indicator. -p bounds concurrent compiler processes;
    # GOMAXPROCS (exported above) bounds the workers inside each of them.
    run_progress "go build -v ($name)" "$HEARTBEAT" \
        env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_TARGET" \
            go build -v -p "$BUILD_SLOTS" \
                -trimpath \
                -ldflags "$LDFLAGS" \
                -o "$INSTALL_DIR/$name" \
                "./$pkg"

    out="$INSTALL_DIR/$name"
    if [ ! -f "$out" ]; then
        log_err "$name: build reported success but produced no binary"
        return 1
    fi

    log_ok "$name built in $(( $(date +%s) - started ))s"
    printf '    %-8s %s\n' "size" "$(du -h "$out" | cut -f1)"
    # Confirm the artefact is really a static linux binary for the intended
    # architecture rather than trusting the flags.
    if command -v file >/dev/null 2>&1; then
        printf '    %-8s %s\n' "type" "$(file -b "$out" | cut -c1-72)"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        printf '    %-8s %s\n' "sha256" "$(sha256sum "$out" | cut -c1-16)..."
    fi
    echo ""
}

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------
build_one "services/messaging-core" "cmd/messaging-core" "messaging-core" \
    || { log_err "messaging-core failed to build"; exit 1; }

build_one "services/workers" "cmd/workers" "workers" \
    || { log_err "workers failed to build"; exit 1; }

# -----------------------------------------------------------------------------
# Smoke test. Only possible for a native build -- a cross-compiled binary
# cannot run on the build host. A build that links but cannot start is not a
# build, so do it whenever it is possible.
# -----------------------------------------------------------------------------
if [ "$CROSS" = true ]; then
    log_info "smoke test skipped: a linux/$GOARCH_TARGET binary cannot run on $ARCH"
else
    log_info "smoke test (each binary must report a config error, not crash)"
    for name in messaging-core workers; do
        smoke=$(cd / && "$INSTALL_DIR/$name" 2>&1 || true)
        if printf '%s' "$smoke" | grep -qiE 'password|required|invalid|usage'; then
            log_ok "  $name loads and validates config"
        else
            log_warn "  $name unexpected output: ${smoke:0:120}"
        fi
    done
    echo ""
fi

# -----------------------------------------------------------------------------
# PATH convenience (native installs only)
# -----------------------------------------------------------------------------
if [ "$CROSS" = false ] && ! grep -q "witsaba/bin" "$HOME/.bashrc" 2>/dev/null; then
    {
        echo ''
        echo '# Witsaba binaries'
        echo 'export PATH="$HOME/.witsaba/bin:$PATH"'
    } >> "$HOME/.bashrc"
    log_info "added ~/.witsaba/bin to PATH in ~/.bashrc"
fi

echo ""
log_ok "Build complete"
log_info "  $INSTALL_DIR/messaging-core"
log_info "  $INSTALL_DIR/workers"
echo ""
if [ "$CROSS" = true ]; then
    log_info "Copy to the target and restart:"
    log_info "  scp $INSTALL_DIR/messaging-core $INSTALL_DIR/workers \\\\"
    log_info "      <user>@<host>:~/.witsaba/bin/"
    log_info "  ssh <user>@<host> 'systemctl --user restart witsaba-messaging-core witsaba-workers'"
else
    log_info "Next: ./13-nginx.sh"
fi
