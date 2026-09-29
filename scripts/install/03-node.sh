#!/bin/bash
# =============================================================================
# 03-node.sh - Install Node.js and pnpm via Homebrew
# =============================================================================
# Node.js is needed to build the witsaba web_ui (Qwik frontend).
# pnpm is the package manager used by the project.
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
echo "  Step 3: Node.js + pnpm Installation"
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
