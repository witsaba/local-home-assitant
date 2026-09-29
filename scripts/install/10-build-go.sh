#!/bin/bash
# =============================================================================
# 10-build-go.sh - Build Go services (messaging-core, workers)
# =============================================================================
# Cross-compiles for ARM64 and installs to ~/.local/bin
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
INSTALL_DIR="$HOME/.witsaba"

# Source shared brew helpers
source "$SCRIPT_DIR/_brew-helpers.sh"

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

log_info "Detected architecture: $ARCH -> GOARCH=$GOARCH"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    log_err "Go is not installed!"
    log_err "Run 02-go.sh first."
    exit 1
fi

# Create installation directory
mkdir -p "$INSTALL_DIR/bin"

# Detect if we're on the Pi or cross-compiling
ON_PI=false
if [ "$(uname -n)" = "home-assistant" ] || grep -q "Raspberry" /proc/cpuinfo 2>/dev/null || [ "$ARCH" = "aarch64" ]; then
    ON_PI=true
fi

log_info "Building for: $(uname -s)/$GOARCH"
log_info "On target: $ON_PI"
echo ""

# Build messaging-core
echo "----------------------------------------------"
log_info "Building messaging-core..."
echo "----------------------------------------------"
cd "$REPO_DIR/services/messaging-core"

# Ensure dependencies are downloaded
go mod download

# Build with version info
GIT_HEAD=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION="${VERSION:-0.1.0-dev}"

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}-${GIT_HEAD}" \
    -o "$INSTALL_DIR/bin/messaging-core" \
    ./cmd/messaging-core

if [ -f "$INSTALL_DIR/bin/messaging-core" ]; then
    log_ok "messaging-core built: $(ls -lh "$INSTALL_DIR/bin/messaging-core" | awk '{print $5}')"
else
    log_err "messaging-core build failed!"
    exit 1
fi

echo ""

# Build workers
echo "----------------------------------------------"
log_info "Building workers..."
echo "----------------------------------------------"
cd "$REPO_DIR/services/workers"

# Ensure dependencies are downloaded
go mod download

# Build with version info
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}-${GIT_HEAD}" \
    -o "$INSTALL_DIR/bin/workers" \
    ./cmd/workers

if [ -f "$INSTALL_DIR/bin/workers" ]; then
    log_ok "workers built: $(ls -lh "$INSTALL_DIR/bin/workers" | awk '{print $5}')"
else
    log_err "workers build failed!"
    exit 1
fi

echo ""
log_ok "All Go services built successfully!"
log_info "Installed to: $INSTALL_DIR/bin/"
log_info ""

# Add to PATH if needed
if ! grep -q "witsaba/bin" "$HOME/.bashrc" 2>/dev/null; then
    log_info "Adding witsaba/bin to PATH..."
    echo '' >> "$HOME/.bashrc"
    echo '# Witsaba binaries' >> "$HOME/.bashrc"
    echo 'export PATH="$HOME/.witsaba/bin:$PATH"' >> "$HOME/.bashrc"
fi

log_info "Next: Run 11-build-frontend.sh to build the web UI"
