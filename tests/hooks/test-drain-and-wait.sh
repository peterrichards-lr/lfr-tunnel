#!/usr/bin/env bash
# test-drain-and-wait.sh — tests for scripts/common/drain-and-wait.sh (#1455)
#
# drain-and-wait.sh is now the ONE copy of the drain sequence, called by both `deploy` and
# restore-with-maintenance.sh. Its whole contract is "do the right thing and never be the
# reason an operation fails", which means the interesting cases are all failure cases: no
# config, no endpoint, a client that will not move. Those are exactly what a happy-path
# check would miss, and getting one of them wrong turns a routine restore into an outage
# or, worse, blocks one.
#
# curl is stubbed rather than a real gateway stood up: what is under test is the script's
# decision-making, and pkg/server/drain_test.go already covers the endpoint itself.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/common/drain-and-wait.sh"

[ -x "$TARGET" ] || { echo "FATAL: $TARGET is missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# make_stub writes a fake curl whose behaviour is driven by files in $WORK, and puts it first
# on PATH. It records every invocation so a test can assert what was actually sent.
make_stub() {
    mkdir -p "$WORK/bin"
    cat > "$WORK/bin/curl" <<'STUB'
#!/usr/bin/env bash
# Record the full invocation for assertions.
echo "$*" >> "$WORK_DIR/curl.log"
# POSTs are announcements or withdrawals; a GET is a status poll.
case "$*" in
    *"-X POST"*) exit "${POST_EXIT:-0}" ;;
esac
# Status poll: emit the next queued leases value, falling back to the last one.
if [ -s "$WORK_DIR/leases.queue" ]; then
    head -1 "$WORK_DIR/leases.queue"
    sed -i.bak '1d' "$WORK_DIR/leases.queue" 2>/dev/null || true
    exit 0
fi
[ -n "${LEASES_DEFAULT:-}" ] && echo "{\"local_leases\":${LEASES_DEFAULT}}"
exit "${GET_EXIT:-0}"
STUB
    chmod +x "$WORK/bin/curl"
    # sleep is stubbed too, so a 90s wait does not cost 90 seconds of test time.
    printf '#!/usr/bin/env bash\nexit 0\n' > "$WORK/bin/sleep"
    chmod +x "$WORK/bin/sleep"
}

reset() {
    rm -f "$WORK/curl.log" "$WORK/leases.queue"
    : > "$WORK/curl.log"
}

write_config() {
    printf 'http_bind_addr: "%s"\n' "$1" > "$WORK/server-config.yaml"
}

make_stub

echo "Testing scripts/common/drain-and-wait.sh"
echo ""

# 1. The happy path still has to work, and has to actually stop once drained.
reset
write_config "127.0.0.1:8080"
printf '{"local_leases":2}\n{"local_leases":0}\n' > "$WORK/leases.queue"
if WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" announce 45 90 "test" >/dev/null 2>&1; then
    if grep -q '"seconds": 45' "$WORK/curl.log"; then
        pass "announce posts the requested window and returns 0 once drained"
    else
        fail "announce did not post the requested window: $(cat "$WORK/curl.log")"
    fi
else
    fail "announce returned non-zero on the happy path"
fi

# 2. A wildcard bind is not dialable. 0.0.0.0 means every interface, so the request has to be
#    aimed at loopback with the port kept -- getting this wrong means never draining anything,
#    silently, on every default deployment.
reset
write_config "0.0.0.0:9999"
printf '{"local_leases":0}\n' > "$WORK/leases.queue"
WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" announce 10 10 "test" >/dev/null 2>&1
if grep -q "http://127.0.0.1:9999/api/local/drain" "$WORK/curl.log"; then
    pass "a 0.0.0.0 bind is dialled on loopback with the port preserved"
else
    fail "wildcard bind was not rewritten to loopback: $(cat "$WORK/curl.log")"
fi

# 3. No readable config must not stop a deploy or a restore.
reset
rm -f "$WORK/server-config.yaml"
if WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" announce 45 90 "test" >/dev/null 2>&1; then
    pass "an unreadable config skips the drain and still exits 0"
else
    fail "an unreadable config made the script fail, which would block a restore"
fi

# 4. An older gateway with no drain endpoint must behave as it did before this existed.
reset
write_config "127.0.0.1:8080"
if WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" POST_EXIT=22 LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" announce 45 90 "test" >/dev/null 2>&1; then
    pass "a gateway with no drain endpoint is tolerated and still exits 0"
else
    fail "a missing drain endpoint made the script fail"
fi

# 5. A client that never moves must be reported, not waited on forever, and must not fail the
#    operation -- refusing to restart because one client will not leave would be a new way for
#    a deploy to break.
reset
write_config "127.0.0.1:8080"
out=$(WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LEASES_DEFAULT=3 LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" announce 5 10 "test" 2>&1)
rc=$?
# Matches the WARNING specifically, not "still attached" -- the polling loop prints that on
# every iteration, so the loose version passed with the warning deleted entirely.
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "WARNING: proceeding with"; then
    pass "a client that will not move is warned about, and does not fail the operation"
else
    fail "timeout case: rc=$rc out=$out"
fi

# 6. clear must withdraw the announcement, or clients keep migrating away from a node that is
#    staying up.
reset
write_config "127.0.0.1:8080"
WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" clear >/dev/null 2>&1
if grep -q '"seconds": 0' "$WORK/curl.log"; then
    pass "clear withdraws the announcement"
else
    fail "clear did not post seconds:0: $(cat "$WORK/curl.log")"
fi

# 7. An unknown verb must not silently do nothing -- a typo in a maintenance script would
#    otherwise look like a successful drain.
reset
write_config "127.0.0.1:8080"
if WORK_DIR="$WORK" PATH="$WORK/bin:$PATH" LFT_CONFIG="$WORK/server-config.yaml" \
    "$TARGET" anounce 45 90 "test" >/dev/null 2>&1; then
    fail "a misspelled verb exited 0, which would hide a broken maintenance script"
else
    pass "an unknown verb fails loudly rather than pretending to drain"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
