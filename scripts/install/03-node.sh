#!/bin/bash
# =============================================================================
# 03-node.sh - Install Node.js and pnpm via Homebrew
# =============================================================================
# Installs the Node version this project actually needs, and makes sure it is
# the one that gets used.
#
# Why node@22 and not the newest available: @builder.io/qwik-city@1.19.2 pulls
# in undici@8.11.2, which declares engines.node ">=22.19.0" and calls
# webidl.util.markAsUncloneable at import time. vite.config.ts imports the
# qwik-city plugin, so merely loading the config runs that code and Node 20
# dies with:
#
#   TypeError: webidl.util.markAsUncloneable is not a function
#     at new CacheStorage (undici/lib/web/cache/cachestorage.js:20:17)
#   error during build: failed to load config from .../vite.config.ts
#
# Homebrew's unversioned `node` formula is currently 26.x. That would work, but
# it is a rolling target: an untested major is a poor default for a project
# that pins its toolchain. node@22 is the newest line the Dockerfile targets.
# Override with WITSABA_NODE_FORMULA if you want a different one.
#
# Homebrew versioned node formulae are KEG-ONLY, so node@22 is NOT symlinked
# into brew/bin. Every other script resolves the interpreter through
# setup_node_env in _lib.sh rather than trusting `node` on PATH.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
HOMEBREW_PREFIX="$(dirname "$(dirname "$BREW_BIN")")"
export HOMEBREW_PREFIX
export PATH="$HOMEBREW_PREFIX/bin:$PATH"

echo ""
echo "=============================================="
echo "  Step 3: Node.js + pnpm"
echo "=============================================="
echo ""

# -----------------------------------------------------------------------------
# Install the Node version the project needs
# -----------------------------------------------------------------------------
FORMULA="${WITSABA_NODE_FORMULA:-node@22}"

if [ -x "$HOMEBREW_PREFIX/opt/$FORMULA/bin/node" ]; then
    log_ok "$FORMULA already installed at $HOMEBREW_PREFIX/opt/$FORMULA"
else
    log_info "Installing $FORMULA (required: node >= $WITSABA_NODE_MIN_VERSION)..."
    "$BREW_BIN" install "$FORMULA"
fi

setup_node_env

if ! node_version_ok; then
    log_err "$("$NODE_BIN" --version 2>/dev/null || echo unknown) is below the required $WITSABA_NODE_MIN_VERSION"
    log_err "the qwik-city -> undici chain calls webidl.util.markAsUncloneable at import"
    exit 1
fi

log_ok "node $("$NODE_BIN" --version) -> $NODE_BIN"

# -----------------------------------------------------------------------------
# pnpm, installed under the same interpreter
# -----------------------------------------------------------------------------
# Running `npm install -g pnpm` with whatever npm happens to be first on PATH
# can bind pnpm's shim to the wrong node. Put the intended node first, which
# setup_node_env has already done, and install through that npm.
NPM_BIN="$(dirname "$NODE_BIN")/npm"
[ -x "$NPM_BIN" ] || NPM_BIN="$(command -v npm 2>/dev/null || echo npm)"

if [ -x "$(dirname "$NODE_BIN")/pnpm" ]; then
    log_ok "pnpm already present alongside node"
else
    log_info "Installing pnpm via $("$NPM_BIN" --version)"
    "$NPM_BIN" install -g pnpm
fi

setup_node_env
PNPM_BIN="$(pnpm_bin)"

# -----------------------------------------------------------------------------
# Verify the combination that actually failed
# -----------------------------------------------------------------------------
log_info "verifying toolchain:"
log_info "  node : $("$NODE_BIN" --version)  ($NODE_BIN)"
log_info "  pnpm : $("$PNPM_BIN" --version 2>/dev/null || echo 'not found')  ($PNPM_BIN)"

if ! "$NODE_BIN" -e 'process.exit(0)' 2>/dev/null; then
    log_err "node at $NODE_BIN is not runnable"
    exit 1
fi

# -----------------------------------------------------------------------------
# Record the chosen paths so 11 and 12 do not have to rediscover them
# -----------------------------------------------------------------------------
ENV_FILE="$HOME/.witsaba/witsaba.env"
if [ -f "$ENV_FILE" ]; then
    # Preserve everything already written, then pin the toolchain paths.
    if grep -q '^NODE_BIN=' "$ENV_FILE"; then
        tmp=$(mktemp)
        grep -v '^NODE_BIN=\|^PNPM_BIN=\|^NODE_MAJOR=' "$ENV_FILE" > "$tmp"
        mv "$tmp" "$ENV_FILE"
    fi
    {
        echo ""
        echo "# --- Node toolchain (keg-only node@22; do not rely on PATH) ---"
        echo "NODE_BIN=$NODE_BIN"
        echo "PNPM_BIN=$PNPM_BIN"
        echo "NODE_MAJOR=$("$NODE_BIN" --version | sed 's/^v//;s/\..*//')"
    } >> "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    log_ok "recorded toolchain paths in $ENV_FILE"
fi

echo ""
log_ok "Node.js + pnpm ready"
log_info "Next: ./04-postgres-init.sh, then ./11-build-frontend.sh"
