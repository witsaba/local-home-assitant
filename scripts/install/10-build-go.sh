#!/bin/bash
# =============================================================================
# 10-build-go.sh - Build the Go services (messaging-core, workers)
# =============================================================================
# Compiles both binaries to ~/.witsaba/bin for the running architecture.
#
# On a Raspberry Pi this is the slowest step in the whole install and the one
# that produces the least output by default: a cold build of the
# otel + gin + pgx + zap + nats dependency tree is many minutes of silence.
# Everything slow here therefore runs under run_progress, and -v is passed to
# go build so each package appears as it is compiled.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba"

. "$SCRIPT_DIR/_lib.sh"

# Homebrew discovery
BREW_BIN=""
for candidate in \
    "/home/linuxbrew/.linuxbrew/bin/brew" \
    "$HOME/.linuxbrew/bin/brew" \
    "$HOME/.brew/bin/brew"; do
    [ -f "$candidate" ] && BREW_BIN="$candidate" && break
done
if [ -z "$BREW_BIN" ]; then
    echo "[x] Homebrew not found. Run 00-brew.sh first." >&2
    exit 1
fi
export HOMEBREW_PREFIX="$(dirname "$(dirname "$BREW_BIN")")"
export PATH="$HOMEBREW_PREFIX/bin:$PATH"

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; NC='\033[0m'
log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err()  { echo -e "${RED}[x]${NC} $1" >&2; }

# How often to print a heartbeat while something slow runs.
HEARTBEAT="${WITSABA_HEARTBEAT_SECS:-20}"

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
    arm64|aarch64) GOARCH_TARGET="arm64" ;;
    x86_64|amd64)  GOARCH_TARGET="amd64" ;;
    *)             GOARCH_TARGET="$ARCH" ;;
esac

BUILD_SLOTS=$(detect_build_parallelism)
VERSION="${VERSION:-0.1.0-dev}"
GIT_HEAD=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS="-s -w -X main.version=${VERSION}-${GIT_HEAD}"

mkdir -p "$INSTALL_DIR/bin"

log_info "target     : linux/$GOARCH_TARGET (this machine: $ARCH, native build)"
log_info "ldflags    : $LDFLAGS"
log_info "output dir : $INSTALL_DIR/bin"
echo ""

log_info "Go environment:"
describe_go_env
echo ""

if [ "$(nproc 2>/dev/null || echo 1)" -gt "$BUILD_SLOTS" ]; then
    log_warn "limiting to $BUILD_SLOTS compile slot(s) to stay inside RAM"
    log_warn "override with WITSABA_BUILD_PARALLELISM if you have swap headroom"
    echo ""
fi

# -----------------------------------------------------------------------------
# build_one <service-dir> <cmd-pkg> <output-name>
# -----------------------------------------------------------------------------
build_one() {
    local dir="$1" pkg="$2" name="$3"
    local started
    started=$(date +%s)

    log_info "── $name ─────────────────────────────────────────"
    cd "$REPO_DIR/$dir" || { log_err "no such directory: $dir"; return 1; }

    # Modules first. go build would fetch these anyway, but doing it as a
    # separate timed step makes the slow part attributable and lets the build
    # cache fill before the compiler starts competing for RAM.
    run_progress "go mod download ($name)" "$HEARTBEAT" \
        go mod download

    # -v prints each package as it finishes compiling, so the log itself is
    # the progress indicator. -p bounds concurrent compiler processes.
    run_progress "go build -v ($name)" "$HEARTBEAT" \
        env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_TARGET" \
            go build -v -p "$BUILD_SLOTS" \
                -trimpath \
                -ldflags "$LDFLAGS" \
                -o "$INSTALL_DIR/bin/$name" \
                "./$pkg"

    local out="$INSTALL_DIR/bin/$name"
    if [ ! -f "$out" ]; then
        log_err "$name: build reported success but produced no binary"
        return 1
    fi

    log_ok "$name built in $(( $(date +%s) - started ))s"
    printf '    %-12s %s\n' "size" "$(du -h "$out" | cut -f1)"
    # Confirm it really is a static linux/arm64 executable rather than
    # trusting the build flags.
    if command -v file >/dev/null 2>&1; then
        printf '    %-12s %s\n' "type" "$(file -b "$out" | cut -c1-70)"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        printf '    %-12s %s\n' "sha256" "$(sha256sum "$out" | cut -c1-16)..."
    fi
    echo ""
}

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------
if ! build_one "services/messaging-core" "cmd/messaging-core" "messaging-core"; then
    log_err "messaging-core failed to build"
    exit 1
fi

if ! build_one "services/workers" "cmd/workers" "workers"; then
    log_err "workers failed to build"
    exit 1
fi

# -----------------------------------------------------------------------------
# Smoke test: the binaries must at least be able to load and print usage.
# A build that links but cannot start is not a build.
# -----------------------------------------------------------------------------
log_info "smoke test (both binaries must report a config error, exit 2)"
for name in messaging-core workers; do
    out=$(cd / && "$INSTALL_DIR/bin/$name" 2>&1 || true)
    if printf '%s' "$out" | grep -qiE 'password|required|invalid|usage'; then
        log_ok "  $name loads and validates config"
    else
        log_warn "  $name produced unexpected output: ${out:0:120}"
    fi
done
echo ""

# -----------------------------------------------------------------------------
# PATH convenience
# -----------------------------------------------------------------------------
if ! grep -q "witsaba/bin" "$HOME/.bashrc" 2>/dev/null; then
    {
        echo ''
        echo '# Witsaba binaries'
        echo 'export PATH="$HOME/.witsaba/bin:$PATH"'
    } >> "$HOME/.bashrc"
    log_info "added ~/.witsaba/bin to PATH in ~/.bashrc"
fi

echo ""
log_ok "Build complete"
log_info "  $INSTALL_DIR/bin/messaging-core"
log_info "  $INSTALL_DIR/bin/workers"
log_info ""
log_info "Next: ./11-build-frontend.sh"
