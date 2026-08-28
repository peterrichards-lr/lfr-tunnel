#!/usr/bin/env bash
# test-stale-branches.sh — tests for scripts/check-stale-branches.sh (#1528)
#
# The interesting behaviour is entirely in what the script REFUSES to do. A cleanup tool that
# deletes one branch too many is worse than no cleanup tool, and the branch it must never delete
# -- `checksums` -- breaks the portal's checksum delivery silently rather than loudly.
#
# Runs against throwaway repositories rather than this one, because the whole point is to watch
# it delete things.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/check-stale-branches.sh"

[ -x "$TARGET" ] || { echo "FATAL: $TARGET missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# make_repo builds a repo with an origin, then deletes some remote branches so their local
# counterparts show as [gone] -- the state GitHub leaves after merging and cleaning up.
make_repo() {
    local d="$WORK/$1"
    rm -rf "$d" "$d-origin"
    git init -q --bare "$d-origin"
    git init -q "$d"
    git -C "$d" config user.email t@example.com
    git -C "$d" config user.name Test
    git -C "$d" commit -q --allow-empty -m "base"
    git -C "$d" branch -M master
    git -C "$d" remote add origin "$d-origin"
    git -C "$d" push -q -u origin master

    for b in "${@:2}"; do
        git -C "$d" checkout -q -b "$b"
        git -C "$d" commit -q --allow-empty -m "work on $b"
        git -C "$d" push -q -u origin "$b"
    done
    git -C "$d" checkout -q master
    echo "$d"
}

echo "Testing scripts/check-stale-branches.sh"
echo ""

# 1. checksums must survive, even with its remote deleted. This is the one that matters: the
#    portal fetches client checksums from that orphan branch over raw.githubusercontent.com,
#    and losing it fails silently.
R=$(make_repo protected feat/merged checksums)
git -C "$R" push -q origin --delete feat/merged checksums
git -C "$R" fetch -q --prune origin
( cd "$R" && bash "$TARGET" --delete >/dev/null 2>&1 )
if git -C "$R" show-ref --verify --quiet refs/heads/checksums; then
    pass "checksums survives --delete even when its remote is gone"
else
    fail "checksums was DELETED -- this breaks checksum delivery to the portal silently"
fi
if git -C "$R" show-ref --verify --quiet refs/heads/feat/merged; then
    fail "a genuinely merged branch was not deleted, so the script does nothing useful"
else
    pass "a branch whose remote is gone is deleted"
fi

# 2. master is never deleted, for the obvious reason.
R=$(make_repo keepmaster feat/x)
git -C "$R" push -q origin --delete feat/x
git -C "$R" fetch -q --prune origin
( cd "$R" && bash "$TARGET" --delete >/dev/null 2>&1 )
if git -C "$R" show-ref --verify --quiet refs/heads/master; then
    pass "master survives"
else
    fail "master was deleted"
fi

# 3. A branch whose remote still EXISTS is untouched -- it may be an open PR, or unpushed work.
#    Deciding from the upstream being gone is the whole safety model, so this is the guard on it.
R=$(make_repo liveremote feat/open)
( cd "$R" && bash "$TARGET" --delete >/dev/null 2>&1 )
if git -C "$R" show-ref --verify --quiet refs/heads/feat/open; then
    pass "a branch with a live remote is left alone"
else
    fail "a branch with a live remote was deleted -- that could be an open PR"
fi

# 4. Report mode must not delete. Anyone running this to see the damage first must not cause it.
R=$(make_repo reportonly feat/y)
git -C "$R" push -q origin --delete feat/y
git -C "$R" fetch -q --prune origin
( cd "$R" && bash "$TARGET" >/dev/null 2>&1 )
if git -C "$R" show-ref --verify --quiet refs/heads/feat/y; then
    pass "reporting does not delete"
else
    fail "reporting deleted a branch"
fi

# 5. Over the threshold, reporting FAILS -- otherwise the pile grows unnoticed, which is exactly
#    how it reached 271.
R=$(make_repo threshold a b c d)
git -C "$R" push -q origin --delete a b c d
git -C "$R" fetch -q --prune origin
out=$( cd "$R" && LFT_STALE_BRANCH_THRESHOLD=2 bash "$TARGET" 2>&1 )
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "over the threshold"; then
    pass "reporting fails once the pile is over the threshold"
else
    fail "threshold not enforced: rc=$rc"
fi
out=$( cd "$R" && LFT_STALE_BRANCH_THRESHOLD=99 bash "$TARGET" 2>&1 )
if [ $? -eq 0 ]; then
    pass "under the threshold it reports without failing"
else
    fail "a small pile failed the check, which would make it noise"
fi

# 6. A branch held by a worktree cannot be deleted by git at all. The script must SAY so rather
#    than report success -- six branches survived a bulk delete this way and the worktrees were
#    invisible until the error named them.
R=$(make_repo worktree feat/held)
git -C "$R" push -q origin --delete feat/held
git -C "$R" fetch -q --prune origin
git -C "$R" worktree add -q "$WORK/held" feat/held 2>/dev/null
out=$( cd "$R" && bash "$TARGET" 2>&1 )
if printf '%s' "$out" | grep -q "worktree remove"; then
    pass "a worktree holding a branch is reported, with what to do about it"
else
    fail "worktrees not reported: $out"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
