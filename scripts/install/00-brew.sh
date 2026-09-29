#!/bin/bash
# =============================================================================
# 00-brew.sh - Check and install Homebrew
# =============================================================================
# Homebrew enables user-space package installation without root privileges.
# This script either verifies brew is installed or prompts the user to install.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$SCRIPT_DIR"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

echo ""
echo "=============================================="
echo "  Step 0: Homebrew Check"
echo "=============================================="
echo ""

# =============================================================================
# STEP 1: Find Homebrew in common locations
# =============================================================================
BREW_FOUND=false
BREW_BIN=""
BREW_PREFIX=""

# Common Homebrew installation paths
BREW_PATHS=(
    "/home/linuxbrew/.linuxbrew/bin/brew"
    "$HOME/.linuxbrew/bin/brew"
    "$HOME/.brew/bin/brew"
    "/usr/local/bin/brew"
)

for path in "${BREW_PATHS[@]}"; do
    if [ -f "$path" ]; then
        BREW_FOUND=true
        BREW_BIN="$path"
        BREW_PREFIX="$(dirname "$(dirname "$path")")"
        break
    fi
done

# If found, set up PATH for this session
if [ "$BREW_FOUND" = true ]; then
    export HOMEBREW_PREFIX="$BREW_PREFIX"
    export PATH="$BREW_PREFIX/bin:$PATH"
    
    # Source brew environment
    eval "$($BREW_BIN --env 2>/dev/null)" 2>/dev/null || true
    
    log_ok "Homebrew found at: $BREW_BIN"
    
    # Update Homebrew
    log_info "Updating Homebrew..."
    if ! $BREW_BIN update --quiet 2>/dev/null; then
        log_warn "brew update failed, continuing anyway..."
    fi
    
    log_ok "Homebrew version: $($BREW_BIN --version | head -1)"
    
    # Ensure brew is in ~/.bashrc for future sessions
    if ! grep -q 'HOMEBREW_PREFIX' "$HOME/.bashrc" 2>/dev/null; then
        log_info "Adding Homebrew to ~/.bashrc..."
        cat >> "$HOME/.bashrc" << 'BASHRC_EOF'

# Homebrew
export HOMEBREW_PREFIX="/home/linuxbrew/.linuxbrew"
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
BASHRC_EOF
    fi
    
    log_ok "Homebrew is ready!"
    exit 0
fi

# =============================================================================
# STEP 2: Homebrew not found - prompt for installation
# =============================================================================
echo ""
log_warn "Homebrew is NOT installed."
echo ""
echo "Homebrew is required to install packages without root privileges."
echo "It will be installed in: /home/linuxbrew/.linuxbrew"
echo ""
echo "Installation command:"
echo "  /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
echo ""
read -p "Do you want to install Homebrew now? (y/n): " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    log_err "Homebrew installation required to continue."
    log_err "Please install Homebrew and run this script again."
    exit 1
fi

log_info "Installing Homebrew..."
echo ""

# Run Homebrew installer
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Verify installation
if [ -f "/home/linuxbrew/.linuxbrew/bin/brew" ]; then
    BREW_BIN="/home/linuxbrew/.linuxbrew/bin/brew"
    BREW_PREFIX="/home/linuxbrew/.linuxbrew"
else
    log_err "Homebrew installation failed."
    exit 1
fi

# Set PATH for this session
export HOMEBREW_PREFIX="$BREW_PREFIX"
export PATH="$BREW_PREFIX/bin:$PATH"
eval "$($BREW_BIN --env 2>/dev/null)" 2>/dev/null || true

# Add to bashrc
log_info "Configuring Homebrew in ~/.bashrc..."
cat >> "$HOME/.bashrc" << 'BASHRC_EOF'

# Homebrew
export HOMEBREW_PREFIX="/home/linuxbrew/.linuxbrew"
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
BASHRC_EOF

log_ok "Homebrew installed successfully!"
log_ok "Path: $BREW_PREFIX"
log_ok ""
log_info "IMPORTANT: For new shells, run: source ~/.bashrc"
log_info "Then continue with: ./01-postgresql.sh"
