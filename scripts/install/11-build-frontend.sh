#!/bin/bash
# =============================================================================
# 11-build-frontend.sh - Build web_ui (Qwik frontend)
# =============================================================================
# Uses pnpm to build the production version of the frontend.
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err() { echo -e "${RED}[✗]${NC} $1" >&2; }

echo ""
echo "=============================================="
echo "  Step 11: Build Web UI"
echo "=============================================="
echo ""

# Check if pnpm is installed
if ! command -v pnpm &> /dev/null; then
    log_err "pnpm is not installed!"
    log_err "Run 03-node.sh first."
    exit 1
fi

cd "$REPO_DIR/frontend/web_ui"

# Check Node.js version
NODE_VERSION=$(node --version | sed 's/v//')
log_info "Node.js version: $NODE_VERSION"
log_info "pnpm version: $(pnpm --version)"

# Check memory and warn if low
AVAILABLE_MEM=$(free -m 2>/dev/null | awk '/Mem:/ {print $7}' || echo "0")
log_info "Available memory: ${AVAILABLE_MEM}MB"

if [ "$AVAILABLE_MEM" -lt 400 ]; then
    log_warn "Low memory detected (< 400MB). Consider increasing swap."
    log_info "Setting NODE_OPTIONS for lower memory usage..."
    export NODE_OPTIONS="--max-old-space-size=256"
fi

# Create installation directory
mkdir -p "$INSTALL_DIR/frontend"

# Install dependencies
echo ""
log_info "Installing dependencies..."
pnpm install

# Build production
echo ""
log_info "Building production frontend..."
pnpm run build

# Copy to installation directory
log_info "Installing to $INSTALL_DIR/frontend..."
rm -rf "$INSTALL_DIR/frontend"
cp -r "$REPO_DIR/frontend/web_ui/dist" "$INSTALL_DIR/frontend/"
cp -r "$REPO_DIR/frontend/web_ui/public" "$INSTALL_DIR/frontend/" 2>/dev/null || true

# Create .env file for frontend
cat > "$INSTALL_DIR/frontend/.env" << 'ENV_EOF'
# Witsaba Web UI Configuration
HOST=0.0.0.0
PORT=4173
NODE_ENV=production
# Limit memory for 1GB RAM devices
NODE_OPTIONS=--max-old-space-size=256
ENV_EOF

echo ""
log_ok "Web UI built successfully!"
log_info "Installed to: $INSTALL_DIR/frontend/"
log_info ""
log_info "To run: pnpm --dir $INSTALL_DIR/frontend preview --host 0.0.0.0"
log_info "Next: Run 12-systemd-services.sh to create systemd user services"
