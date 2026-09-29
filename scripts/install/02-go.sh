#!/bin/bash
# =============================================================================
# 02-go.sh - Install Go via Homebrew
# =============================================================================
# Go is needed to build the witsaba services (messaging-core, workers).
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
echo "  Step 2: Go Installation"
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

# Check if Go is already installed
if command -v go &> /dev/null; then
    GO_VERSION=$(go version | grep -oP 'go\K[0-9]+\.[0-9]+')
    log_ok "Go is already installed: $(go version)"
    
    if [ "$(echo "$GO_VERSION >= 1.26" | bc)" -eq 1 ]; then
        log_ok "Go version is sufficient (>= 1.26)"
    else
        log_info "Installing Go 1.26+..."
        brew install go --quiet
    fi
else
    log_info "Installing Go 1.26..."
    brew install go --quiet
fi

# Verify installation
if command -v go &> /dev/null; then
    log_ok "Go installed: $(go version)"
    log_info "Go path: $(which go)"
else
    log_err "Go installation failed"
    exit 1
fi

log_ok ""
log_ok "Go installation complete!"
