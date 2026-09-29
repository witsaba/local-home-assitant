#!/bin/bash
# =============================================================================
# 02-go.sh - Install Go via Homebrew
# =============================================================================
# Go is needed to build the witsaba services (messaging-core, workers).
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
echo "  Step 2: Go Installation"
echo "=============================================="
echo ""

# Check if Go is already installed
if command -v go &> /dev/null; then
    log_ok "Go is already installed: $(go version)"
else
    log_info "Installing Go..."
    brew install go --quiet
fi

log_ok "Go installed: $(go version)"
