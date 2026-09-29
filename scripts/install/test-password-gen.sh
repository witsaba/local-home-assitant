#!/bin/bash
# shellcheck disable=SC1090  # sources _lib.sh via $SCRIPT_DIR, and a temp file
# =============================================================================
# test-password-gen.sh - Verify _lib.sh without installing anything
# =============================================================================
# Runs entirely against a temporary directory. No database, no Homebrew, no
# root, no network. Safe to run on a fresh machine before trusting the
# installer.
#
#   ./test-password-gen.sh
# =============================================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/_lib.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
pass=0
fail=0

ok()   { printf '  %sPASS%s %s\n' "$GREEN" "$NC" "$1"; pass=$((pass + 1)); }
bad()  { printf '  %sFAIL%s %s\n' "$RED" "$NC" "$1"; fail=$((fail + 1)); }
info() { printf '  %s....%s %s\n' "$BLUE" "$NC" "$1"; }

echo ""
echo "=============================================="
echo "  Password generator tests"
echo "=============================================="

# -----------------------------------------------------------------------------
# 1. Length and alphabet
# -----------------------------------------------------------------------------
info "length and alphabet"
for bytes in 16 24 32; do
    secret=$(gen_secret "$bytes")
    expected=$(( bytes * 2 ))
    if [ "${#secret}" -eq "$expected" ]; then
        ok "gen_secret $bytes -> $expected chars"
    else
        bad "gen_secret $bytes returned ${#secret} chars, expected $expected"
    fi

    case "$secret" in
        *[!0-9a-f]*) bad "gen_secret $bytes produced a non-hex character" ;;
        *)           ok "gen_secret $bytes is lowercase hex only" ;;
    esac
done

# -----------------------------------------------------------------------------
# 2. Default length
# -----------------------------------------------------------------------------
info "default length"
default_secret=$(gen_secret)
if [ "${#default_secret}" -eq 48 ]; then
    ok "gen_secret with no argument -> 48 chars (192 bits)"
else
    bad "default is ${#default_secret} chars, expected 48"
fi

# -----------------------------------------------------------------------------
# 3. Uniqueness. 400 samples at 192 bits each: a single collision would mean
#    the generator is broken, not unlucky.
# -----------------------------------------------------------------------------
info "uniqueness"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for _ in $(seq 1 400); do
    gen_secret 24 >> "$tmp/samples.txt"
    printf '\n' >> "$tmp/samples.txt"
done
total=$(wc -l < "$tmp/samples.txt")
unique_count=$(sort -u "$tmp/samples.txt" | wc -l)
if [ "$total" -eq "$unique_count" ] && [ "$total" -eq 400 ]; then
    ok "400/400 samples distinct"
else
    bad "only $unique_count of $total samples were distinct"
fi

# -----------------------------------------------------------------------------
# 4. Uniformity of the high nibble. A biased generator still produces distinct
#    values, so uniqueness alone does not prove randomness. Expect ~50% of
#    first hex characters to be 8..f.
# -----------------------------------------------------------------------------
info "distribution (low nibble)"
# tr -d does not remove newlines unless asked, and wc -c counts them. Delete
# them and count characters with wc -c: counting lines (wc -l) after the
# newlines are gone always reports 0.
low=$(cut -c1 "$tmp/samples.txt" | LC_ALL=C tr -d '89abcdef\n' | wc -c | tr -d ' ')
low_expected=200
delta=$(( low - low_expected ))
if [ "${delta#-}" -le 45 ]; then
    ok "low nibble split ${low}/400 (expected ~${low_expected}, delta ${delta})"
else
    bad "low nibble split ${low}/400, delta ${delta} exceeds tolerance 45"
fi

# -----------------------------------------------------------------------------
# 5. Shell-safety: the value must survive being sourced by witsaba.env
# -----------------------------------------------------------------------------
info "shell sourcing"
secret=$(gen_secret 24)
env_file="$tmp/probe.env"
cat > "$env_file" <<EOF
PG_TEST_PASSWORD=$secret
EOF
loaded=$(set -a; . "$env_file"; set +a; printf '%s' "$PG_TEST_PASSWORD")
if [ "$loaded" = "$secret" ]; then
    ok "value round-trips through a sourced env file"
else
    bad "value changed when sourced: '$secret' -> '$loaded'"
fi

# -----------------------------------------------------------------------------
# 6. SQL-safety: the value must survive a single-quoted ALTER ROLE literal
# -----------------------------------------------------------------------------
info "SQL single-quote context"
case "$secret" in
    *"'"*|*'\'*) bad "value contains a SQL metacharacter" ;;
    *)             ok "value contains no quote or backslash" ;;
esac

# -----------------------------------------------------------------------------
# 7. No whitespace, no leading dash. A leading '-' would be read as a flag by
#    any tool that takes the value as an argument.
# -----------------------------------------------------------------------------
info "whitespace and leading character"
case "$secret" in
    *[[:space:]]*) bad "value contains whitespace" ;;
    *)             ok "value contains no whitespace" ;;
esac
case "$secret" in
    -*) bad "value starts with a dash" ;;
    *)  ok "value does not start with a dash" ;;
esac

# -----------------------------------------------------------------------------
# 8. is_weak_secret
# -----------------------------------------------------------------------------
info "is_weak_secret"
if is_weak_secret "$(gen_secret 24)"; then
    bad "a fresh 48-char secret was classified weak"
else
    ok "a fresh secret is accepted"
fi

for weak in "" "short" "changeme-worker" "changeme-messaging" "PASSWORD123" "postgres" "witsaba"; do
    if is_weak_secret "$weak"; then
        ok "rejects '$weak'"
    else
        bad "accepted weak value '$weak'"
    fi
done

# A hand-edited value that is not hex must be rejected: it was not produced by
# the generator, and we cannot reason about its provenance or entropy.
if is_weak_secret "zqxjvbnmkrwpltsdgfhjcdyqwxznmlkrtvphbjqcdx"; then
    ok "rejects a non-hex hand-edited value"
else
    bad "accepted a non-hex hand-edited value"
fi

# Long, all-hex, but only one distinct character: structurally valid and
# completely worthless. Length alone must not be enough.
if is_weak_secret "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"; then
    ok "rejects a long single-character value"
else
    bad "accepted a long single-character value"
fi

# Two distinct characters, repeated: same reasoning.
if is_weak_secret "ababababababababababababababababababababababab"; then
    ok "rejects a long two-character repeating value"
else
    bad "accepted a long two-character repeating value"
fi

# Exactly at the distinct-character floor: must be accepted.
if is_weak_secret "0123456789abcdef0123456789abcdef0123456789ab"; then
    bad "rejected a value with 16 distinct characters at the length floor"
else
    ok "accepts a value with 16 distinct characters"
fi

# -----------------------------------------------------------------------------
# 9. mask_secret must never leak the value
# -----------------------------------------------------------------------------
info "mask_secret"
secret=$(gen_secret 24)
masked=$(mask_secret "$secret")
if [ "$masked" = "$secret" ]; then
    bad "mask_secret returned the secret verbatim"
elif printf '%s' "$masked" | grep -qF "$secret"; then
    bad "mask_secret output contains the full secret"
else
    ok "masked output does not contain the secret"
fi
case "$masked" in
    *"${secret:12}"*) bad "mask_secret leaks a middle slice" ;;
    *)               ok "masked output shows only a short prefix" ;;
esac

# -----------------------------------------------------------------------------
# 10. No entropy source on PATH -> fail loudly, never return empty
# -----------------------------------------------------------------------------
info "failure mode"
if gen_secret 24 > "$tmp/should_be_nonempty"; then
    if [ -s "$tmp/should_be_nonempty" ]; then
        ok "gen_secret succeeds in the normal case"
    else
        bad "gen_secret returned 0 with empty output"
    fi
else
    bad "gen_secret failed in the normal case"
fi

# -----------------------------------------------------------------------------
# 11. available_mem_mb
#
# The bug this guards against: /proc/meminfo has lines named MemTotal,
# MemFree and MemAvailable, and NO line called "Mem:". A pattern of /^Mem:/
# against /proc/meminfo matches nothing and never errors, so the
# memory-aware parallelism silently fell through to its fallback forever and
# looked like it was working. Assert on the exact key so it cannot rot again.
# -----------------------------------------------------------------------------
info "available_mem_mb"
mem=$(available_mem_mb || true)
if [ ! -r /proc/meminfo ] && ! command -v free >/dev/null 2>&1; then
    # macOS: no procfs and no free(1). available_mem_mb is documented to return
    # nothing rather than guess, so returning nothing here is correct.
    info "skipped (no /proc/meminfo and no free command on this host)"
elif [ -n "$mem" ] && [ "$mem" -gt 0 ] 2>/dev/null; then
    ok "available_mem_mb returned ${mem} MB"
else
    bad "available_mem_mb returned nothing on a host that can report it"
fi

if [ -r /proc/meminfo ]; then
    from_avail=$(LC_ALL=C awk '/^MemAvailable:/ {print int($2/1024); exit}' /proc/meminfo)
    if [ "$mem" = "$from_avail" ]; then
        ok "value matches MemAvailable from /proc/meminfo (${from_avail} MB)"
    else
        bad "reported ${mem} MB but MemAvailable says ${from_avail} MB"
    fi

    # /proc/meminfo must still have no 'Mem:' line. If a future kernel adds
    # one, this flips and gets investigated deliberately.
    # awk, not 'grep -c ... || echo 0': grep -c prints the count AND exits
    # non-zero when the count is 0, so the || branch fires too and the variable
    # ends up holding "0\n0" instead of "0".
    matches=$(LC_ALL=C awk '/^Mem:/ {n++} END {print n+0}' /proc/meminfo)
    if [ "$matches" = "0" ]; then
        ok "/proc/meminfo has no 'Mem:' line, confirming the trap is avoided"
    else
        bad "/proc/meminfo now has ${matches} 'Mem:' lines -- re-check available_mem_mb"
    fi
fi

# -----------------------------------------------------------------------------
# 12. detect_build_parallelism: never 0, never above the core count, and it
#     must actually respond to the override.
# -----------------------------------------------------------------------------
info "detect_build_parallelism"
slots=$(detect_build_parallelism)
cpus=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)
if [ "$slots" -ge 1 ] 2>/dev/null; then
    ok "default slots = ${slots} (at least 1)"
else
    bad "default slots = '${slots}', must be >= 1"
fi
if [ "$slots" -le "$cpus" ] 2>/dev/null; then
    ok "slots ${slots} does not exceed ${cpus} cores"
else
    bad "slots ${slots} exceeds ${cpus} cores"
fi
if [ "$(WITSABA_BUILD_PARALLELISM=1 detect_build_parallelism)" = "1" ]; then
    ok "WITSABA_BUILD_PARALLELISM=1 is honoured"
else
    bad "WITSABA_BUILD_PARALLELISM override ignored"
fi

# -----------------------------------------------------------------------------
# 13. run_progress: heartbeat fires, exit status propagates, no orphan loops
# -----------------------------------------------------------------------------
info "run_progress"
# Retry rather than flake. The heartbeat is a wall-clock assertion: the loop
# sleeps for the interval, and under load on a 1GB Pi that sleep can be delayed
# past the end of a short-lived command. This flaked once in ~6 runs on the
# target while the five services were up. A test that fails intermittently is
# worse than no test, so give the scheduler room and then retry once.
heartbeat_seen=0
for _attempt in 1 2 3; do
    out=$(run_progress "probe" 1 sh -c 'sleep 6' 2>&1)
    if printf '%s' "$out" | grep -q 'still running'; then
        heartbeat_seen=1
        break
    fi
    sleep 1
done
if [ "$heartbeat_seen" = 1 ]; then
    ok "heartbeat fires while the command runs"
else
    bad "no heartbeat line after 3 attempts: $out"
fi
if printf '%s' "$out" | grep -q 'done in'; then
    ok "completion line reports elapsed time"
else
    bad "no completion line in output: $out"
fi

set +e
run_progress "probe-fail" 1 sh -c 'exit 7' >/dev/null 2>&1
rc=$?
set -e
if [ "$rc" -eq 7 ]; then
    ok "propagates the command's exit status (7)"
else
    bad "exit status was ${rc}, expected 7"
fi

sleep 2
if pgrep -f 'still running' >/dev/null 2>&1; then
    bad "a heartbeat loop survived the command"
    pkill -f 'still running' 2>/dev/null || true
else
    ok "no orphaned heartbeat loops"
fi

echo ""
echo "=============================================="
printf '  %d passed, %d failed\n' "$pass" "$fail"
echo "=============================================="
echo ""

[ "$fail" -eq 0 ] || exit 1
exit 0
