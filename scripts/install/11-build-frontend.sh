#!/bin/bash
# =============================================================================
# 11-build-frontend.sh - Build web_ui (Qwik frontend)
# =============================================================================
# Uses pnpm to build the production version of the frontend.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba"

. "$SCRIPT_DIR/_lib.sh"

# =============================================================================
# Find and set up Homebrew
# =============================================================================
BREW_FOUND=false
BREW_PATHS=(
    "/home/linuxbrew/.linuxbrew/bin/brew"
    "$HOME/.linuxbrew/bin/brew"
    "$HOME/.brew/bin/brew"
)

for brew_path in "${BREW_PATHS[@]}"; do
    if [ -f "$brew_path" ]; then
        export HOMEBREW_PREFIX="$(dirname "$(dirname "$brew_path")")"
        export PATH="$(dirname "$brew_path"):$PATH"
        BREW_FOUND=true
        break
    fi
done

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

echo ""
echo "=============================================="
echo "  Step 11: Build web UI"
echo "=============================================="
echo ""

# How often to print a heartbeat while something slow runs.
HEARTBEAT="${WITSABA_HEARTBEAT_SECS:-20}"

FRONTEND_DIR="$REPO_DIR/frontend/web_ui"
cd "$FRONTEND_DIR" || { log_err "no such directory: $FRONTEND_DIR"; exit 1; }

# -----------------------------------------------------------------------------
# Node preflight
#
# Run before pnpm install: a wrong Node version produces a confusing failure
# deep inside Vite's config loader, minutes into the step, after a 7 minute
# dependency download. Fail immediately and say why instead.
# -----------------------------------------------------------------------------
if ! preflight_node; then
    exit 1
fi

# -----------------------------------------------------------------------------
# V8 heap ceiling. A Qwik/Vite build is the most allocation-hungry thing in
# the install, and on a 1GB box the default "grow until the machine is out"
# behaviour gets the process OOM-killed instead of completing.
# -----------------------------------------------------------------------------
export NODE_OPTIONS="--max-old-space-size=${WITSABA_NODE_HEAP_MB:-512}"

log_info "node        : $("$NODE_BIN" --version)  ($NODE_BIN)"
log_info "pnpm        : $("$PNPM_BIN" --version 2>/dev/null || echo '?')  ($PNPM_BIN)"
log_info "NODE_OPTIONS: $NODE_OPTIONS"
log_info "available   : $(available_mem_mb || echo '?') MB RAM"
log_info "workdir     : $FRONTEND_DIR"
echo ""

# -----------------------------------------------------------------------------
# Install dependencies
#
# pnpm install on a Pi is minutes of near-silence, and --frozen-lockfile makes
# it reproducible. The lockfile in the repo already pins every transitive
# version, so a drifting install is a bug, not a convenience.
# -----------------------------------------------------------------------------
log_info "── dependencies ─────────────────────────────"
run_progress "pnpm install" "$HEARTBEAT" \
    "$PNPM_BIN" install --frozen-lockfile

# -----------------------------------------------------------------------------
# Second, decisive check: require the exact module that broke the build.
# This catches any future drift where the Node floor moves again, and it
# cannot be fooled by a stale version string.
# -----------------------------------------------------------------------------
if ! "$NODE_BIN" -e 'require("undici")' >/dev/null 2>&1; then
    log_err "this dependency tree cannot be loaded by $("$NODE_BIN" --version)"
    log_err ""
    log_err "require('undici') fails. undici is pulled in by @builder.io/qwik-city"
    log_err "and calls webidl.util.markAsUncloneable, which needs node >= $WITSABA_NODE_MIN_VERSION."
    "$NODE_BIN" -e 'require("undici")' 2>&1 | head -6 | while read -r line; do
        log_err "  $line"
    done
    exit 1
fi
log_ok "undici loads under $("$NODE_BIN" --version)"

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------
log_info "── production build ──────────────────────────"
run_progress "pnpm run build" "$HEARTBEAT" \
    "$PNPM_BIN" run build

if [ ! -d "$REPO_DIR/frontend/web_ui/dist" ]; then
    log_err "build reported success but frontend/web_ui/dist does not exist"
    exit 1
fi

# -----------------------------------------------------------------------------
# Install artefacts
# -----------------------------------------------------------------------------
log_info "installing artefacts to $INSTALL_DIR/frontend"
rm -rf "$INSTALL_DIR/frontend"
mkdir -p "$INSTALL_DIR/frontend"
cp -r "$REPO_DIR/frontend/web_ui/dist" "$INSTALL_DIR/frontend/"
cp -r "$REPO_DIR/frontend/web_ui/public" "$INSTALL_DIR/frontend/" 2>/dev/null || true
cp "$REPO_DIR/frontend/web_ui/package.json" "$INSTALL_DIR/frontend/" 2>/dev/null || true

# vite preview is a dev-server process: it reads vite.config.ts and expects the
# project layout, not just the dist output. Without the config and a
# node_modules with vite in it, `pnpm preview` cannot start at all. Copy the
# minimum needed for the preview server to boot.
log_info "copying preview-server prerequisites (vite config + node_modules)"
cp "$REPO_DIR/frontend/web_ui/vite.config.ts" "$INSTALL_DIR/frontend/" 2>/dev/null || true
cp -r "$REPO_DIR/frontend/web_ui/node_modules" "$INSTALL_DIR/frontend/" 2>/dev/null || true

if [ ! -d "$INSTALL_DIR/frontend/node_modules" ]; then
    log_err "node_modules was not copied; vite preview cannot start without it"
    log_err "this is expected if the build ran in a container without node_modules"
    exit 1
fi

printf '    %-12s %s\n' "dist size" "$(du -sh "$INSTALL_DIR/frontend/dist" 2>/dev/null | cut -f1)"
printf '    %-12s %s\n' "total" "$(du -sh "$INSTALL_DIR/frontend" 2>/dev/null | cut -f1)"
echo ""

log_ok "Web UI built and installed"
log_info "  $INSTALL_DIR/frontend"
log_info ""
log_info "Next: ./12-systemd-services.sh"

