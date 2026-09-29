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

# Check if brew is installed
if command -v brew &> /dev/null; then
    log_ok "Homebrew is already installed: $(brew --version | head -1)"
    
    # Ensure brew is in PATH for this session
    if ! grep -q 'eval "$(brew --env)"' ~/.bashrc 2>/dev/null; then
        log_info "Adding Homebrew to ~/.bashrc..."
        echo '' >> ~/.bashrc
        echo '# Homebrew' >> ~/.bashrc
        echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew --env)"' >> ~/.bashrc
        echo 'export HOMEBREW_PREFIX="/home/linuxbrew/.linuxbrew"' >> ~/.bashrc
        echo 'export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"' >> ~/.bashrc
    fi
    
    # Update Homebrew
    log_info "Updating Homebrew..."
    brew update --quiet || log_warn "brew update failed, continuing..."
    
    log_ok "Homebrew is ready!"
    exit 0
fi

# Brew not installed - prompt user
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

# Detect installation path
if [ -d "/home/linuxbrew/.linuxbrew" ]; then
    BREW_PREFIX="/home/linuxbrew/.linuxbrew"
elif [ -d "$HOME/.linuxbrew" ]; then
    BREW_PREFIX="$HOME/.linuxbrew"
else
    BREW_PREFIX="$HOME/.brew"
fi

BREW_BIN="$BREW_PREFIX/bin/brew"

if [ ! -f "$BREW_BIN" ]; then
    log_err "Homebrew installation failed.brew not found at $BREW_BIN"
    exit 1
fi

# Add to PATH and bashrc
log_info "Configuring Homebrew in ~/.bashrc..."
cat >> ~/.bashrc << 'BASHRC_EOF'

# Homebrew
eval "$(/home/linuxbrew/.linuxbrew/bin/brew --env 2>/dev/null)" 2>/dev/null || true
export HOMEBREW_PREFIX="/home/linuxbrew/.linuxbrew"
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
BASHRC_EOF

# Source brew environment for this script
eval "$($BREW_BIN --env)"

# Ensure build tools are installed (Linux dependencies)
log_info "Installing Linux build dependencies..."
$BREW_BIN install gcc make 2>/dev/null || true

log_ok "Homebrew installed successfully!"
log_ok "Path: $BREW_PREFIX"
log_ok ""
log_info "IMPORTANT: Run these commands to activate Homebrew in current session:"
echo ""
echo "  source ~/.bashrc"
echo "  eval \"\$($BREW_BIN --env)\""
echo ""
log_info "Then re-run this installation script."

exit 0
