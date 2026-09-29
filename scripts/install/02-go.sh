#!/bin/bash
# =============================================================================
# 02-go.sh - Install Go via Homebrew
# =============================================================================
# Go is needed to build the witsaba services (messaging-core, workers).
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source shared brew helpers
source "$SCRIPT_DIR/_brew-helpers.sh"

# Setup brew PATH
if ! setup_brew_path; then
    log_err "Homebrew is not installed!"
    log_err "Run 00-brew.sh first."
    exit 1
fi

echo ""
echo "=============================================="
echo "  Step 2: Go Installation"
echo "=============================================="
echo ""

# Setup brew PATH (handles zsh -> bash -> brew chain)
if ! command -v brew &> /dev/null; then
    # Try common Homebrew locations
    BREW_PATHS=(
        "/home/linuxbrew/.linuxbrew/bin/brew"
        "$HOME/.linuxbrew/bin/brew"
        "$HOME/.brew/bin/brew"
    )
    for brew_path in "${BREW_PATHS[@]}"; do
        if [ -f "$brew_path" ]; then
            export PATH="$(dirname "$brew_path"):$PATH"
            break
        fi
    done
fi

# Source brew environment
if command -v brew &> /dev/null; then
    eval "$(brew --env 2>/dev/null)" 2>/dev/null || true
else
    log_err "Homebrew is not installed!"
    log_err "Run 00-brew.sh first."
    exit 1
fi

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
