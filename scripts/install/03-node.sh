#!/bin/bash
# =============================================================================
# 03-node.sh - Install Node.js and pnpm via Homebrew
# =============================================================================
# Node.js is needed to build the witsaba web_ui (Qwik frontend).
# pnpm is the package manager used by the project.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
echo "  Step 3: Node.js + pnpm Installation"
echo "=============================================="
echo ""

# Check brew
if ! command -v brew &> /dev/null; then
    log_err "Homebrew is not installed!"
    log_err "Run 00-brew.sh first."
    exit 1
fi

# Source brew environment
eval "$(brew --env)"

# Install Node.js 20 LTS
if command -v node &> /dev/null; then
    log_ok "Node.js is already installed: $(node --version)"
else
    log_info "Installing Node.js 20 LTS..."
    brew install node@20 --quiet
fi

# Install pnpm globally
if command -v pnpm &> /dev/null; then
    log_ok "pnpm is already installed: $(pnpm --version)"
else
    log_info "Installing pnpm..."
    npm install -g pnpm --quiet
fi

# Verify installations
log_ok "Node.js: $(node --version)"
log_ok "npm: $(npm --version)"
log_ok "pnpm: $(pnpm --version)"

log_ok ""
log_ok "Node.js + pnpm installation complete!"
