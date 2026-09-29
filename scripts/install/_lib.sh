#!/bin/bash
# =============================================================================
# _lib.sh - Shared helpers for the witsaba install scripts
# =============================================================================
# Sourced, never executed. Keep this dependency-free: coreutils only, no
# openssl, no python, no perl. The target is a minimal Ubuntu Server image on
# a 1GB Raspberry Pi where an extra toolchain is a real cost.
#
# This file is self-sufficient: sourcing it gives you the colour/logging
# helpers as well as the utility functions. A script that only sources this
# and calls log_ok must not fail with "command not found".
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[i]${NC} $1"; }
log_ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_err()  { echo -e "${RED}[x]${NC} $1" >&2; }

# -----------------------------------------------------------------------------
# gen_secret
#
#   gen_secret [bytes]
#
# Writes a random lowercase hex string to stdout and returns 0. The length is
# always exactly bytes*2 characters, or 1 on failure.
#
# Entropy: 4 bits per hex character, taken straight from the kernel CSPRNG.
#   24 bytes -> 48 chars -> 192 bits. 32 bytes -> 64 chars -> 256 bits.
#
# Why hex and not a bigger alphabet:
#
#   This value is not a human-chosen password. It is a machine credential that
#   gets written into three different parsers, and every extra symbol class is
#   a place where those parsers disagree:
#
#     1. SQL      -- embedded in  ALTER ROLE "pg-worker" PASSWORD '<value>'
#                    A single quote or backslash needs escaping and is a
#                    classic injection/breakage source.
#     2. shell    -- the file is sourced with  set -a; . witsaba.env
#                    $, `, ", \ and whitespace all change meaning.
#     3. systemd  -- parsed as EnvironmentFile, whose quoting rules differ
#                    from both POSIX shell and SQL.
#
#   Lowercase hex is [0-9a-f] only, so it is inert in all three. It also has no
#   visually ambiguous characters (no 0/O, no 1/l/I), so it can be read aloud or
#   transcribed from a screen without error.
#
#   The smaller alphabet is fully paid for by length: 192 bits is astronomically
#   beyond brute force, and far stronger than a 12-character password that
#   satisfies a "must contain upper, lower, digit and symbol" policy.
#
# Why od and not the usual  tr -dc 'A-Za-z0-9' </dev/urandom | head -c N
#
#   od -N reads an exact byte count and then exits on its own. The tr idiom
#   depends on SIGPIPE tearing down the pipeline, so its behaviour varies with
#   shell options (pipefail), tr implementation and buffering. A credential
#   generator should not have a code path that can block waiting for input
#   that never arrives.
# -----------------------------------------------------------------------------
gen_secret() {
    local bytes="${1:-24}"
    local out

    # od fails on a short read rather than returning partial entropy, and
    # /dev/urandom never blocks once the pool is initialised.
    out=$(LC_ALL=C od -An -v -tx1 -N "$bytes" /dev/urandom 2>/dev/null | tr -d ' \n')

    if [ -z "$out" ]; then
        return 1
    fi

    # Guard against a short read silently yielding a weak secret. Entropy per
    # byte is uniform, so a truncated value is still random, just shorter --
    # and its length would be wrong, so reject it rather than ship it.
    if [ "${#out}" -ne $(( bytes * 2 )) ]; then
        return 1
    fi

    printf '%s' "$out"
}

# -----------------------------------------------------------------------------
# is_weak_secret
#
#   is_weak_secret <value>
#
# Returns 0 when the value must not be kept. True when it is empty, shorter
# than the minimum, one of the placeholder defaults shipped by an earlier
# version of these scripts, not hex, or carrying almost no entropy.
#
# Used to upgrade an existing deployment in place: an install that already
# exists with changeme-worker must not be treated as "already configured,
# leave it alone".
# -----------------------------------------------------------------------------
WITSABA_MIN_SECRET_LEN=32
# A 48-char value drawn from fewer than this many distinct characters is
# padding, not a password. Catches aaaaaaaaaaaa..., 121212121212... and the
# "correct horse battery staple, but make it 48 chars" pattern.
WITSABA_MIN_SECRET_DISTINCT=8

is_weak_secret() {
    local value="${1:-}"
    local lowered
    local distinct

    [ -z "$value" ] && return 0
    [ "${#value}" -lt "$WITSABA_MIN_SECRET_LEN" ] && return 0

    lowered=$(printf '%s' "$value" | LC_ALL=C tr '[:upper:]' '[:lower:]')
    case "$lowered" in
        changeme*|password*|secret*|witsaba*|postgres*|admin*|test*|example*)
            return 0
            ;;
    esac

    # Must be hex: the generator only ever emits [0-9a-f]. Anything else was
    # hand-edited, and a hand-edited value must not silently pass as generated.
    case "$value" in
        *[!0-9a-f]*) return 0 ;;
    esac

    # Structural hex alone does not imply entropy: aaaaaaaa... is valid hex and
    # worthless. Count distinct characters with coreutils only.
    #
    # Deliberately not `local -A seen=()`: associative arrays need bash 4, and
    # macOS still ships bash 3.2. The installer targets Linux, but the test
    # suite has to run wherever the developer is.
    distinct=$(printf '%s' "$value" | fold -w1 | LC_ALL=C sort -u | wc -l | tr -d ' ')
    [ "$distinct" -lt "$WITSABA_MIN_SECRET_DISTINCT" ] && return 0

    return 1
}

# -----------------------------------------------------------------------------
# new_secret
#
#   new_secret [bytes]
#
# gen_secret plus a self-check loop. A generator bug that returns a short,
# empty or degenerate value must not reach a database role, so the output is
# validated with is_weak_secret and retried. After the configured attempts it
# fails rather than emitting something unchecked.
# -----------------------------------------------------------------------------
WITSABA_SECRET_ATTEMPTS=5

new_secret() {
    local bytes="${1:-24}"
    local attempt secret

    for (( attempt = 1; attempt <= WITSABA_SECRET_ATTEMPTS; attempt++ )); do
        secret=$(gen_secret "$bytes") || continue
        if [ -n "$secret" ] && ! is_weak_secret "$secret"; then
            printf '%s' "$secret"
            return 0
        fi
    done

    return 1
}

# -----------------------------------------------------------------------------
# read_env_value
#
#   read_env_value <key> <file>
#
# Reads KEY=VALUE out of a witsaba.env-style file without sourcing it. The file
# holds credentials, so it is parsed rather than executed: sourcing a file on
# disk that another process could write to turns a config read into arbitrary
# code execution.
# -----------------------------------------------------------------------------
read_env_value() {
    local key="$1"
    local file="$2"
    local line

    [ -f "$file" ] || return 1
    line=$(grep -E "^${key}=" "$file" 2>/dev/null | head -n 1) || return 1
    [ -n "$line" ] || return 1
    line="${line#${key}=}"
    # Strip surrounding quotes if a human added them by hand.
    line="${line%\"}"; line="${line#\"}"
    printf '%s' "$line"
}

# -----------------------------------------------------------------------------
# run_progress
#
#   run_progress <label> <heartbeat_seconds> <command> [args...]
#
# Runs a long command, printing a timestamped heartbeat line every N seconds
# so a silent step is not indistinguishable from a hang. This matters most on
# the Pi: a cold `go build` of the otel + gin + pgx tree is minutes of no
# output, and "is this working or wedged?" is the question an operator
# actually has at that moment.
#
# Returns the command's own exit status.
# -----------------------------------------------------------------------------
run_progress() {
    local label="$1"; shift
    local beat="$1"; shift

    local start heartbeat rc
    start=$(date +%s)

    printf '    %s ...\n' "$label"

    # Subshell so the loop is its own process group and can be killed cleanly.
    (
        while :; do
            sleep "$beat"
            printf '    [%s] %s -- still running (%ds elapsed)\n' \
                "$(date +%H:%M:%S)" "$label" "$(( $(date +%s) - start ))"
        done
    ) &
    heartbeat=$!

    # Capture the status without touching shell options. An earlier version
    # wrapped the call in `set +e` / `set -e`; because a function body shares
    # the caller's shell, that re-enabled errexit on return and aborted any
    # caller that was deliberately running with it off. `|| rc=$?` is scoped to
    # the single command and leaves errexit exactly as it found it.
    rc=0
    "$@" || rc=$?

    kill "$heartbeat" 2>/dev/null || true
    wait "$heartbeat" 2>/dev/null || true

    local elapsed=$(( $(date +%s) - start ))
    if [ "$rc" -eq 0 ]; then
        printf '    [%s] %s -- done in %dm%02ds\n' \
            "$(date +%H:%M:%S)" "$label" $(( elapsed / 60 )) $(( elapsed % 60 ))
    else
        printf '    [%s] %s -- FAILED after %dm%02ds (exit %d)\n' \
            "$(date +%H:%M:%S)" "$label" $(( elapsed / 60 )) $(( elapsed % 60 )) "$rc"
    fi

    return "$rc"
}

# -----------------------------------------------------------------------------
# available_mem_mb
#
#   available_mem_mb
#
# Prints available RAM in MB, or nothing if it cannot be determined.
#
# Reads MemAvailable from /proc/meminfo. Note the exact key: /proc/meminfo has
# MemTotal / MemFree / MemAvailable and NO line called "Mem:" -- that row
# exists in the output of the `free` command, not in /proc/meminfo. Matching
# /^Mem:/ against /proc/meminfo silently returns nothing, which is how the
# memory-aware parallelism below once looked like it was working while in fact
# always taking the fallback branch.
# -----------------------------------------------------------------------------
available_mem_mb() {
    local kb
    kb=$(LC_ALL=C awk '/^MemAvailable:/ {print $2; exit}' /proc/meminfo 2>/dev/null | tr -d ' ')

    if [ -n "$kb" ] && [ "$kb" -gt 0 ] 2>/dev/null; then
        printf '%s' "$(( kb / 1024 ))"
        return 0
    fi

    # Fallback for hosts without procfs. The `free` row really is called Mem,
    # and column 7 is "available".
    kb=$(LC_ALL=C free -m 2>/dev/null | LC_ALL=C awk '/^Mem:/ {print $7; exit}')
    if [ -n "$kb" ] && [ "$kb" -gt 0 ] 2>/dev/null; then
        printf '%s' "$kb"
        return 0
    fi

    return 1
}

# -----------------------------------------------------------------------------
# detect_build_parallelism
#
#   detect_build_parallelism
#
# How many concurrent compiler processes to allow.
#
# `go build` runs one `compile` process per -p slot and the Go compiler is the
# single most memory-hungry thing in this install. On a 1GB Pi the default of
# GOMAXPROCS (all cores) is how you get OOM-killed halfway through a build,
# which costs far more time than the slower serial build would have.
#
# ~300MB of headroom per slot is a deliberately conservative rule of thumb for
# the otel/gin/pgx/zap dependency tree. Never exceeds the core count.
#
# Override with WITSABA_BUILD_PARALLELISM: drop it to 1 if a build still dies
# to the OOM killer, raise it if there is swap headroom to spare.
# -----------------------------------------------------------------------------
detect_build_parallelism() {
    if [ -n "${WITSABA_BUILD_PARALLELISM:-}" ]; then
        printf '%s' "$WITSABA_BUILD_PARALLELISM"
        return
    fi

    local cpus slots mem_mb

    # nproc is coreutils and absent on macOS; getconf is POSIX; sysctl is the
    # BSD fallback. The test suite runs on a Mac, and a silent "1" there would
    # hide a broken heuristic.
    cpus=$(nproc 2>/dev/null \
        || getconf _NPROCESSORS_ONLN 2>/dev/null \
        || sysctl -n hw.ncpu 2>/dev/null \
        || echo 1)
    [ -z "$cpus" ] && cpus=1

    mem_mb=$(available_mem_mb || true)

    if [ -n "$mem_mb" ] && [ "$mem_mb" -gt 0 ] 2>/dev/null; then
        slots=$(( mem_mb / 300 ))
    else
        # Memory unknown (non-Linux host). Assume a modest machine and stay
        # conservative rather than trusting the core count, which would
        # happily ask a 1GB-class machine for more compile slots than it can
        # hold.
        slots=$(( cpus / 2 ))
    fi

    [ "$slots" -lt 1 ] && slots=1
    [ "$slots" -gt "$cpus" ] && slots="$cpus"

    printf '%s' "$slots"
}

# -----------------------------------------------------------------------------
# describe_go_env
#
# Prints where Go keeps its caches and whether the build cache is warm. A cold
# cache is the difference between a 20 minute build and a 20 second one, and
# it is invisible until you are already waiting.
# -----------------------------------------------------------------------------
describe_go_env() {
    local cache_size="0"
    local cache_dir
    cache_dir=$(go env GOCACHE 2>/dev/null || echo "")

    if [ -n "$cache_dir" ] && [ -d "$cache_dir" ]; then
        cache_size=$(du -sh "$cache_dir" 2>/dev/null | cut -f1)
    fi

    local cpus
    cpus=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || echo '?')

    printf '    %-12s %s\n' "GOCACHE"    "${cache_dir:-unknown} (${cache_size})"
    printf '    %-12s %s\n' "GOMODCACHE" "$(go env GOMODCACHE 2>/dev/null || echo unknown)"
    printf '    %-12s %s\n' "GOARCH"     "$(go env GOARCH 2>/dev/null || echo unknown)"
    printf '    %-12s %s\n' "GOMAXPROCS" "${cpus} cores available"
    printf '    %-12s %s\n' "available" "$(available_mem_mb || echo unknown) MB"
    printf '    %-12s %s\n' "-p"         "$(detect_build_parallelism) compile slots"

    case "$cache_size" in
        0|""|unknown) log_warn "build cache is empty: the next build is a cold build" ;;
        *)             log_info "build cache is warm (${cache_size}); rebuilds will be fast" ;;
    esac
}

# -----------------------------------------------------------------------------
# mask_secret
#
#   mask_secret <value>
#
# Renders a secret safe to print in installer output and commit messages. Only
# the length and a short fingerprint are shown, never the value.
# -----------------------------------------------------------------------------
mask_secret() {
    local value="${1:-}"
    if [ -z "$value" ]; then
        printf '(empty)'
        return
    fi
    printf '%s... (%d chars, sha256 %s)' \
        "${value:0:4}" \
        "${#value}" \
        "$(printf '%s' "$value" | sha256sum 2>/dev/null | cut -c1-12)"
}
