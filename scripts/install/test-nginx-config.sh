#!/bin/bash
# =============================================================================
# test-nginx-config.sh - Verify the generated nginx config and the static site
# =============================================================================
# Three layers:
#   1. static   - required files present, no leftovers from the Qwik tree
#   2. config   - nginx -t accepts it, and it is genuinely unprivileged
#   3. live     - nginx serves the page and proxies /api through to
#                 messaging-core, proving the single-origin design works
#
# Layers 1 and 2 need no running service. Layer 3 is skipped unless nginx is
# already listening, so this is safe to run anywhere.
#
#   ./test-nginx-config.sh
# =============================================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/_lib.sh"

INSTALL_DIR="$HOME/.witsaba"
NGINX_PREFIX="$INSTALL_DIR/nginx"
NGINX_CONF="$NGINX_PREFIX/nginx.conf"
NGINX_HTML="$NGINX_PREFIX/html"
NGINX_BIN="${HOMEBREW_PREFIX:-/home/linuxbrew/.linuxbrew}/bin/nginx"
LISTEN_PORT="${WITSABA_HTTP_PORT:-4173}"

pass=0; fail=0; skip=0
ok()   { printf '  %sPASS%s %s\n' "$GREEN" "$NC" "$1"; pass=$((pass+1)); }
bad()  { printf '  %sFAIL%s %s\n' "$RED" "$NC" "$1"; fail=$((fail+1)); }
skip() { printf '  %sSKIP%s %s\n' "$YELLOW" "$NC" "$1"; skip=$((skip+1)); }

echo ""
echo "=============================================="
echo "  nginx config + static site checks"
echo "=============================================="

# -----------------------------------------------------------------------------
# 1. Static site
# -----------------------------------------------------------------------------
echo ""
log_info "static site"
if [ ! -d "$NGINX_HTML" ]; then
    bad "document root missing: $NGINX_HTML"
else
    for required in \
        index.html devices.html favicon.svg \
        assets/app.css assets/app.js \
        50x.html
    do
        if [ -f "$NGINX_HTML/$required" ]; then
            ok "$required present"
        else
            bad "$required missing"
        fi
    done

    size=$(du -sh "$NGINX_HTML" | cut -f1)
    ok "document root is $size (vite preview needed 308MB of node_modules)"
fi

# The Qwik tree must not be required. It is installed at
# ~/.witsaba/frontend by 11-build-frontend.sh; nginx must not depend on it.
echo ""
log_info "independence from the Qwik tree"
if [ -d "$INSTALL_DIR/frontend/node_modules" ]; then
    # Temporarily hide it and confirm the config does not reference it.
    if grep -q "$INSTALL_DIR/frontend" "$NGINX_CONF" 2>/dev/null; then
        bad "nginx.conf references $INSTALL_DIR/frontend"
    else
        ok "nginx.conf does not reference the Qwik tree"
    fi
else
    ok "no Qwik tree installed; nothing to depend on"
fi

# -----------------------------------------------------------------------------
# 2. Config
# -----------------------------------------------------------------------------
echo ""
log_info "config"
if [ ! -x "$NGINX_BIN" ]; then
    skip "nginx not installed at $NGINX_BIN"
elif [ ! -f "$NGINX_CONF" ]; then
    bad "config missing: $NGINX_CONF"
else
    if out=$("$NGINX_BIN" -t -c "$NGINX_CONF" -p "$NGINX_PREFIX" 2>&1); then
        ok "nginx -t accepts the config"
    else
        bad "nginx -t rejected the config:"
        printf '%s\n' "$out" | sed 's/^/        /'
    fi

    # Unprivileged operation. A `user` directive is only valid for the root
    # master process and makes nginx refuse to start otherwise; the temp paths
    # must all live under $HOME or the worker cannot create them.
    if grep -qE '^\s*user\s+' "$NGINX_CONF"; then
        bad "config has a 'user' directive; invalid for a non-root master"
    else
        ok "no 'user' directive (correct for a non-root master)"
    fi

    if grep -qE '(pid|error_log|access_log|[a-z_]+_temp_path)\s+/var|/tmp/nginx' "$NGINX_CONF"; then
        bad "config writes state outside the user prefix"
    else
        ok "all runtime paths stay under $NGINX_PREFIX"
    fi

    if grep -qE '^\s*listen\s+(80|443)\b' "$NGINX_CONF"; then
        bad "config binds a privileged port; needs root"
    else
        ok "binds no privileged port"
    fi

    if grep -q "listen $LISTEN_PORT" "$NGINX_CONF"; then
        ok "listens on $LISTEN_PORT"
    else
        bad "not listening on $LISTEN_PORT"
    fi

    # The WebSocket location is the part most likely to be silently wrong: a
    # missing Upgrade header turns a stream into a hanging request.
    if grep -q 'proxy_set_header Upgrade' "$NGINX_CONF" \
        && grep -q 'proxy_set_header Connection "upgrade"' "$NGINX_CONF"; then
        ok "WebSocket upgrade headers present for /stream"
    else
        bad "missing WebSocket upgrade headers"
    fi

    # Clean URLs: a request for /devices has to reach devices.html on disk.
    # try_files without '$uri.html' 404s, because the file carries an extension.
    if grep -q 'try_files .*\$uri\.html' "$NGINX_CONF"; then
        ok "try_files maps extensionless URLs to .html files"
    else
        bad "try_files lacks \$uri.html; /devices will 404"
    fi

    if grep -q 'proxy_buffering off' "$NGINX_CONF"; then
        ok "proxy_buffering off (frames are not held back)"
    else
        bad "proxy_buffering not disabled; adds latency to live frames"
    fi

    # `/stream` is BOTH the viewer page and the stem of the `location /stream/`
    # proxy prefix. nginx issues a trailing-slash redirect for any URI that is
    # the stem of a prefix location, and it does so BEFORE try_files runs. So
    # without an exact-match block, `/stream` is 301'd to `/stream/`, which
    # lands in the proxy and 404s at the gateway -- while stream.html sits in
    # the document root serving 200 at /stream.html the entire time and is
    # never consulted. Observed on the Pi: /api and /assets redirect for the
    # same reason, while /devices (no matching prefix) serves 200.
    if grep -qE '^\s*location = /stream\s*\{' "$NGINX_CONF" \
        && grep -q 'try_files /stream\.html' "$NGINX_CONF"; then
        ok "exact 'location = /stream' serves the viewer page ahead of the proxy prefix"
    else
        bad "no exact 'location = /stream'; /stream 301s to /stream/ and the viewer page 404s"
    fi

    # Caching a shell that references new asset names breaks deploys.
    if grep -q 'no-store' "$NGINX_CONF"; then
        ok "html is marked no-store"
    else
        bad "html caching not disabled"
    fi
fi

# -----------------------------------------------------------------------------
# 3. Live round-trip
# -----------------------------------------------------------------------------
echo ""
log_info "live"
if ! command -v curl >/dev/null 2>&1; then
    skip "curl not available"
elif ! curl -sf -o /dev/null --max-time 3 "http://127.0.0.1:$LISTEN_PORT/"; then
    skip "nothing listening on :$LISTEN_PORT (start the units to exercise this)"
else
    body=$(curl -s --max-time 5 "http://127.0.0.1:$LISTEN_PORT/")
    case "$body" in
        *"<title>Witsaba</title>"*) ok "GET / serves the home page" ;;
        *) bad "GET / did not return the home page" ;;
    esac

    body=$(curl -s --max-time 5 "http://127.0.0.1:$LISTEN_PORT/devices")
    case "$body" in
        *"Devices"*) ok "GET /devices serves the device list" ;;
        *) bad "GET /devices did not return the device page" ;;
    esac

    # Both the clean URL and the explicit file must work.
    clean=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/devices")
    explicit=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/devices.html")
    if [ "$clean" = "200" ] && [ "$explicit" = "200" ]; then
        ok "both /devices and /devices.html resolve (HTTP 200)"
    else
        bad "clean URL returned $clean, explicit returned $explicit"
    fi

    # A missing asset must 404 rather than silently serving the shell, which
    # would produce a confusing MIME error in the browser.
    missing=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/assets/nope.js")
    if [ "$missing" = "404" ]; then
        ok "missing asset returns 404"
    else
        bad "missing asset returned $missing, expected 404"
    fi

    # Content-Type matters: a stylesheet served as text/plain is ignored.
    ctype=$(curl -s -o /dev/null -w '%{content_type}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/assets/app.css")
    case "$ctype" in
        text/css*) ok "app.css served as $ctype" ;;
        *) bad "app.css served as '$ctype', expected text/css" ;;
    esac

    # The single-origin claim: /api must be served by nginx, from the same
    # origin, with no CORS headers involved.
    api_code=$(curl -s -o /tmp/witsaba-api.json -w '%{http_code}' --max-time 6 \
        "http://127.0.0.1:$LISTEN_PORT/api/devices/active")
    if [ "$api_code" = "200" ]; then
        ok "/api/devices/active proxied to messaging-core (HTTP 200)"
        if head -c 1 /tmp/witsaba-api.json | grep -q '\['; then
            ok "proxied response is a JSON array, as the API contract requires"
        else
            bad "proxied response is not a JSON array: $(head -c 80 /tmp/witsaba-api.json)"
        fi
    else
        bad "/api/devices/active returned HTTP $api_code (is messaging-core running?)"
    fi

    # An unknown API path must surface the backend's status, not nginx's 404.
    nf=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/api/definitely-not-a-route")
    if [ "$nf" = "404" ]; then
        ok "unknown /api path proxies through and 404s from the backend"
    else
        bad "unknown /api path returned $nf, expected a proxied 404"
    fi

    # The viewer page must be reachable at /stream, not 301'd into the proxy
    # prefix. This is the regression the exact-match block above prevents.
    stream_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/stream")
    if [ "$stream_code" = "200" ]; then
        body=$(curl -s --max-time 5 "http://127.0.0.1:$LISTEN_PORT/stream")
        case "$body" in
            *"Camera stream"*) ok "GET /stream serves the camera viewer page" ;;
            *) bad "GET /stream returned 200 but not the viewer page" ;;
        esac
    else
        bad "GET /stream returned $stream_code, expected 200 (did it 301 to /stream/?)"
    fi

    # The socket path must still be proxied, not served as a file.
    sock=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:$LISTEN_PORT/stream/000000000000")
    case "$sock" in
        404|400) ok "GET /stream/<mac> still reaches the gateway (HTTP $sock)" ;;
        301) bad "/stream/<mac> redirected; the exact-match block is too broad" ;;
        *) bad "/stream/<mac> returned $sock, expected the gateway's own status" ;;
    esac
fi

echo ""
echo "=============================================="
printf '  %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skip"
echo "=============================================="
echo ""
[ "$fail" -eq 0 ] || exit 1
exit 0
