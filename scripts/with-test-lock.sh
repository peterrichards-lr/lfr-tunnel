#!/bin/sh
# with-test-lock.sh -- serialise anything that uses the single EDR-whitelisted test binary.
#
# `make test` builds and runs $LFT_TEST_DIR/lfr-tunnel, a FIXED absolute path. It has to be
# fixed: that literal path is the SentinelOne exclusion, not the directory tree around it, and
# an unsigned test binary built anywhere else gets quarantined. Assuming otherwise cost a full
# environment reinstall on 2026-08-13 (see .agents/skills/edr-constraints/SKILL.md).
#
# The consequence is that every worktree and every clone resolves to the SAME file, so two
# concurrent `make test` runs delete each other's binary mid-run (#1714). The loser fails with:
#
#     /bin/sh: /private/tmp/lfr-tunnel: No such file or directory
#     make: *** [test] Error 1
#
# which reads as a test failure and is not one. That is the dangerous part: it aborted a release
# with "Tests failed. Please fix before pushing." when nothing was wrong, and two separate agents
# recorded it as a flaky test. Anything that reads it as "my change broke this" chases a ghost;
# anything that reads it as "flaky, retry" learns to ignore real failures.
#
# So: serialise rather than relocate. Concurrent invocations queue; the whitelisted path is
# untouched. A per-run path would be the obvious fix and is exactly the one that is unsafe here.
#
# mkdir, not flock: flock is util-linux and is absent on macOS, which is the platform this
# actually has to work on. mkdir is atomic on every POSIX filesystem and needs no extra tool.
set -eu

LFT_TEST_DIR="${LFT_TEST_DIR:-$([ -d /private/tmp ] && echo /private/tmp || echo /tmp)}"
LOCK_DIR="$LFT_TEST_DIR/.lfr-tunnel-test.lock"

# How long to queue before giving up. The full suite takes a couple of minutes, so this allows
# for a few runs ahead in the queue without hanging a CI job indefinitely.
LOCK_TIMEOUT="${LFT_TEST_LOCK_TIMEOUT:-900}"

# A lock whose owner died is not a lock. Broken only when the recorded pid is gone, rather than
# on age alone -- a long full-suite run must never have its lock stolen by a shorter one.
STALE_AFTER="${LFT_TEST_LOCK_STALE_AFTER:-30}"

if [ "$#" -eq 0 ]; then
    echo "usage: with-test-lock.sh <command> [args...]" >&2
    exit 2
fi

held=""
cleanup() {
    # Only ever remove a lock this process owns. Without the guard, a run that timed out waiting
    # would delete the lock belonging to the run it was waiting for.
    if [ -n "$held" ]; then
        rm -f "$LOCK_DIR/pid" 2>/dev/null || true
        rmdir "$LOCK_DIR" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM HUP

mkdir -p "$LFT_TEST_DIR"

waited=0
announced=0
while :; do
    if mkdir "$LOCK_DIR" 2>/dev/null; then
        held=1
        echo $$ > "$LOCK_DIR/pid" 2>/dev/null || true
        break
    fi

    owner=$(cat "$LOCK_DIR/pid" 2>/dev/null || echo "")
    if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
        # Owner is gone. Wait out STALE_AFTER first so this cannot race a process that has just
        # created the directory and not yet written its pid.
        if [ "$waited" -ge "$STALE_AFTER" ]; then
            echo "make test: breaking a stale lock left by pid $owner, which is no longer running." >&2
            rm -f "$LOCK_DIR/pid" 2>/dev/null || true
            rmdir "$LOCK_DIR" 2>/dev/null || true
            continue
        fi
    fi

    if [ "$announced" -eq 0 ]; then
        echo "make test: another test run holds $LOCK_DIR${owner:+ (pid $owner)}; waiting." >&2
        echo "  The test binary path is shared by every worktree and cannot be made per-run" >&2
        echo "  without leaving the EDR whitelist, so runs queue instead of colliding (#1714)." >&2
        announced=1
    fi

    if [ "$waited" -ge "$LOCK_TIMEOUT" ]; then
        echo "make test: timed out after ${LOCK_TIMEOUT}s waiting for $LOCK_DIR." >&2
        echo "  If no test run is active, remove it: rmdir $LOCK_DIR" >&2
        exit 1
    fi

    sleep 1
    waited=$((waited + 1))
done

"$@"
