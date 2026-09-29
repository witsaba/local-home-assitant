#!/bin/bash
# =============================================================================
# 10-build-go.sh - Build Go services (messaging-core, workers)
# =============================================================================
# Cross-compiles for ARM64 and installs to ~/.witsaba/bin
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba"

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
echo "  Step 10: Build Go Services"
echo "=============================================="
echo ""

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    arm64|aarch64) GOARCH="arm64" ;;
    x86_64) GOARCH="amd64" ;;
    *) GOARCH="arm64" ;;
esac

log_info "Architecture: $ARCH -> GOARCH=$GOARCH"

# Check Go
if ! command -v go &> /dev/null; then
    log_err "Go not found. Run 02-go.sh first."
    exit 1
fi

mkdir -p "$INSTALL_DIR/bin"

# Build messaging-core
echo ""
log_info "Building messaging-core..."
cd "$REPO_DIR/services/messaging-core"
go mod download
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
    -trimpath -ldflags "-s -w" \
    -o "$INSTALL_DIR/bin/messaging-core" \
    ./cmd/messaging-core

if [ -f "$INSTALL_DIR/bin/messaging-core" ]; then
    log_ok "messaging-core built: $(ls -lh "$INSTALL_DIR/bin/messaging-core" | awk '{print $5}')"
fi

# Build workers
echo ""
log_info "Building workers..."
cd "$REPO_DIR/services/workers"
go mod download
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
    -trimpath -ldflags "-s -w" \
    -o "$INSTALL_DIR/bin/workers" \
    ./cmd/workers

if [ -f "$INSTALL_DIR/bin/workers" ]; then
    log_ok "workers built: $(ls -lh "$INSTALL_DIR/bin/workers" | awk '{print $5}')"
fi

echo ""
log_ok "All Go services built to: $INSTALL_DIR/bin/"
