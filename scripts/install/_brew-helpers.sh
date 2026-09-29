#!/bin/bash
# =============================================================================
# _brew-helpers.sh - Shared Homebrew setup functions
# =============================================================================
# Source this file in other scripts to ensure Homebrew is in PATH.
# Handles zsh -> bash -> brew chain correctly.
# =============================================================================

# Colors
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export BLUE='\033[0;34m'
export YELLOW='\033[1;33m'
export NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

# Setup brew PATH - call this at the start of each script
setup_brew_path() {
    # If brew is already in PATH, we're done
    if command -v brew &> /dev/null; then
        eval "$(brew --env 2>/dev/null)" 2>/dev/null || true
        return 0
    fi
    
    # Try common Homebrew locations (handles zsh -> bash -> brew)
    local BREW_PATHS=(
        "/home/linuxbrew/.linuxbrew/bin/brew"
        "$HOME/.linuxbrew/bin/brew"
        "$HOME/.brew/bin/brew"
        "/usr/local/bin/brew"
    )
    
    for brew_path in "${BREW_PATHS[@]}"; do
        if [ -f "$brew_path" ]; then
            export HOMEBREW_PREFIX="$(dirname "$(dirname "$brew_path")")"
            export PATH="$(dirname "$brew_path"):$PATH"
            eval "$($brew_path --env 2>/dev/null)" 2>/dev/null || true
            return 0
        fi
    done
    
    return 1
}
