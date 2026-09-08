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

# ------------------------------------------------------------------------------------------
# The remaining cases are about the script's view of the remote being CURRENT (#1814).
#
# `%(upstream:track)` is computed entirely from local remote-tracking refs. Every case above
# fetches in the fixture before running the script, so all of them pass against a script that
# never fetches at all -- which is what shipped, and what reported "nothing to tidy" on
# 2026-09-08 with five merged branches still sitting there.
#
# So these delete the branch INSIDE THE BARE ORIGIN and never fetch in the fixture. The local
# remote-tracking ref is deliberately left stale; only the script fetching can discover it.
# ------------------------------------------------------------------------------------------

# delete_at_origin removes a branch from the bare origin without going through the clone, so the
# clone's refs/remotes/origin/* is left untouched. `git push --delete` would update it locally
# and destroy the whole point of these fixtures.
delete_at_origin() {
    local repo="$1"
    shift
    for b in "$@"; do
        git -C "${repo}-origin" update-ref -d "refs/heads/$b"
    done
}

# 7. The report must name a branch whose remote was deleted with no local fetch. This is the
#    #1814 defect exactly: a clean report that is merely a stale one.
R=$(make_repo nofetch feat/merged)
delete_at_origin "$R" feat/merged
if ! git -C "$R" show-ref --verify --quiet refs/remotes/origin/feat/merged; then
    fail "HARNESS: refs/remotes/origin/feat/merged vanished without a fetch -- fixture is wrong,
        this case proves nothing about the script"
else
    out=$( cd "$R" && bash "$TARGET" 2>&1 )
    if printf '%s\n' "$out" | grep -q 'feat/merged'; then
        pass "a branch whose remote was deleted without a local fetch is still reported"
    else
        fail "reported on stale remote-tracking refs -- the script never fetched: $out"
    fi
fi

# 8. Protection is not a side effect of the old code path: with the deletion discovered by the
#    script's OWN fetch, `checksums` must still survive and the ordinary branch must still go.
R=$(make_repo protectednofetch feat/merged checksums)
delete_at_origin "$R" feat/merged checksums
( cd "$R" && bash "$TARGET" --delete >/dev/null 2>&1 )
if git -C "$R" show-ref --verify --quiet refs/heads/checksums; then
    pass "checksums survives --delete when the script's own fetch finds its remote gone"
else
    fail "checksums was DELETED -- this breaks checksum delivery to the portal silently"
fi
if git -C "$R" show-ref --verify --quiet refs/heads/master; then
    pass "master survives --delete when the script's own fetch finds branches gone"
else
    fail "master was deleted"
fi
if git -C "$R" show-ref --verify --quiet refs/heads/feat/merged; then
    fail "a branch deleted at the origin was not removed -- the script never fetched"
else
    pass "a branch deleted at the origin is removed after the script's own fetch"
fi

# 9. A branch held by a worktree cannot be deleted. Reporting that and exiting 0 is how
#    `make prune-branches` reports success having left branches behind.
R=$(make_repo worktreerc feat/held)
git -C "$R" push -q origin --delete feat/held
git -C "$R" fetch -q --prune origin
git -C "$R" worktree add -q "$WORK/held-rc" feat/held 2>/dev/null
if ! git -C "$R" show-ref --verify --quiet refs/heads/feat/held; then
    fail "HARNESS: the worktree fixture lost feat/held before the script ran"
else
    out=$( cd "$R" && bash "$TARGET" --delete 2>&1 )
    rc=$?
    held=0
    git -C "$R" show-ref --verify --quiet refs/heads/feat/held && held=1
    if [ "$held" -ne 1 ]; then
        fail "HARNESS: git deleted a worktree-held branch, so there was no failure to report"
    elif [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q "could not delete feat/held"; then
        pass "a branch a worktree holds makes --delete exit non-zero and names the branch"
    else
        fail "--delete left feat/held behind and exited $rc: $out"
    fi
fi

# 10. If the fetch cannot happen the script must refuse, not fall back to the stale refs it
#     already has. Reporting on yesterday's remote is the failure; doing it quietly is worse.
R=$(make_repo deadremote feat/x)
git -C "$R" push -q origin --delete feat/x
git -C "$R" fetch -q --prune origin
rm -rf "$R-origin"
out=$( cd "$R" && bash "$TARGET" 2>&1 )
rc=$?
if [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q "could not fetch"; then
    pass "an unreachable remote makes the script refuse rather than report stale refs"
else
    fail "unreachable remote: rc=$rc, expected a refusal naming the failed fetch: $out"
fi

# 11. Same decision, other cause: no remote at all means there is nothing to be current with.
R="$WORK/noremote"
rm -rf "$R"
git init -q "$R"
git -C "$R" config user.email t@example.com
git -C "$R" config user.name Test
git -C "$R" commit -q --allow-empty -m base
out=$( cd "$R" && bash "$TARGET" 2>&1 )
rc=$?
if [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q "no remote"; then
    pass "a repository with no remote is refused rather than reported clean"
else
    fail "no-remote repo: rc=$rc, expected a refusal: $out"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
