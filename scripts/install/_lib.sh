#!/bin/bash
# =============================================================================
# _lib.sh - Shared helpers for the witsaba install scripts
# =============================================================================
# Sourced, never executed. Keep this dependency-free: coreutils only, no
# openssl, no python, no perl. The target is a minimal Ubuntu Server image on
# a 1GB Raspberry Pi where an extra toolchain is a real cost.
# =============================================================================

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
