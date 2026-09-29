#!/bin/bash
# =============================================================================
# uninstall-ubuntu.sh - Uninstall Witsaba from Linux
# =============================================================================
# Removes all witsaba installations and services created by install-ubuntu.sh
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$HOME/.witsaba"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

# Banner
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║     Witsaba Native Linux Uninstaller                   ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""

# Check if witsaba is installed
if [ ! -d "$INSTALL_DIR" ]; then
    log_warn "Witsaba is not installed at $INSTALL_DIR"
    log_info "Nothing to uninstall."
    exit 0
fi

# Confirmation
echo "This will remove:"
echo "  - Witsaba binaries:     $INSTALL_DIR/bin/"
echo "  - Witsaba frontend:    $INSTALL_DIR/frontend/"
echo "  - PostgreSQL data:      $INSTALL_DIR/postgres/"
echo "  - Systemd services:     witsaba-*.service"
echo "  - Witsaba source:      ~/repositories/witsaba"
echo ""
echo "This will NOT remove:"
echo "  - Homebrew (can be kept for other packages)"
echo "  - Go / Node.js (installed via Homebrew)"
echo ""

read -p "Are you sure you want to uninstall? (y/n): " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    log_info "Uninstall cancelled."
    exit 0
fi

echo ""
log_info "Starting uninstallation..."
echo ""

# Step 1: Stop and disable systemd services
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_info "Stopping systemd services..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

SYSTEMD_DIR="$HOME/.config/systemd/user"

if [ -d "$SYSTEMD_DIR" ]; then
    for service in witsaba-postgres witsaba-messaging-core witsaba-workers witsaba-web-ui; do
        if systemctl --user list-unit-files | grep -q "$service"; then
            log_info "Stopping $service..."
            systemctl --user stop "$service" 2>/dev/null || true
            systemctl --user disable "$service" 2>/dev/null || true
            rm -f "$SYSTEMD_DIR/${service}.service"
        fi
    done
    
    # Reload systemd
    systemctl --user daemon-reload
    log_ok "Systemd services removed"
else
    log_info "No systemd services found"
fi

# Step 2: Stop PostgreSQL
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_info "Stopping PostgreSQL..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ -f "$INSTALL_DIR/postgres/stop.sh" ]; then
    "$INSTALL_DIR/postgres/stop.sh" 2>/dev/null || true
fi

# Kill any remaining postgres processes
pkill -f "pg_ctl.*witsaba" 2>/dev/null || true
pkill -f "postgres.*witsaba" 2>/dev/null || true

log_ok "PostgreSQL stopped"

# Step 3: Remove witsaba installation directory
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_info "Removing witsaba files..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ -d "$INSTALL_DIR" ]; then
    log_info "Removing $INSTALL_DIR..."
    rm -rf "$INSTALL_DIR"
    log_ok "Installation directory removed"
fi

# Step 4: Remove witsaba repository (optional)
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_info "Checking for witsaba repository..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ -d "$HOME/repositories/witsaba" ]; then
    echo ""
    log_info "Found: $HOME/repositories/witsaba"
    read -p "Remove witsaba source repository? (y/n): " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        rm -rf "$HOME/repositories/witsaba"
        log_ok "Repository removed"
    else
        log_info "Repository kept"
    fi
fi

# Step 5: Remove PATH additions from bashrc
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log_info "Cleaning ~/.bashrc..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Remove witsaba-related lines from bashrc
if [ -f "$HOME/.bashrc" ]; then
    # Create backup
    cp "$HOME/.bashrc" "$HOME/.bashrc.bak.$(date +%Y%m%d%H%M%S)"
    
    # Remove witsaba entries
    sed -i '/# Witsaba/,/# End Witsaba/d' "$HOME/.bashrc" 2>/dev/null || true
    sed -i '/# Witsaba binaries/d' "$HOME/.bashrc" 2>/dev/null || true
    sed -i '/export PATH=.*\.witsaba\/bin/d' "$HOME/.bashrc" 2>/dev/null || true
    sed -i '/Homebrew/d' "$HOME/.bashrc" 2>/dev/null || true
    sed -i '/eval.*brew.*--env/d' "$HOME/.bashrc" 2>/dev/null || true
    sed -i '/HOMEBREW_PREFIX/d' "$HOME/.bashrc" 2>/dev/null || true
    
    log_ok "~/.bashrc cleaned"
    log_info "Backup saved: ~/.bashrc.bak.*"
fi

# Final summary
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║              Uninstallation Complete!                    ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
log_ok "All witsaba components removed"
echo ""
echo "Note: Homebrew is still installed if you want to keep it."
echo "      To remove Homebrew completely, run:"
echo ""
echo "  /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/uninstall.sh)\""
echo ""
