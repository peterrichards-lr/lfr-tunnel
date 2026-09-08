#!/usr/bin/env bash
# test-pre-push-range.sh — the pre-push hook must never report success having checked nothing (#1816)
#
# The hook decides what to check from a commit range. Two ways it got that wrong, and both ended
# the same way: "Nothing to check", exit 0, no vet, no tests, no React build.
#
#   1. The fallback `origin/master..HEAD` was installed without verifying origin/master resolves.
#      `git diff` then exits 128, its stderr was discarded, CHANGED came back empty.
#   2. On a force-push after a rebase, remote_oid and local_oid are divergent rather than
#      ancestor/descendant. A content-preserving rebase makes the two-dot diff between them empty
#      while the push carries real work.
#
# Both are the same defect -- the range is not the change being pushed -- so both are asserted
# here rather than only the reported one.
#
# The hook is driven the way git drives it: "<local ref> <local oid> <remote ref> <remote oid>"
# on stdin. Nothing is mocked, so a case cannot pass because the harness lied about the input.
#
# bash 3.2 compatible (macOS /bin/bash) -- no associative arrays, no mapfile. See AGENTS.md.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
HOOK="$REPO_ROOT/scripts/pre-push-hook.sh"

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
harness() { echo "  HARNESS: $1"; FAIL=$((FAIL + 1)); }

SANDBOX=$(mktemp -d)
cleanup() { rm -rf "$SANDBOX"; }
trap cleanup EXIT

echo "Testing pre-push-hook range selection..."

if [ ! -x "$HOOK" ] && [ ! -f "$HOOK" ]; then
    harness "hook not found at $HOOK -- nothing below was tested"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi

# A throwaway repo with a real origin, so the hook's git plumbing operates on genuine refs.
build_repo() {
    rm -rf "$SANDBOX/origin" "$SANDBOX/work"
    git init --quiet --bare "$SANDBOX/origin"
    git clone --quiet "$SANDBOX/origin" "$SANDBOX/work" 2>/dev/null
    cd "$SANDBOX/work" || return 1
    git config user.email t@example.com
    git config user.name Test
    git config commit.gpgsign false
    echo "package main" >main.go
    git add main.go
    git commit --quiet -m "base"
    git branch -M master
    git push --quiet origin master 2>/dev/null
    cd "$REPO_ROOT" || return 1
}

# run_hook <stdin line> ; echoes output, sets RC
run_hook() {
    cd "$SANDBOX/work" || return 1
    OUT=$(printf '%s\n' "$1" | /bin/bash "$HOOK" 2>&1)
    RC=$?
    cd "$REPO_ROOT" || return 1
}

# ---------------------------------------------------------------------------
# 1. The reported case: the target ref does not resolve.
#    A hook that cannot tell what changed must refuse, not pass.
# ---------------------------------------------------------------------------
if ! build_repo; then
    harness "could not build the fixture repo"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi
cd "$SANDBOX/work" || exit 1
echo "// changed" >>main.go
git commit --quiet -am "a real go change"
NEW=$(git rev-parse HEAD)
# Destroy the remote-tracking ref the fallback depends on.
git update-ref -d refs/remotes/origin/master 2>/dev/null
cd "$REPO_ROOT" || exit 1

run_hook "refs/heads/feature $NEW refs/heads/feature 0000000000000000000000000000000000000000"
if [ "$RC" -eq 0 ] && echo "$OUT" | grep -q "Nothing to check"; then
    fail "unresolvable target ref: hook exited 0 with 'Nothing to check' despite a real .go change.
        A green pre-push here is not evidence the checks ran.
        output: $(echo "$OUT" | head -2 | tr '\n' ' ')"
elif [ "$RC" -ne 0 ]; then
    pass "unresolvable target ref is refused rather than silently passed"
else
    pass "unresolvable target ref did not produce a vacuous 'Nothing to check'"
fi

# ---------------------------------------------------------------------------
# 2. Force-push after a rebase. remote_oid is the OLD head and is not an ancestor of the new one;
#    a content-preserving rebase leaves their trees identical, so remote_oid..local_oid is empty
#    while the branch genuinely adds work to master.
# ---------------------------------------------------------------------------
if ! build_repo; then
    harness "could not rebuild the fixture for case 2"
else
    cd "$SANDBOX/work" || exit 1
    git checkout --quiet -b feature
    echo "// feature work" >>main.go
    git commit --quiet -am "feature work"
    OLD=$(git rev-parse HEAD)
    # Rewrite it: same content, different commit -- exactly what a rebase produces.
    git commit --quiet --amend -m "feature work (rebased)"
    NEW=$(git rev-parse HEAD)

    EMPTY_RANGE=$(git diff --name-only "$OLD..$NEW" | wc -l | tr -d ' ')
    REAL_RANGE=$(git diff --name-only "origin/master..$NEW" | wc -l | tr -d ' ')
    cd "$REPO_ROOT" || exit 1

    if [ "$EMPTY_RANGE" != "0" ] || [ "$REAL_RANGE" = "0" ]; then
        harness "fixture is not the shape this case needs (old..new=$EMPTY_RANGE files, master..new=$REAL_RANGE) -- case 2 proves nothing"
    else
        run_hook "refs/heads/feature $NEW refs/heads/feature $OLD"
        if [ "$RC" -eq 0 ] && echo "$OUT" | grep -q "Nothing to check"; then
            fail "force-push after rebase: hook reported 'Nothing to check' and exited 0, but the
        branch adds $REAL_RANGE changed file(s) to master. The narrow remote..local range does not
        describe a push that rewrote history -- and a rebase is when the checks matter most,
        because the code is being replayed onto a base it was never compiled against."
        else
            pass "force-push after a rebase is measured against the target branch, not the old head"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# 3. The narrowness control. A hook that simply always checked everything would satisfy both
#    cases above while throwing away the speed-up the range exists for. A genuine fast-forward
#    with no relevant change must still short-circuit.
# ---------------------------------------------------------------------------
if ! build_repo; then
    harness "could not rebuild the fixture for case 3"
else
    cd "$SANDBOX/work" || exit 1
    git checkout --quiet -b docsonly
    BASE=$(git rev-parse HEAD)
    echo "# notes" >NOTES.md
    git add NOTES.md
    git commit --quiet -m "docs only"
    NEW=$(git rev-parse HEAD)
    cd "$REPO_ROOT" || exit 1

    run_hook "refs/heads/docsonly $NEW refs/heads/docsonly $BASE"
    if [ "$RC" -ne 0 ]; then
        fail "a fast-forward push of a docs-only change was refused (rc=$RC): $(echo "$OUT" | head -2 | tr '\n' ' ')"
    else
        pass "a genuine fast-forward is still evaluated on the narrow range (no false refusal)"
    fi
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
