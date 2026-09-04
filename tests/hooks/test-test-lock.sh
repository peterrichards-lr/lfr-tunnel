#!/bin/sh
# test-test-lock.sh -- tests for the serialisation around the shared test binary (#1714).
#
# `make test` builds and runs $LFT_TEST_DIR/lfr-tunnel, a fixed absolute path that IS the
# SentinelOne exclusion. Every worktree and clone resolves to that same file, so concurrent runs
# used to delete each other's binary and report it as a test failure. These checks hold the
# serialisation in place, because the failure it prevents is indistinguishable from a flaky test
# and trains people to ignore real ones.
set -eu

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
LOCK_SCRIPT="$REPO_ROOT/scripts/with-test-lock.sh"
PASS=0
FAIL=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# --- the wiring, which is what stops the lock being quietly dropped later ------------------

if grep -qE '^test:.*compile-check' "$REPO_ROOT/Makefile"; then
    pass "test still depends on compile-check"
else
    fail "test no longer depends on compile-check -- an untested package can stop compiling unnoticed"
fi

if grep -qE '^\s*@\./scripts/with-test-lock\.sh' "$REPO_ROOT/Makefile"; then
    pass "make test runs through with-test-lock.sh"
else
    fail "make test no longer takes the lock -- concurrent runs will delete each other's binary (#1714)"
fi

# The whitelisted path is the whole reason serialising is necessary rather than using a
# per-run filename. If TEST_BINARY ever becomes unique per run, the EDR exclusion has to have
# been changed and verified first -- see .agents/skills/edr-constraints/SKILL.md.
if grep -qE '^TEST_BINARY := \$\(LFT_TEST_DIR\)/lfr-tunnel' "$REPO_ROOT/Makefile"; then
    pass "the test binary still uses the exact whitelisted path"
else
    fail "TEST_BINARY moved off \$(LFT_TEST_DIR)/lfr-tunnel -- an unsigned binary outside the EDR whitelist gets quarantined"
fi

# A missing binary must fail loudly. It used to be an `if [ -f ]` that skipped the package in
# silence, so a run could report success having tested nothing.
if grep -q 'no binary was produced' "$REPO_ROOT/Makefile"; then
    pass "a missing test binary fails instead of silently skipping the package"
else
    fail "a missing test binary is silently skipped again -- a package can go untested and still exit 0"
fi

# --- the lock itself ------------------------------------------------------------------------

export LFT_TEST_DIR="$WORK"

# Bounded deliberately. With the default 900s timeout, a regression that never releases the
# lock makes this suite HANG for fifteen minutes instead of failing -- which is worse than the
# bug, because a hung run gets killed by a CI timeout and reported as something else entirely.
# Long enough for the 2s holder below, short enough to fail fast when release is broken.
export LFT_TEST_LOCK_TIMEOUT=30

# Serialisation: two concurrent holders must not overlap. Each appends on entry and exit, so
# interleaved windows are visible in the ordering.
( "$LOCK_SCRIPT" sh -c 'echo A-in >> "$0"/log; sleep 2; echo A-out >> "$0"/log' "$WORK" ) &
sleep 1
( "$LOCK_SCRIPT" sh -c 'echo B-in >> "$0"/log; echo B-out >> "$0"/log' "$WORK" ) &
wait

if [ "$(tr '\n' ' ' < "$WORK/log")" = "A-in A-out B-in B-out " ]; then
    pass "a second run waits rather than overlapping"
else
    fail "runs overlapped: $(tr '\n' ' ' < "$WORK/log")"
fi

# The lock must be released on exit, or the next run blocks for the whole timeout.
if [ ! -d "$WORK/.lfr-tunnel-test.lock" ]; then
    pass "the lock is released when the command finishes"
else
    fail "the lock survived the command -- every later run would queue behind a dead holder"
fi

# A non-zero exit from the wrapped command must propagate; a wrapper that swallows it would
# make every failing test suite look green.
if "$LOCK_SCRIPT" sh -c 'exit 3' >/dev/null 2>&1; then
    fail "a failing command exited 0 through the wrapper -- failures would be invisible"
else
    rc=$?
    if [ "$rc" -eq 3 ]; then
        pass "the wrapped command's exit status is preserved"
    else
        fail "expected exit 3 from the wrapped command, got $rc"
    fi
fi

# A lock whose owner is gone must be broken, or one crashed run wedges the repo until someone
# removes a directory by hand.
mkdir -p "$WORK/.lfr-tunnel-test.lock"
echo 999999 > "$WORK/.lfr-tunnel-test.lock/pid"   # a pid that is not running
if LFT_TEST_LOCK_STALE_AFTER=1 LFT_TEST_LOCK_TIMEOUT=20 \
        "$LOCK_SCRIPT" sh -c 'exit 0' >/dev/null 2>&1; then
    pass "a stale lock left by a dead process is broken"
else
    fail "a stale lock was not broken -- a crashed run would wedge every later one"
fi
rm -rf "$WORK/.lfr-tunnel-test.lock"

# Waiting must be bounded, and must say what to do rather than hanging silently.
mkdir -p "$WORK/.lfr-tunnel-test.lock"
echo $$ > "$WORK/.lfr-tunnel-test.lock/pid"        # a live pid: this test itself
out=$(LFT_TEST_LOCK_TIMEOUT=2 LFT_TEST_LOCK_STALE_AFTER=999 \
        "$LOCK_SCRIPT" sh -c 'exit 0' 2>&1 || true)
if echo "$out" | grep -q "timed out"; then
    pass "waiting for a live holder times out instead of hanging"
else
    fail "no timeout message when the lock was held by a live process: $out"
fi
if echo "$out" | grep -q "rmdir"; then
    pass "the timeout message says how to clear a lock left behind"
else
    fail "the timeout message does not tell the operator how to recover"
fi
rm -rf "$WORK/.lfr-tunnel-test.lock"

printf '\n'
if [ "$FAIL" -eq 0 ]; then
    printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
else
    printf '\033[31m%d of %d checks failed\033[0m\n' "$FAIL" "$((PASS + FAIL))"
    exit 1
fi
