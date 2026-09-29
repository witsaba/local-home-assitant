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
echo "  Step 11: Build Web UI"
echo "=============================================="
echo ""

# Check pnpm
if ! command -v pnpm &> /dev/null; then
    log_err "pnpm not found. Run 03-node.sh first."
    exit 1
fi

cd "$REPO_DIR/frontend/web_ui"

log_info "Node.js: $(node --version)"
log_info "pnpm: $(pnpm --version)"

# Memory check
AVAILABLE_MEM=$(free -m 2>/dev/null | awk '/Mem:/ {print $7}' || echo "0")
log_info "Available memory: ${AVAILABLE_MEM}MB"

# Build
mkdir -p "$INSTALL_DIR/frontend"

log_info "Installing dependencies..."
pnpm install --frozen-lockfile 2>/dev/null || pnpm install

log_info "Building frontend..."
export NODE_OPTIONS="--max-old-space-size=256"
pnpm run build

log_info "Installing to $INSTALL_DIR/frontend..."
rm -rf "$INSTALL_DIR/frontend"
cp -r "$REPO_DIR/frontend/web_ui/dist" "$INSTALL_DIR/frontend/"
cp -r "$REPO_DIR/frontend/web_ui/public" "$INSTALL_DIR/frontend/" 2>/dev/null || true

log_ok "Frontend built and installed!"
