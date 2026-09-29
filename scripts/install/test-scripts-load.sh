#!/bin/bash
# =============================================================================
# test-scripts-load.sh - Static check that every script's dependencies resolve
# =============================================================================
# bash -n only validates syntax. These scripts call shared helpers from
# _lib.sh, and a script can pass bash -n while calling a function that does
# not exist -- which only shows up as "command not found" at runtime, on the
# target, after the step has already started.
#
# Checks, for each install script:
#   1. sources _lib.sh if it calls any helper defined there
#   2. calls only functions that _lib.sh actually defines
#   3. defines the log_* helpers it uses
#
#   ./test-scripts-load.sh
# =============================================================================

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR" || exit 1

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
pass=0; fail=0
ok()  { printf '  %sPASS%s %s\n' "$GREEN" "$NC" "$1"; pass=$((pass+1)); }
bad() { printf '  %sFAIL%s %s\n' "$RED" "$NC" "$1"; fail=$((fail+1)); }

echo ""
echo "=============================================="
echo "  Script load / helper resolution checks"
echo "=============================================="

# Every function _lib.sh defines.
LIB_FUNCS=$(grep -oE '^[a-z_][a-z0-9_]*\(\)' _lib.sh | tr -d '()' | sort -u)
LIB_COUNT=$(printf '%s\n' "$LIB_FUNCS" | grep -c . )
echo ""
echo "  _lib.sh defines $LIB_COUNT functions:"
printf '%s\n' "$LIB_FUNCS" | tr '\n' ' ' | fold -sw 74 -s | sed 's/^/    /'
echo ""

# The helpers a script is expected to provide for itself.
LOG_FUNCS="log_info log_ok log_warn log_err"

for script in 00-brew.sh 01-postgresql.sh 02-go.sh 03-node.sh 04-postgres-init.sh \
              10-build-go.sh 11-build-frontend.sh 12-systemd-services.sh \
              install-ubuntu.sh uninstall-ubuntu.sh; do
    [ -f "$script" ] || { bad "$script: missing"; continue; }
    echo "  --- $script ---"

    # 1. syntax
    if bash -n "$script" 2>/dev/null; then
        ok "syntax"
    else
        bad "syntax"; continue
    fi

    sources_lib=$(grep -c '\. "\$SCRIPT_DIR/_lib.sh"' "$script")
    self_defined=$(grep -oE '^[a-z_][a-z0-9_]*\(\)' "$script" | tr -d '()' | sort -u)

    # 2. every _lib.sh function the script mentions as a bare word must be
    #    resolvable. Match on the word, not on a trailing paren: helpers are
    #    routinely invoked as `setup_node_env` or $(pnpm_bin), with no
    #    parentheses at all, and a call-site regex silently misses those.
    missing=""
    for fn in $LIB_FUNCS; do
        # Only count it as a use if the name appears outside comments.
        if grep -vE '^[[:space:]]*#' "$script" | grep -qE "(^|[^a-z0-9_-])${fn}([^a-z0-9_]|$)"; then
            if [ "$sources_lib" -eq 0 ] && ! printf '%s\n' "$self_defined" | grep -qx "$fn"; then
                missing="$missing $fn(no-source)"
            fi
        fi
    done
    if [ -z "$missing" ]; then
        ok "all _lib.sh helpers resolvable"
    else
        bad "unresolvable helper(s):$missing"
    fi

    # 3. log_* it uses must be defined here or come from _lib.sh
    used_logs=""
    for lf in $LOG_FUNCS; do
        if grep -qE "(^|[^a-z_])$lf " "$script" || grep -qE "(^|[^a-z_])$lf\"" "$script"; then
            used_logs="$used_logs $lf"
        fi
    done
    undef=""
    for lf in $used_logs; do
        printf '%s\n' "$self_defined" | grep -qx "$lf" && continue
        if [ "$sources_lib" -eq 1 ] && printf '%s\n' "$LIB_FUNCS" | grep -qx "$lf"; then
            continue
        fi
        undef="$undef $lf"
    done
    if [ -z "$undef" ]; then
        ok "log helpers available"
    else
        bad "log helper(s) used but never defined:$undef"
    fi
done

echo ""
echo "=============================================="
printf '  %d passed, %d failed\n' "$pass" "$fail"
echo "=============================================="
echo ""
[ "$fail" -eq 0 ] || exit 1
exit 0
