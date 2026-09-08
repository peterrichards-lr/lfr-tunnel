#!/usr/bin/env bash
# check-stale-branches.sh — report (and optionally delete) branches whose work has landed (#1528)
#
# CONTRIBUTING.md has always said to delete feature and fix branches once they merge. Nothing ran
# it, so the local checkout reached 271 branches and 6 stale worktrees before anyone noticed --
# the same shape as #1498, where a ratchet wired into nothing drifted past its ceiling unseen.
#
# Three details make this easy to get wrong from memory, which is why it is a script and not a
# paragraph:
#
#   * `git branch -d` REFUSES on a squash-merged branch, because its commits are not literal
#     ancestors of master. The safe flag does not work here, and reaching for -D instead is
#     unsafe in general -- so this decides from the UPSTREAM being gone, which is what GitHub
#     leaves behind when it deletes a branch on merge.
#   * `git branch --merged` misses squash merges for the same reason. The obvious listing lies.
#   * A branch checked out in a WORKTREE cannot be deleted at all. Six survived a bulk delete
#     for this reason and the worktrees were invisible until the error named them.
#
# `%(upstream:track)` is computed from the LOCAL remote-tracking refs, so this script is only as
# truthful as its last fetch -- and it used not to fetch at all (#1814). On 2026-09-08 it reported
# "nothing to tidy" with five merged branches still present; a plain `git fetch --prune` made all
# five appear. A clean report was indistinguishable from a stale one, precisely when it mattered:
# right after a merge. So it now fetches first, in BOTH modes.
#
# Two decisions that follow, stated rather than left as side effects:
#
#   * The read-only report fetches too. `--prune` deletes refs/remotes/* only -- never a branch,
#     never a commit -- and a report computed from unpruned refs would reproduce the exact bug.
#     A "read-only" mode that answers from a stale cache is not safer, only quieter.
#   * If the fetch FAILS -- offline, no credentials, no remote -- this refuses and exits non-zero
#     rather than falling back to the refs it already has. A cleanup tool quietly reporting on
#     yesterday's remote is worse than one that says it cannot tell.
#
# PROTECTED, in code rather than in prose: `checksums` is an orphan branch the portal fetches
# client checksums from over raw.githubusercontent.com, because Release assets fail CORS. Deleting
# it breaks checksum delivery to the portal silently. CONTRIBUTING.md says so two clauses from the
# rule telling you to tidy branches up, which is exactly the arrangement that gets it deleted one
# day. Here it cannot be: the name is refused before anything is evaluated.
set -uo pipefail

# Never deleted, whatever else is true of them.
PROTECTED="master checksums"

# Report-only until this many have piled up. One stale branch is not worth a failing check;
# a hundred is how it got to 271.
THRESHOLD="${LFT_STALE_BRANCH_THRESHOLD:-15}"

DELETE=0
for arg in "$@"; do
    case "$arg" in
        --delete) DELETE=1 ;;
        -h|--help)
            echo "Usage: $0 [--delete]"
            echo "  Reports local branches whose remote is gone (i.e. merged and cleaned up on GitHub)."
            echo "  --delete removes them, after writing a manifest of branch + SHA to /tmp."
            echo "  Both modes run 'git fetch --prune' first -- it removes remote-tracking refs"
            echo "  only, never branches -- and refuse to report if that fetch fails."
            echo "  Never touches: $PROTECTED"
            exit 0 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

is_protected() {
    for p in $PROTECTED; do
        [ "$1" = "$p" ] && return 0
    done
    return 1
}

# Refresh the remote-tracking refs before deciding anything from them. `origin` by name where it
# exists, because that is what this repo has and what the branches track; otherwise the only
# remote, so a differently-named single remote still works.
REMOTE=$(git remote 2>/dev/null | grep -x origin || git remote 2>/dev/null | head -n 1)
if [ -z "$REMOTE" ]; then
    echo "check-stale-branches: this repository has no remote, so 'whose remote is gone' has no" >&2
    echo "  answer here. Refusing rather than reporting a clean tree from local refs alone." >&2
    exit 1
fi

FETCH_OUT=$(git fetch --prune --quiet "$REMOTE" 2>&1)
if [ $? -ne 0 ]; then
    echo "check-stale-branches: could not fetch from '$REMOTE':" >&2
    echo "$FETCH_OUT" | sed 's/^/  /' >&2
    echo "  Refusing to report. Staleness is the defect this check exists to catch (#1814), so" >&2
    echo "  answering from the remote-tracking refs already on disk would be the same bug." >&2
    exit 1
fi

# Worktrees first. A branch held by one cannot be deleted, so a cleanup that ignores them
# reports success while leaving them behind -- and the worktree is the thing to remove anyway.
WORKTREES=$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2}' | tail -n +2)
if [ -n "$WORKTREES" ]; then
    echo "Worktrees other than the main checkout:"
    echo "$WORKTREES" | while read -r w; do
        dirty=$(git -C "$w" status --short 2>/dev/null | wc -l | tr -d ' ')
        branch=$(git -C "$w" branch --show-current 2>/dev/null)
        if [ "$dirty" != "0" ]; then
            echo "  $w [$branch] -- $dirty uncommitted change(s), leave it alone"
        else
            echo "  $w [$branch] -- clean; 'git worktree remove' it before deleting the branch"
        fi
    done
    echo
fi

STALE=$(git for-each-ref --format='%(refname:short)|%(upstream:track)' refs/heads \
    | awk -F'|' '$2=="[gone]" {print $1}')

COUNT=0
for b in $STALE; do
    is_protected "$b" && continue
    COUNT=$((COUNT + 1))
done

if [ "$COUNT" -eq 0 ]; then
    echo "check-stale-branches: no branches whose remote has been deleted. Nothing to tidy."
    exit 0
fi

echo "$COUNT local branch(es) whose remote is gone -- merged on GitHub and cleaned up there:"
for b in $STALE; do
    is_protected "$b" && continue
    echo "  $b ($(git rev-parse --short "$b"))"
done
echo

if [ "$DELETE" -ne 1 ]; then
    echo "Delete them with: $0 --delete   (or 'make prune-branches')"
    if [ "$COUNT" -gt "$THRESHOLD" ]; then
        echo
        echo "FAILED: $COUNT is over the threshold of $THRESHOLD. CONTRIBUTING.md asks for these to"
        echo "go as they merge; left alone they reached 271 once (#1528)."
        exit 1
    fi
    exit 0
fi

# A manifest before anything is removed. `git branch -D` on a squash-merged branch is safe in
# the sense that the work is in master, but the SHA is the only cheap way back to a branch that
# turns out to have held something else.
MANIFEST="${TMPDIR:-/tmp}/lfr-deleted-branches-$(git rev-parse --short HEAD).txt"
: > "$MANIFEST"

DELETED=0
UNDELETED=0
for b in $STALE; do
    if is_protected "$b"; then
        echo "  REFUSED  $b is protected and will never be deleted by this script"
        continue
    fi
    echo "$b $(git rev-parse --short "$b")" >> "$MANIFEST"
    if ERR=$(git branch -D "$b" 2>&1); then
        DELETED=$((DELETED + 1))
    else
        # Almost always a worktree holding it; the report above says which.
        echo "  could not delete $b -- $(printf '%s\n' "$ERR" | tail -1)"
        UNDELETED=$((UNDELETED + 1))
    fi
done

echo
echo "Deleted $DELETED branch(es). Manifest: $MANIFEST"

# A branch that could not be removed is work still outstanding, so this must not exit 0. It did,
# and `make prune-branches` reported success with branches left behind (#1814) -- the same
# "green while work remains" shape as the missing fetch, in the delete path.
if [ "$UNDELETED" -ne 0 ]; then
    echo
    echo "FAILED: $UNDELETED branch(es) could not be deleted -- named above. A worktree holding a"
    echo "branch is the usual cause; 'git worktree remove' it and run this again."
    exit 1
fi
exit 0
