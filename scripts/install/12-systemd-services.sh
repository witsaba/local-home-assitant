#!/bin/bash
# =============================================================================
# 12-systemd-services.sh - Create systemd user services
# =============================================================================
# Creates systemd user units for auto-start on boot (with lingering enabled).
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
echo "  Step 12: Create systemd User Services"
echo "=============================================="
echo ""

# Check lingering
if ! loginctl show-session "$(loginctl | grep "$USER" | awk '{print $1}')" 2>/dev/null | grep -q "Linger=yes"; then
    log_err "Linger not enabled. Run: sudo loginctl enable-linger \$USER"
    exit 1
fi

# Load env
if [ -f "$INSTALL_DIR/.env" ]; then
    source "$INSTALL_DIR/.env" 2>/dev/null || true
fi

PG_PASSWORD="${PG_PASSWORD:-changeme-worker}"
PG_MESSAGING_PASSWORD="${PG_MESSAGING_CORE_PASSWORD:-changeme-messaging}"

# Detect brew paths
BREW_PREFIX="${HOMEBREW_PREFIX:-/home/linuxbrew/.linuxbrew}"
PG_CTL="$BREW_PREFIX/bin/pg_ctl"
PNPM="$BREW_PREFIX/bin/pnpm"

# Create systemd user directory
SYSTEMD_DIR="$HOME/.config/systemd/user"
mkdir -p "$SYSTEMD_DIR"

# Create services
log_info "Creating systemd services..."

cat > "$SYSTEMD_DIR/witsaba-postgres.service" << SERVICE_EOF
[Unit]
Description=Witsaba PostgreSQL
After=network.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=${PG_CTL} -D ${HOME}/.witsaba/postgres/data -l ${HOME}/.witsaba/postgres/logs/postgresql.log start
ExecStop=${PG_CTL} -D ${HOME}/.witsaba/postgres/data stop -m fast
Restart=on-failure

[Install]
WantedBy=default.target
SERVICE_EOF

cat > "$SYSTEMD_DIR/witsaba-messaging-core.service" << SERVICE_EOF
[Unit]
Description=Witsaba Messaging Core
After=network.target witsaba-postgres.service

[Service]
Type=simple
Environment="NATS_HOST=127.0.0.1"
Environment="NATS_PORT=4222"
Environment="STREAM_PORT=8080"
Environment="API_PORT=8081"
Environment="MESSAGING_CORE_PG_HOST=127.0.0.1"
Environment="MESSAGING_CORE_PG_PORT=5432"
Environment="MESSAGING_CORE_PG_DATABASE=witsaba"
Environment="MESSAGING_CORE_PG_USER=pg-messaging-core"
Environment="MESSAGING_CORE_PG_PASSWORD=${PG_MESSAGING_PASSWORD}"
ExecStart=${HOME}/.witsaba/bin/messaging-core
Restart=always

[Install]
WantedBy=default.target
SERVICE_EOF

cat > "$SYSTEMD_DIR/witsaba-workers.service" << SERVICE_EOF
[Unit]
Description=Witsaba Workers
After=network.target witsaba-postgres.service

[Service]
Type=simple
Environment="DISCOVERY_WORKER_POOL_SIZE=32"
Environment="PG_HOST=127.0.0.1"
Environment="PG_PORT=5432"
Environment="PG_DATABASE=witsaba"
Environment="PG_USER=pg-worker"
Environment="PG_WORKER_PASSWORD=${PG_PASSWORD}"
ExecStart=${HOME}/.witsaba/bin/workers
Restart=always

[Install]
WantedBy=default.target
SERVICE_EOF

cat > "$SYSTEMD_DIR/witsaba-web-ui.service" << SERVICE_EOF
[Unit]
Description=Witsaba Web UI
After=network.target

[Service]
Type=simple
WorkingDirectory=${HOME}/.witsaba/frontend
ExecStart=${PNPM} preview --host 0.0.0.0 --port 4173
Environment="NODE_ENV=production"
Environment="NODE_OPTIONS=--max-old-space-size=256"
Restart=always

[Install]
WantedBy=default.target
SERVICE_EOF

# Enable services
systemctl --user daemon-reload
systemctl --user enable witsaba-postgres.service witsaba-messaging-core.service witsaba-workers.service witsaba-web-ui.service

log_ok "Services created and enabled!"
echo ""
echo "Start with:"
echo "  systemctl --user start witsaba-postgres"
echo "  systemctl --user start witsaba-messaging-core"
echo "  systemctl --user start witsaba-workers"
echo "  systemctl --user start witsaba-web-ui"
