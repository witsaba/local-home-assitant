#!/bin/bash
# =============================================================================
# main.sh - Witsaba Native Linux Installation Orchestrator
# =============================================================================
# Runs all installation steps in order.
# Each step is a separate script that can be run independently.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
log_step() { echo -e "${BOLD}${BLUE}[STEP]${NC} $1${NC}"; }

# Banner
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║     Witsaba Native Linux Installation                    ║"
echo "║     Optimized for Raspberry Pi 1GB RAM                    ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""

# Check for --skip-build flag
SKIP_BUILD=false
if [[ "$1" == "--skip-build" ]]; then
    SKIP_BUILD=true
    log_warn "Skipping build steps (--skip-build specified)"
fi

# Run installation steps
STEPS=(
    "00-brew.sh:Homebrew (package manager)"
    "01-postgresql.sh:PostgreSQL 16"
    "02-go.sh:Go compiler"
    "03-node.sh:Node.js + pnpm"
    "04-postgres-init.sh:Database initialization"
)

BUILD_STEPS=(
    "10-build-go.sh:Build Go services"
    "11-build-frontend.sh:Build web UI"
    "12-systemd-services.sh:Create systemd services"
)

# Run installation steps
for step in "${STEPS[@]}"; do
    script=$(echo "$step" | cut -d: -f1)
    name=$(echo "$step" | cut -d: -f2)
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    log_step "Running: $name"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    bash "$SCRIPT_DIR/$script"
    
    if [ $? -ne 0 ]; then
        log_err "Step failed: $name"
        log_err "Script: $SCRIPT_DIR/$script"
        exit 1
    fi
done

# Build steps (optional)
if [ "$SKIP_BUILD" = false ]; then
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "                    BUILD PHASE"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    log_info "Building witsaba services from source..."
    log_info "Make sure you have cloned the repository:"
    echo ""
    echo "  cd ~/repositories"
    echo "  git clone <your-repo-url> witsaba"
    echo ""
    
    for step in "${BUILD_STEPS[@]}"; do
        script=$(echo "$step" | cut -d: -f1)
        name=$(echo "$step" | cut -d: -f2)
        
        echo ""
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        log_step "Running: $name"
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        
        bash "$SCRIPT_DIR/$script"
        
        if [ $? -ne 0 ]; then
            log_err "Step failed: $name"
            exit 1
        fi
    done
else
    echo ""
    log_info "Skipping build steps"
fi

# Final summary
echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║              Installation Complete!                       ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""
echo "Summary:"
echo "  Homebrew:     Installed"
echo "  PostgreSQL:   Configured"
echo "  Go:           Installed"
echo "  Node.js:      Installed"
echo "  Database:     Initialized"
if [ "$SKIP_BUILD" = false ]; then
    echo "  Go services:  Built"
    echo "  Web UI:       Built"
    echo "  Services:     Created"
fi
echo ""
echo "Next steps:"
echo "  1. Enable lingering (if not done):"
echo "     sudo loginctl enable-linger \$USER"
echo ""
echo "  2. Start services:"
echo "     systemctl --user start witsaba-postgres"
echo "     systemctl --user start witsaba-messaging-core"
echo "     systemctl --user start witsaba-workers"
echo "     systemctl --user start witsaba-web-ui"
echo ""
echo "  3. Check status:"
echo "     systemctl --user status witsaba-messaging-core"
echo "     curl http://localhost:8081/api/devices/active"
echo ""
echo "  4. Access web UI:"
echo "     http://192.168.1.115:4173"
echo ""
