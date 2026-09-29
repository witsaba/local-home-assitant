#!/bin/bash
# =============================================================================
# 03-node.sh - Install Node.js and pnpm via Homebrew
# =============================================================================
# Node.js is needed to build the witsaba web_ui (Qwik frontend).
# pnpm is the package manager used by the project.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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

if [ "$BREW_FOUND" != true ]; then
    echo "[✗] Homebrew not found. Run 00-brew.sh first."
    exit 1
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }

echo ""
echo "=============================================="
echo "  Step 3: Node.js + pnpm Installation"
echo "=============================================="
echo ""

# Install Node.js 20
if command -v node &> /dev/null; then
    log_ok "Node.js already installed: $(node --version)"
else
    log_info "Installing Node.js 20..."
    brew install node@20 --quiet
fi

# Install pnpm
if command -v pnpm &> /dev/null; then
    log_ok "pnpm already installed: $(pnpm --version)"
else
    log_info "Installing pnpm..."
    npm install -g pnpm --quiet
fi

log_ok "Node.js: $(node --version)"
log_ok "pnpm: $(pnpm --version)"
