#!/usr/bin/env bash
# test-nested-worktree-scope.sh — every repo-tree walk must skip a nested git worktree (#1815)
#
# The class: a gate that walks the repository tree cannot, by default, tell a checkout from a
# second copy of itself. `git worktree add` inside the repo -- which is where agent worktrees
# land, under .claude/worktrees -- puts a complete duplicate of every tracked file under the
# root the gate is walking.
#
# What that costs, measured on 2026-09-08: `make test` failed on a clean master with three agent
# worktrees present, because TestEveryClientTokenAssignmentDeclaresItsProvenance reported the
# same three legitimate pkg/config/config.go lines once per worktree. The pre-push hook runs
# `make test`, so every push from the main checkout was rejected, naming a file that was not
# wrong.
#
# The fixture is a real `git worktree add`, not a mock directory, because the thing being relied
# on is git's own on-disk shape -- a worktree root carries `.git` as a FILE holding a `gitdir:`
# pointer, where a repository root carries it as a directory.
#
# WHAT THIS FILE IS AND IS NOT, corrected on 2026-09-30. It used to open by saying it asserted
# "the PROPERTY rather than the three gates that had the bug", on the reasoning that "the next
# tree-walking gate anyone adds will have the same hole, and a per-gate assertion would not see
# it". That reasoning is right and the file did not follow it: every case below IS a per-gate
# assertion, and nothing enumerates the gates that walk the tree. Two that do were missed for
# three weeks --  check-edr-safety.sh and check-test-home-isolation.sh, cases 5 and 6, added
# under #2310 after the first of them reported three offences inside this suite's own fixture
# and blocked an unrelated commit.
#
# So: this is a LIST, and a list has to be maintained. Add a case when you add a gate that walks
# the repository tree. Nothing here will tell you that you forgot -- that limit is the honest
# statement of what the file does, and github-workflow SKILL 5d is the general shape of it.
#
# The premise checks below ask git for that shape rather than testing "$REPO_ROOT/.git" (#1839).
# The first version tested the path directly, which is true only from the main checkout: run
# from a worktree -- which is how every agent on this repo works -- "$REPO_ROOT/.git" is a FILE,
# the harness branch fired, and `make test-hooks` could not pass at all. The suite was defeated
# by the very property it exists to assert, which is worth keeping in view: a premise check that
# assumes one vantage point is a scope blind spot like any other (#1779).
#
# bash 3.2 compatible (macOS /bin/bash) -- no associative arrays, no mapfile. See AGENTS.md.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT" || exit 1

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
harness() { echo "  HARNESS: $1"; FAIL=$((FAIL + 1)); }

# A name that cannot collide with anything real, and is obviously disposable if a crash leaves
# it behind.
WT_DIR=".lft-nested-worktree-test-$$"
WT_PATH="$REPO_ROOT/$WT_DIR"

cleanup() {
    if [ -e "$WT_PATH" ]; then
        git worktree remove --force "$WT_PATH" >/dev/null 2>&1 || rm -rf "$WT_PATH"
    fi
    git worktree prune >/dev/null 2>&1
}
trap cleanup EXIT

echo "Testing nested-worktree scope..."

# ---------------------------------------------------------------------------
# Fixture. A detached worktree at HEAD, so it is a full second copy of the tree.
# ---------------------------------------------------------------------------
if ! git worktree add --detach "$WT_PATH" HEAD >/dev/null 2>&1; then
    harness "could not create the fixture worktree at $WT_DIR -- nothing below was tested"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi

# The premise every gate's fix rests on. If git ever stopped writing .git as a file here, the
# fixes would silently stop matching and every case below would pass for the wrong reason.
if [ -f "$WT_PATH/.git" ]; then
    pass "the fixture worktree root carries .git as a FILE (the property the fixes test for)"
else
    harness "fixture worktree has no .git FILE -- the premise behind every fix below is wrong"
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi

# The other half of the premise: a real repository directory is a DIRECTORY, so a repository
# root is not mistaken for a worktree and skipped.
#
# Asked of `git rev-parse --git-common-dir` rather than of "$REPO_ROOT/.git" (#1839). REPO_ROOT
# is wherever this suite is run from, and that is not necessarily the main checkout -- every
# agent on this repo works from a `git worktree add` under .claude/worktrees, where
# "$REPO_ROOT/.git" is a FILE. The direct test therefore fired its own harness branch and failed
# the suite from the vantage point the suite exists to describe: the property it asserts is
# exactly what broke its own premise guard.
#
# --git-common-dir resolves the one real repository directory from either vantage point, so the
# premise can be stated truthfully without weakening it. It is still a premise check: if git
# ever stopped keeping that directory, every case below would start passing for the wrong
# reason and this says so first.
GIT_COMMON_DIR=$(git rev-parse --git-common-dir 2>/dev/null || true)
case "$GIT_COMMON_DIR" in
    "") ;;
    /*) ;;
    *) GIT_COMMON_DIR="$REPO_ROOT/$GIT_COMMON_DIR" ;;
esac
if [ -n "$GIT_COMMON_DIR" ] && [ -d "$GIT_COMMON_DIR" ]; then
    pass "the real repository directory is a DIRECTORY (so a repository root is never skipped)"
else
    harness "git rev-parse --git-common-dir gave '$GIT_COMMON_DIR', which is not a directory -- cannot distinguish a repository root from a worktree"
fi

# And the two must actually be distinguishable, which is the property the fixes rely on rather
# than either half of it alone. Inside a worktree, --git-dir is that worktree's private
# administrative directory under <common>/worktrees/<name>; at a repository root the two are
# the same path. Asserted against the FIXTURE, which this suite created, so the answer does not
# depend on where the suite was invoked from.
WT_GIT_DIR=$(cd "$WT_PATH" && git rev-parse --git-dir 2>/dev/null || true)
WT_COMMON_DIR=$(cd "$WT_PATH" && git rev-parse --git-common-dir 2>/dev/null || true)
if [ -n "$WT_GIT_DIR" ] && [ "$WT_GIT_DIR" != "$WT_COMMON_DIR" ]; then
    pass "a worktree's --git-dir differs from its --git-common-dir (root and worktree are distinguishable)"
else
    harness "the fixture worktree reports --git-dir '$WT_GIT_DIR' and --git-common-dir '$WT_COMMON_DIR' -- git no longer distinguishes the two, and every case below would pass for the wrong reason"
fi

# ---------------------------------------------------------------------------
# 1. check_docs_review.py (full-repo mode) must not report a file inside the worktree.
# ---------------------------------------------------------------------------
DOCS_OUT=$(python3 scripts/check_docs_review.py --dir . 2>&1)
if echo "$DOCS_OUT" | grep -q "$WT_DIR"; then
    fail "check_docs_review.py reported files inside the nested worktree:
$(echo "$DOCS_OUT" | grep "$WT_DIR" | head -3)"
else
    pass "check_docs_review.py reports nothing from inside the nested worktree"
fi

# ---------------------------------------------------------------------------
# 2. append_timestamps.py WRITES, so the cost of descending is worse than a false report: it
#    stamps footers into duplicate files. Assert it leaves the worktree copy untouched.
# ---------------------------------------------------------------------------
PROBE_MD="$WT_PATH/nested-worktree-probe.md"
printf '# probe\n\nNo footer here on purpose.\n' >"$PROBE_MD"
PROBE_BEFORE=$(cksum <"$PROBE_MD")

if ! python3 scripts/append_timestamps.py >/dev/null 2>&1; then
    harness "append_timestamps.py exited non-zero -- case 2 proves nothing"
else
    PROBE_AFTER=$(cksum <"$PROBE_MD")
    if [ "$PROBE_BEFORE" = "$PROBE_AFTER" ]; then
        pass "append_timestamps.py did not write into the nested worktree"
    else
        fail "append_timestamps.py WROTE a footer into a file inside the nested worktree -- a duplicate of a file it does not own"
    fi
fi

# ---------------------------------------------------------------------------
# 3. The Go gate. Scoped to the one test rather than the suite: this asserts the walk's scope,
#    not pkg/config's behaviour, and `make test` is the only EDR-safe way to run it (never a
#    bare `go test` -- see .agents/skills/edr-constraints/SKILL.md).
# ---------------------------------------------------------------------------
GO_OUT=$(make test PKG=./pkg/config/... \
    TEST_FLAGS='-test.run TestEveryClientTokenAssignmentDeclaresItsProvenance' 2>&1)
GO_RC=$?
if [ "$GO_RC" -ne 0 ] && ! echo "$GO_OUT" | grep -q "TestEveryClientTokenAssignmentDeclaresItsProvenance"; then
    harness "the Go gate did not run (rc=$GO_RC); case 3 proves nothing:
$(echo "$GO_OUT" | tail -3)"
elif echo "$GO_OUT" | grep -q "$WT_DIR"; then
    fail "the Go provenance gate reported offenders inside the nested worktree:
$(echo "$GO_OUT" | grep "$WT_DIR" | head -3)"
elif [ "$GO_RC" -ne 0 ]; then
    fail "the Go provenance gate failed with a nested worktree present (rc=$GO_RC):
$(echo "$GO_OUT" | grep -E '^\s+\S+\.go:|FAIL' | head -5)"
else
    pass "the Go provenance gate passes with a nested worktree present"
fi

# ---------------------------------------------------------------------------
# 4. The scope must be NARROW as well as correct: a gate that skipped the whole tree would pass
#    every case above. Assert each gate still reads the real tree.
# ---------------------------------------------------------------------------
if echo "$DOCS_OUT" | grep -qE "Scanning [1-9][0-9]* markdown files"; then
    pass "check_docs_review.py still scanned the real tree (not an empty scan)"
else
    fail "check_docs_review.py scanned nothing -- a skip that wide would satisfy case 1 vacuously:
$(echo "$DOCS_OUT" | head -3)"
fi

# ---------------------------------------------------------------------------
# 5. check-edr-safety.sh. A grep-based gate, so it cannot apply the `.git`-is-a-FILE test during
#    a walk it does not control; it asks `git worktree list` for the same fact up front instead.
#
#    This case is the one that was missing. On 2026-09-30 the fixture above was live when an
#    unrelated commit ran the pre-commit hook, and this gate reported three `go run` lines inside
#    .lft-nested-worktree-test-$$ and refused the commit -- the exact failure mode the header
#    describes, from a gate this suite did not name (#2310).
#
#    The toolchain word is assembled rather than written: this file is a `*.sh` inside the corpus
#    the gate scans, so spelling the pattern would plant a permanent violation in the real tree.
# ---------------------------------------------------------------------------
NW_TOOLCHAIN="go"
printf '#!/usr/bin/env bash\n%s run ./cmd/example\n' "$NW_TOOLCHAIN" \
    >"$WT_PATH/scripts/nested-worktree-probe.sh"
EDR_OUT=$(./scripts/check-edr-safety.sh 2>&1)
EDR_RC=$?
if echo "$EDR_OUT" | grep -q "$WT_DIR"; then
    fail "check-edr-safety.sh reported a toolchain invocation inside the nested worktree:
$(echo "$EDR_OUT" | grep "$WT_DIR" | head -3)"
elif [ "$EDR_RC" -ne 0 ]; then
    fail "check-edr-safety.sh failed with a nested worktree present (rc=$EDR_RC):
$(echo "$EDR_OUT" | head -5)"
else
    # NARROWNESS, the case 4 control applied here: a gate that had stopped reading the tree
    # would satisfy the above just as well. The same line one directory up must still be caught.
    printf '#!/usr/bin/env bash\n%s run ./cmd/example\n' "$NW_TOOLCHAIN" \
        >"$REPO_ROOT/scripts/.nested-worktree-control.sh"
    CTRL_OUT=$(./scripts/check-edr-safety.sh 2>&1)
    rm -f "$REPO_ROOT/scripts/.nested-worktree-control.sh"
    if echo "$CTRL_OUT" | grep -q 'nested-worktree-control.sh'; then
        pass "check-edr-safety.sh skips the nested worktree and still reads the real tree"
    else
        fail "check-edr-safety.sh missed the control violation in the REAL tree, so its silence on the worktree proves nothing:
$(echo "$CTRL_OUT" | head -5)"
    fi
fi

# ---------------------------------------------------------------------------
# 6. check-test-home-isolation.sh. Same mechanism, and the sharper consequence: its anti-vacuity
#    floor (#1779) counts packages, so duplicates do not merely inflate the number -- they can
#    satisfy the floor on their own in a tree with no source in it at all.
#
#    Asserted with a planted OFFENDER rather than a count: a package inside the worktree that
#    reaches the home directory and has no testmain_home_test.go. Before the fix the gate names
#    it and exits 1; after, it does not see it. A count assertion would have to encode 4, which
#    changes whenever a package legitimately joins the class.
# ---------------------------------------------------------------------------
mkdir -p "$WT_PATH/pkg/nestedprobe"
printf 'package nestedprobe\n\nfunc probe() { _, _ = os.UserHomeDir() }\n' \
    >"$WT_PATH/pkg/nestedprobe/probe_test.go"
HOME_OUT=$(./scripts/check-test-home-isolation.sh 2>&1)
HOME_RC=$?
if echo "$HOME_OUT" | grep -q 'nestedprobe'; then
    fail "check-test-home-isolation.sh reported a package inside the nested worktree:
$(echo "$HOME_OUT" | grep 'nestedprobe' | head -3)"
elif [ "$HOME_RC" -ne 0 ]; then
    fail "check-test-home-isolation.sh failed with a nested worktree present (rc=$HOME_RC):
$(echo "$HOME_OUT" | head -5)"
else
    # NARROWNESS: the identical package in the real tree must still be reported, or this gate
    # could have been skipping everything.
    mkdir -p "$REPO_ROOT/pkg/.nestedprobectl"
    printf 'package nestedprobectl\n\nfunc probe() { _, _ = os.UserHomeDir() }\n' \
        >"$REPO_ROOT/pkg/.nestedprobectl/probe_test.go"
    CTRL_OUT=$(./scripts/check-test-home-isolation.sh 2>&1)
    rm -rf "$REPO_ROOT/pkg/.nestedprobectl"
    if echo "$CTRL_OUT" | grep -q 'nestedprobectl'; then
        pass "check-test-home-isolation.sh skips the nested worktree and still reads the real tree"
    else
        fail "check-test-home-isolation.sh missed the control package in the REAL tree, so its silence on the worktree proves nothing:
$(echo "$CTRL_OUT" | head -5)"
    fi
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
