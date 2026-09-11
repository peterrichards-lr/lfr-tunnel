#!/usr/bin/env bash
# test-watchdog-spool.sh -- assert gateway-watchdog.sh actually records what it healed (#1875).
#
# The defect being guarded is a restart that happens and reaches nobody, which on a running box is
# indistinguishable from a gateway that never restarted. So the cases here are about the RECORD:
# that a restart produces one, that a failed restart is not recorded as a recovery, and that the
# rate is counted -- three restarts in an hour being a different event from one.
#
# The watchdog is root-only and talks to systemd, so it runs here against a stub PATH. The risk
# with stubs is testing the stubs, so the premise case asserts the healthy path writes NOTHING,
# and the last case mutates the script and requires a firing case to stop firing.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WATCHDOG="${REPO_ROOT}/scripts/common/gateway-watchdog.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/watchdog-spool.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$WATCHDOG" ]; then
    fail "scripts/common/gateway-watchdog.sh is missing -- if it moved, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# Stub PATH. Each stub reads an env var so a case can choose what the box is doing, rather than
# every case getting the same canned answers.
STUBS="$WORK/bin"
mkdir -p "$STUBS"

cat > "$STUBS/sudo" <<'EOF'
#!/usr/bin/env bash
# Drop the sudo and run the rest through the stubs.
exec "$@"
EOF

cat > "$STUBS/systemctl" <<'EOF'
#!/usr/bin/env bash
# is-active --quiet <svc> -- exit 0 unless the case says the service stays dead.
if [ "${1:-}" = "is-active" ]; then
    [ "${STUB_NGINX_ACTIVE:-1}" = "1" ] && exit 0
    exit 3
fi
exit 0
EOF

cat > "$STUBS/curl" <<'EOF'
#!/usr/bin/env bash
# The health probes. STUB_TUNNEL_UP/STUB_NGINX_UP are the box's state before any restart;
# STUB_TUNNEL_UP_AFTER is what the restart achieved, which is how "restarted and recovered" and
# "restarted and did not recover" are told apart.
for arg in "$@"; do
    case "$arg" in
        *127.0.0.1:*/api/version)
            if [ -f "$STUB_STATE/tunnel_restarted" ]; then
                [ "${STUB_TUNNEL_UP_AFTER:-1}" = "1" ] && exit 0
                exit 7
            fi
            [ "${STUB_TUNNEL_UP:-1}" = "1" ] && exit 0
            exit 7
            ;;
        https://127.0.0.1)
            [ "${STUB_NGINX_UP:-1}" = "1" ] && exit 0
            exit 7
            ;;
    esac
done
exit 0
EOF

cat > "$STUBS/nginx" <<'EOF'
#!/usr/bin/env bash
exit "${STUB_NGINX_CONFIG_OK:-0}"
EOF

cat > "$STUBS/openssl" <<'EOF'
#!/usr/bin/env bash
# dhparam -out <file> -- create the file so the heal path completes.
while [ $# -gt 0 ]; do
    if [ "$1" = "-out" ]; then shift; : > "$1"; fi
    shift
done
exit 0
EOF
chmod +x "$STUBS"/*

# systemctl restart lfr-tunneld has to leave a trace the curl stub can see, so that the probe
# after a restart can answer differently from the probe before it.
cat > "$STUBS/systemctl" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "is-active" ]; then
    [ "${STUB_NGINX_ACTIVE:-1}" = "1" ] && exit 0
    exit 3
fi
if [ "${1:-}" = "restart" ] && [ "${2:-}" = "lfr-tunneld" ]; then
    touch "$STUB_STATE/tunnel_restarted"
fi
exit 0
EOF
chmod +x "$STUBS/systemctl"

# run_watchdog <case-dir> [env assignments...] -- runs the script with a private spool and a
# private Let's Encrypt pair that already exists, so the SSL-heal path is quiet unless a case
# deliberately removes them. Echoes the spool path.
run_watchdog() {
    local dir="$1"; shift
    mkdir -p "$dir/state"
    : "${SPOOL:=$dir/spool.jsonl}"
    if [ ! -f "$dir/le-options" ]; then
        : > "$dir/le-options"
        : > "$dir/le-dhparams"
    fi
    env PATH="$STUBS:/usr/bin:/bin:/usr/sbin:/sbin" \
        STUB_STATE="$dir/state" \
        LFT_BACKEND_PORT=9999 \
        LFT_WATCHDOG_SPOOL="$dir/spool.jsonl" \
        LFT_LE_OPTIONS_FILE="$dir/le-options" \
        LFT_LE_DHPARAMS_FILE="$dir/le-dhparams" \
        "$@" \
        bash "${WATCHDOG_UNDER_TEST:-$WATCHDOG}" > "$dir/out.log" 2>&1
    echo "$dir/spool.jsonl"
}

count_events() {
    [ -f "$1" ] || { echo 0; return; }
    grep -c '"event":"restart"' "$1" 2>/dev/null || echo 0
}

echo "Gateway watchdog spool cases:"
echo ""

# PREMISE. A healthy gateway records nothing. Without this every firing case below could be
# passing because the script records on every run regardless of what it found.
d="$WORK/healthy"; mkdir -p "$d"
spool=$(run_watchdog "$d" STUB_TUNNEL_UP=1 STUB_NGINX_UP=1)
if [ "$(count_events "$spool")" -eq 0 ]; then
    pass "PREMISE  a healthy gateway records nothing"
else
    fail "PREMISE  a healthy gateway recorded $(count_events "$spool") event(s) -- the spool would fill with non-events"
    cat "$spool"
fi

# FIRING. The issue exactly: the daemon is dead, the watchdog restarts it, it comes back, and
# before this nobody was ever told.
d="$WORK/tunnel-recovers"; mkdir -p "$d"
spool=$(run_watchdog "$d" STUB_TUNNEL_UP=0 STUB_TUNNEL_UP_AFTER=1 STUB_NGINX_UP=1)
if grep -q '"service":"lfr-tunneld","outcome":"recovered"' "$spool" 2>/dev/null; then
    pass "FIRING   a restarted-and-recovered daemon is recorded"
else
    fail "FIRING   no recovered event for lfr-tunneld"
    cat "$spool" 2>/dev/null
fi

# FIRING. The one that must not be lost: the box did not heal itself.
d="$WORK/tunnel-fails"; mkdir -p "$d"
spool=$(run_watchdog "$d" STUB_TUNNEL_UP=0 STUB_TUNNEL_UP_AFTER=0 STUB_NGINX_UP=1)
if grep -q '"service":"lfr-tunneld","outcome":"failed"' "$spool" 2>/dev/null; then
    pass "FIRING   a restart that did NOT recover is recorded as failed"
else
    fail "FIRING   a failed restart was not recorded as failed"
    cat "$spool" 2>/dev/null
fi

# BOUNDING. A failed restart must not be recorded as a recovery. The alert subject is built from
# this field, so getting it backwards tells the owner the opposite of the truth.
if grep -q '"service":"lfr-tunneld","outcome":"recovered"' "$WORK/tunnel-fails/spool.jsonl" 2>/dev/null; then
    fail "BOUNDING a failed restart was ALSO recorded as recovered"
else
    pass "BOUNDING a failed restart is not recorded as a recovery"
fi

# FIRING. Nginx dead and staying dead.
d="$WORK/nginx-fails"; mkdir -p "$d"
spool=$(run_watchdog "$d" STUB_TUNNEL_UP=1 STUB_NGINX_UP=0 STUB_NGINX_ACTIVE=0)
if grep -q '"service":"nginx","outcome":"failed"' "$spool" 2>/dev/null; then
    pass "FIRING   nginx failing to come back is recorded"
else
    fail "FIRING   nginx failure not recorded"
    cat "$spool" 2>/dev/null
fi

# BOUNDING. Nginx slow but alive is explicitly NOT a restart -- the script only acts when
# is-active says it is down. Recording it would report an outage that did not happen.
d="$WORK/nginx-slow"; mkdir -p "$d"
spool=$(run_watchdog "$d" STUB_TUNNEL_UP=1 STUB_NGINX_UP=0 STUB_NGINX_ACTIVE=1)
if [ "$(count_events "$spool")" -eq 0 ]; then
    pass "BOUNDING a slow-but-running nginx is not recorded as a restart"
else
    fail "BOUNDING a slow nginx was recorded as an outage"
    cat "$spool"
fi

# FIRING. The issue's second ask: nothing counted repeated restarts, and three in an hour is a
# different problem from one. The count has to survive the script exiting -- it is a oneshot
# timer unit, so it can only come from the spool itself.
d="$WORK/loop"; mkdir -p "$d"
for _ in 1 2 3; do
    rm -f "$d/state/tunnel_restarted"
    run_watchdog "$d" STUB_TUNNEL_UP=0 STUB_TUNNEL_UP_AFTER=1 STUB_NGINX_UP=1 > /dev/null
done
if grep -q '"restarts_last_hour":3' "$d/spool.jsonl" 2>/dev/null; then
    pass "FIRING   repeated restarts are counted across separate runs"
else
    fail "FIRING   restarts_last_hour never reached 3 across three runs"
    cat "$d/spool.jsonl" 2>/dev/null
fi

# BOUNDING. An old restart must not inflate the rate, or every gateway that ever restarted looks
# like it is looping forever.
d="$WORK/stale"; mkdir -p "$d/state"
printf '{"ts":"2020-01-01T00:00:00Z","event":"restart","service":"nginx","outcome":"recovered","restarts_last_hour":9}\n' > "$d/spool.jsonl"
: > "$d/le-options"; : > "$d/le-dhparams"
run_watchdog "$d" STUB_TUNNEL_UP=0 STUB_TUNNEL_UP_AFTER=1 STUB_NGINX_UP=1 > /dev/null
if tail -1 "$d/spool.jsonl" | grep -q '"restarts_last_hour":1'; then
    pass "BOUNDING an hours-old restart does not inflate the current rate"
else
    fail "BOUNDING a stale event counted toward restarts_last_hour"
    tail -1 "$d/spool.jsonl"
fi

# The daemon reads this file; the script writes it. Two defaults in two languages that must agree,
# asserted from the shell side as well as from Go, because a drift means the daemon reads a file
# nothing writes and reports no restarts forever -- which looks exactly like a healthy gateway.
if grep -q 'LFT_WATCHDOG_SPOOL:-/var/lib/lfr-tunnel/watchdog-events.jsonl' "$WATCHDOG"; then
    pass "the default spool path is the one pkg/server/watchdog_spool.go reads"
else
    fail "the watchdog's default spool path does not match the daemon's"
fi

# The Let's Encrypt paths are overridable ONLY so this file can run the script off a gateway. If
# production stopped looking at /etc/letsencrypt, this guard would be exercising a path the real
# gateway never takes.
if grep -q 'LFT_LE_OPTIONS_FILE:-/etc/letsencrypt/options-ssl-nginx.conf' "$WATCHDOG"; then
    pass "the test override still defaults to the real /etc/letsencrypt path"
else
    fail "the Let's Encrypt default changed -- this guard may be testing a path production does not take"
fi

# CONTROL. Everything above is green against a script that records. Break the recording and the
# firing cases must go red -- otherwise they are passing on the stubs, not on the script.
MUTANT="$WORK/mutant.sh"
sed 's/^        record_event "lfr-tunneld" "recovered"$/        : # control: recording removed/' "$WATCHDOG" > "$MUTANT"
if ! grep -q 'control: recording removed' "$MUTANT"; then
    fail "CONTROL  could not build the mutant -- the guard below proves nothing"
else
    d="$WORK/control"; mkdir -p "$d"
    spool=$(WATCHDOG_UNDER_TEST="$MUTANT" run_watchdog "$d" STUB_TUNNEL_UP=0 STUB_TUNNEL_UP_AFTER=1 STUB_NGINX_UP=1)
    if grep -q '"service":"lfr-tunneld","outcome":"recovered"' "$spool" 2>/dev/null; then
        fail "CONTROL  a script with the recording removed still produced the event"
    else
        pass "CONTROL  removing the recording makes the firing case stop firing"
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
