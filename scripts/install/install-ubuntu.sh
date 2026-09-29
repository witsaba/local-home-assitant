#!/bin/bash
# =============================================================================
# main.sh - Witsaba Native Linux Installation Orchestrator
# =============================================================================
# Runs all installation steps in order.
# Each step is a separate script that can be run independently.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Shared helpers: available_mem_mb, gen_secret, run_progress, ...
if [ -f "$SCRIPT_DIR/_lib.sh" ]; then
    . "$SCRIPT_DIR/_lib.sh"
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }
log_step() { echo -e "${BOLD}${BLUE}[STEP]${NC} $1${NC}"; }

# Banner
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║     Witsaba Native Linux Installation                    ║"
echo "║     Optimized for Raspberry Pi 1GB RAM                    ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""

# Check for --skip-build flag
SKIP_BUILD=false
if [[ "$1" == "--skip-build" ]]; then
    SKIP_BUILD=true
    log_warn "Skipping build steps (--skip-build specified)"
fi

# Run installation steps
STEPS=(
    "00-brew.sh:Homebrew (package manager)"
    "01-postgresql.sh:PostgreSQL 16"
    "02-go.sh:Go compiler"
    "03-node.sh:Node.js + pnpm"
    "04-postgres-init.sh:Database initialization"
)

BUILD_STEPS=(
    "10-build-go.sh:Build Go services"
    "13-nginx.sh:Serve the static UI with nginx"
    "12-systemd-services.sh:Create systemd services"
)

# The Qwik/Vite build is not part of the default path any more. It still works
# and is still useful for framework work on a workstation, but on the Pi it
# produced 143MB of runtime for a page that is 40KB on disk, and it is not
# what nginx serves. Opt in with WITSABA_WITH_QWIK=1.
if [ "${WITSABA_WITH_QWIK:-0}" = "1" ]; then
    BUILD_STEPS=(
        "10-build-go.sh:Build Go services"
        "11-build-frontend.sh:Build web UI (Qwik)"
        "13-nginx.sh:Serve the static UI with nginx"
        "12-systemd-services.sh:Create systemd services"
    )
fi

# Run installation steps
for step in "${STEPS[@]}"; do
    script=$(echo "$step" | cut -d: -f1)
    name=$(echo "$step" | cut -d: -f2)
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    log_step "Running: $name"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    bash "$SCRIPT_DIR/$script"
    
    if [ $? -ne 0 ]; then
        log_err "Step failed: $name"
        log_err "Script: $SCRIPT_DIR/$script"
        exit 1
    fi
done

# Build steps (optional)
if [ "$SKIP_BUILD" = false ]; then
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "                    BUILD PHASE"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""

    # A cold build of the Go dependency tree does not fit in 1GB of RAM. The
    # two heavy packages (ugorji/go/codec ~446MB RSS and
    # nats-io/nats-server/v2/server ~185MB) push the build into SD-card swap,
    # where it spends most of its wall clock in iowait rather than on the CPU.
    #
    # Rather than let the operator discover that after 20 minutes, detect a
    # low-memory host and tell them what to do instead.
    MEM_MB=$(available_mem_mb || echo 0)
    if [ "${MEM_MB:-0}" -lt 1400 ] 2>/dev/null; then
        log_warn "only ${MEM_MB} MB RAM available"
        log_warn "building the Go services here will thrash on SD-card swap"
        log_warn ""
        log_warn "Recommended: cross-compile on a workstation, then copy the binaries."
        log_warn ""
        log_warn "  # on the workstation"
        log_warn "  ./scripts/install/10-build-go.sh --target arm64 --out ./build"
        log_warn ""
        log_warn "  # copy them over"
        log_warn "  scp ./build/messaging-core ./build/workers \\"
        log_warn "      $(id -un)@192.168.1.115:~/.witsaba/bin/"
        log_warn ""
        log_warn "Set WITSABA_SKIP_GO_BUILD=1 to build the frontend only and come"
        log_warn "back to this step after the binaries are in place."
        echo ""
        SKIP_GO_BUILD=true
    else
        SKIP_GO_BUILD=false
    fi
    if [ "${WITSABA_SKIP_GO_BUILD:-0}" = "1" ]; then
        SKIP_GO_BUILD=true
    fi

    for step in "${BUILD_STEPS[@]}"; do
        script=$(echo "$step" | cut -d: -f1)
        name=$(echo "$step" | cut -d: -f2)

        if [ "$SKIP_GO_BUILD" = true ] && [ "$script" = "10-build-go.sh" ]; then
            echo ""
            echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            log_step "Skipped: $name (low memory, cross-compile instead)"
            echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            continue
        fi

        echo ""
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        log_step "Running: $name"
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

        bash "$SCRIPT_DIR/$script"

        if [ $? -ne 0 ]; then
            log_err "Step failed: $name"
            exit 1
        fi
    done
else
    echo ""
    log_info "Skipping build steps"
fi

# Final summary
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║              Installation Complete!                       ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "Summary:"
echo "  Homebrew:     Installed"
echo "  PostgreSQL:   Configured"
echo "  Go:           Installed"
echo "  Node.js:      Installed"
echo "  Database:     Initialized"
if [ "$SKIP_BUILD" = false ]; then
    echo "  Go services:  Built"
    echo "  Web UI:       served by nginx"
    echo "  Services:     Created"
fi
echo ""
echo "Next steps:"
echo "  1. Enable lingering (if not done):"
echo "     sudo loginctl enable-linger \$USER"
echo ""
echo "  2. Start services:"
echo "     systemctl --user start witsaba-postgres witsaba-postgres-ready \\"
echo "         witsaba-messaging-core witsaba-workers witsaba-nginx"
echo ""
echo "  3. Check status:"
echo "     systemctl --user status witsaba-nginx"
echo "     curl http://localhost:4173/api/devices/active"
echo ""
echo "  4. Access web UI:"
LAN_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo "     http://${LAN_IP:-<this-host>}:4173/"
echo "     or http://localhost:4173/ from the Pi itself"
echo ""
