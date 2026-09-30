#!/usr/bin/env bash
# check-test-home-isolation.sh -- a test that resolves the user's home must not read the real one.
#
# TestLoadClientConfig_TokenFile and TestInsecurePermissionWarning asserted the client's token
# resolution ladder by calling LoadClientConfig(""), which reads ~/.lfr-tunnel/config.yaml. On a
# machine with one, an inline auth_token: short-circuits the ladder and both fail; in CI, whose
# home is empty, both pass. The tests were right and the environment was not theirs (#1798).
#
# It fails in the worse direction: green where it proves least. So the fix is structural -- a
# TestMain per package rather than a t.Setenv per test -- and this is what stops a fifth package
# joining the class without one.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

# Calls that resolve a path through the home directory. UserHomeDir is the root of all of them;
# the other two are the pkg/config wrappers a test is far likelier to call directly.
HOME_REACHING='os\.UserHomeDir\(\)|ResolveDefaultConfigPath\(\)|(Load|Save)ClientConfig\(""\)'

# Anti-vacuity floor (#1779): a scan that examined nothing must not report success. Four
# packages qualify today; the floor sits below that so an honest removal does not break the
# build, but a glob that matches nothing does. Overridable so it can be exercised in place.
#
# The floor only means anything while the scan below reads THIS tree and not a copy of it, which
# is why `worktrees` is excluded there. Agent worktrees are full checkouts living inside the repo,
# and with nine of them present this script reported "All 40 packages" over the same four: the
# four real ones plus thirty-six copies. That is not merely noisy -- it hands the floor a way to
# be satisfied with no source in the tree at all. Reproduced on 2026-09-30 in a directory holding
# `scripts/` and `.claude/worktrees/` and nothing else, no pkg/ and no cmd/ anywhere:
#
#     $ ./scripts/check-test-home-isolation.sh
#     ✅ All 5 packages whose tests resolve the user's home isolate it.   → exit 0
#
# Which is #1779's shape exactly, arriving through a directory this gate's author had no reason
# to think about. Case 25 of tests/hooks/test-gate-scope-boundaries.sh is that reproduction.
MIN_PACKAGES="${LFT_HOME_ISOLATION_MIN_PACKAGES:-2}"

# What the scan must not descend into. The NAME covers the convention agent checkouts follow;
# the listing below covers the mechanism, and they are not redundant -- see the same pair, with
# its reasoning, in scripts/check-edr-safety.sh. A nested worktree need not be under
# `.claude/worktrees/`: tests/hooks/test-nested-worktree-scope.sh puts one at the repository root
# for the duration of its run.
#
# Seeded rather than declared empty: under `set -u`, bash 3.2 treats "${arr[@]}" on an empty
# array as an unbound variable, and this runs from a pre-push hook on macOS.
SCAN_EXCLUDES=(--exclude-dir=worktrees)
nested_worktree_names() {
    local root line path
    root="$(git rev-parse --show-toplevel 2>/dev/null)" || return 0
    [ -n "$root" ] || return 0
    git worktree list --porcelain 2>/dev/null | while IFS= read -r line; do
        case "$line" in
            worktree\ *) path="${line#worktree }" ;;
            *) continue ;;
        esac
        [ "$path" = "$root" ] && continue
        case "$path" in "$root"/*) ;; *) continue ;; esac
        printf '%s\n' "${path##*/}"
    done
}

# A name that collides with a tracked directory is REFUSED, not excluded -- `--exclude-dir` takes
# a name rather than a path, so a worktree called `config` would take the real pkg/config out of
# the scan. Same reasoning, at more length, in scripts/check-edr-safety.sh; validated here in the
# main shell for the same reason, since `exit` inside a process substitution ends the subshell
# and nothing else.
WORKTREE_NAMES="$(nested_worktree_names)"
# `|| true` is load-bearing, exactly as in corpus_size() below. Outside a git repository
# `git ls-files` exits 128; the pipeline's status is sort's, so `pipefail` promotes the failure
# and `set -e` kills the gate with no message. Both gates ran fine anywhere before this block
# existed, and this suite's own fixtures are plain directories -- they caught it immediately.
TRACKED_NAMES="$(git ls-files 2>/dev/null | tr '/' '\n' | sort -u || true)"
while IFS= read -r wt_name; do
    [ -n "$wt_name" ] || continue
    if grep -qx -- "$wt_name" <<<"$TRACKED_NAMES"; then
        echo "GATE SCOPE ERROR: a git worktree is named '$wt_name', which is also a tracked path"
        echo "component. Excluding it would also skip the real ./$wt_name, and this gate would"
        echo "then report on fewer packages than it claims. Rename or remove that worktree."
        echo "See #2310."
        exit 1
    fi
    SCAN_EXCLUDES+=("--exclude-dir=$wt_name")
done <<<"$WORKTREE_NAMES"

# bash 3.2 on macOS has no mapfile, and this runs from a pre-push hook there.
packages=$(
    grep -rlE "$HOME_REACHING" --include='*_test.go' "${SCAN_EXCLUDES[@]}" . 2>/dev/null |
        grep -v '/testmain_home_test\.go$' |
        xargs -n1 dirname 2>/dev/null |
        sort -u || true
)

count=$(printf '%s' "$packages" | grep -c . || true)

if [ "$count" -lt "$MIN_PACKAGES" ]; then
    echo "❌ Only $count package(s) matched the home-reaching patterns, expected at least $MIN_PACKAGES."
    echo "   Either the patterns have gone stale or this ran somewhere with no source in it."
    echo "   A check that examined nothing must not report success."
    exit 1
fi

failed=0
for pkg in $packages; do
    main="$pkg/testmain_home_test.go"
    if [ ! -f "$main" ]; then
        echo "❌ $pkg has tests that resolve the user's home but no $main"
        failed=1
        continue
    fi
    if ! grep -q 'testhome.Isolate()' "$main"; then
        echo "❌ $main exists but never calls testhome.Isolate()"
        failed=1
    fi
    if ! grep -q 'func TestPackageTestsCannotReachTheRealHome' "$main"; then
        echo "❌ $main has no TestPackageTestsCannotReachTheRealHome -- without it, deleting the"
        echo "   TestMain would be silent in CI, where the real home is empty either way"
        failed=1
    fi
done

if [ "$failed" -ne 0 ]; then
    echo
    echo "Copy internal/testhome's TestMain into the package(s) named above; pkg/config has one."
    exit 1
fi

echo "✅ All $count packages whose tests resolve the user's home isolate it."
