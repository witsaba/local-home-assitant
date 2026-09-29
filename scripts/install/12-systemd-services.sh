#!/bin/bash
# =============================================================================
# 12-systemd-services.sh - Create systemd user services
# =============================================================================
# Creates systemd user units for auto-start on boot (with lingering enabled).
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
echo "  Step 12: Create systemd User Services"
echo "=============================================="
echo ""

# Check if lingering is enabled
if ! loginctl show-session "$(loginctl | grep "$USER" | awk '{print $1}')" 2>/dev/null | grep -q "Linger=yes"; then
    log_info "Enabling systemd user lingering..."
    log_info "This allows services to run after logout."
    log_info "Run this command with sudo:"
    echo ""
    echo "  sudo loginctl enable-linger $USER"
    echo ""
    log_warn "Skipping service creation until lingering is enabled."
    log_warn "Run this script again after enabling linger."
    exit 0
fi

# Load witsaba env
if [ -f "$INSTALL_DIR/.env" ]; then
    export $(cat "$INSTALL_DIR/.env" | grep -v '^#' | xargs)
fi

# Set defaults
PG_PASSWORD="${PG_PASSWORD:-changeme-worker}"
PG_MESSAGING_PASSWORD="${PG_MESSAGING_PASSWORD:-changeme-messaging}"

# Create systemd user directory
SYSTEMD_DIR="$HOME/.config/systemd/user"
mkdir -p "$SYSTEMD_DIR"

# Create postgres startup service (if using homebrew postgres)
cat > "$SYSTEMD_DIR/witsaba-postgres.service" << 'SERVICE_EOF'
[Unit]
Description=Witsaba PostgreSQL (Homebrew)
After=network.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/home/linuxbrew/.linuxbrew/bin/pg_ctl -D /home/liwaisi/.witsaba/postgres/data -l /home/liwaisi/.witsaba/postgres/logs/postgresql.log start
ExecStop=/home/linuxbrew/.linuxbrew/bin/pg_ctl -D /home/liwaisi/.witsaba/postgres/data stop -m fast
Restart=on-failure

[Install]
WantedBy=default.target
SERVICE_EOF

# messaging-core service
cat > "$SYSTEMD_DIR/witsaba-messaging-core.service" << SERVICE_EOF
[Unit]
Description=Witsaba Messaging Core (NATS + WebSocket Gateway)
After=network.target witsaba-postgres.service

[Service]
Type=simple
Environment="NATS_HOST=127.0.0.1"
Environment="NATS_PORT=4222"
Environment="STREAM_PORT=8080"
Environment="API_PORT=8081"
Environment="LOG_LEVEL=info"
Environment="MESSAGING_CORE_PG_HOST=127.0.0.1"
Environment="MESSAGING_CORE_PG_PORT=5432"
Environment="MESSAGING_CORE_PG_DATABASE=witsaba"
Environment="MESSAGING_CORE_PG_USER=pg-messaging-core"
Environment="MESSAGING_CORE_PG_PASSWORD=${PG_MESSAGING_PASSWORD}"
ExecStart=${HOME}/.witsaba/bin/messaging-core
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
SERVICE_EOF

# workers service
cat > "$SYSTEMD_DIR/witsaba-workers.service" << SERVICE_EOF
[Unit]
Description=Witsaba Workers (Device Discovery)
After=network.target witsaba-postgres.service

[Service]
Type=simple
Environment="LOG_LEVEL=info"
Environment="DISCOVERY_INTERVAL_SECONDS=60"
Environment="DISCOVERY_WORKER_POOL_SIZE=32"
Environment="DISCOVERY_PROBE_TIMEOUT_MS=1500"
Environment="PG_HOST=127.0.0.1"
Environment="PG_PORT=5432"
Environment="PG_DATABASE=witsaba"
Environment="PG_USER=pg-worker"
Environment="PG_WORKER_PASSWORD=${PG_PASSWORD}"
ExecStart=${HOME}/.witsaba/bin/workers
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
SERVICE_EOF

# web_ui service
cat > "$SYSTEMD_DIR/witsaba-web-ui.service" << SERVICE_EOF
[Unit]
Description=Witsaba Web UI (Qwik Preview)
After=network.target

[Service]
Type=simple
WorkingDirectory=${HOME}/.witsaba/frontend
ExecStartPre=/bin/sleep 3
ExecStart=/home/linuxbrew/.linuxbrew/bin/pnpm preview --host 0.0.0.0 --port 4173
Environment="NODE_ENV=production"
Environment="NODE_OPTIONS=--max-old-space-size=256"
Environment="HOST=0.0.0.0"
Environment="PORT=4173"
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
SERVICE_EOF

# Reload systemd
log_info "Reloading systemd daemon..."
systemctl --user daemon-reload

# Enable services
log_info "Enabling services..."
systemctl --user enable witsaba-postgres.service
systemctl --user enable witsaba-messaging-core.service
systemctl --user enable witsaba-workers.service
systemctl --user enable witsaba-web-ui.service

echo ""
log_ok "Systemd user services created!"
echo ""
echo "Services created:"
echo "  witsaba-postgres.service    - PostgreSQL database"
echo "  witsaba-messaging-core.service - NATS + WebSocket gateway"
echo "  witsaba-workers.service     - Device discovery"
echo "  witsaba-web-ui.service      - Web UI (port 4173)"
echo ""
echo "Service commands:"
echo "  systemctl --user start witsaba-postgres"
echo "  systemctl --user start witsaba-messaging-core"
echo "  systemctl --user start witsaba-workers"
echo "  systemctl --user start witsaba-web-ui"
echo ""
echo "  systemctl --user status witsaba-messaging-core"
echo "  journalctl --user-unit=witsaba-messaging-core -f"
echo ""
log_ok "Installation complete!"
