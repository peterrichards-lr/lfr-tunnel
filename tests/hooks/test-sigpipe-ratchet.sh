#!/usr/bin/env bash
#
# test-sigpipe-ratchet.sh -- scripts/check-sigpipe-ratchet.sh counts the right thing (#2290).
#
# The ratchet's whole value is its number. A counter that silently counts too much invites someone
# to raise the ceiling for a defect that is not there; one that counts too little reports a clean
# tree over a growing fault. Both look identical to a passing run, so each boundary below is
# asserted rather than assumed.
#
# Labelling follows tests/hooks/test-gate-scope-boundaries.sh:
#   FIRING   -- fails against the state before the assertion existed; evidence the gate reads it
#   BOUNDING -- passes before and after, on purpose; it pins a deliberate edge so crossing it
#               turns this suite red rather than quietly changing what the ceiling means

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

GATE="scripts/check-sigpipe-ratchet.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -x "$GATE" ]; then
    fail "PREMISE: $GATE is missing or not executable"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# Fixtures must not land inside the repo: the gate globs tests/hooks/ and scripts/ from its working
# directory, so a fixture planted in the real tree would be counted by the real run. mktemp rather
# than a sibling of the repo -- this test runs no Docker, so the #1377 "/private/tmp is invisible to
# Docker on macOS" reasoning that other hook tests carry does not apply here, and writing into
# /Volumes/SanDisk/repos/ litters a directory holding other checkouts and agent worktrees.
FIXTURE_BASE="$(mktemp -d "${TMPDIR:-/tmp}/lft-sigpipe-fixture.XXXXXX")"
cleanup() { rm -rf "$FIXTURE_BASE"; }
trap cleanup EXIT

# run_in <label> -- builds a tree containing the real gate plus whatever the caller planted, runs
# it there, and prints "<exit> <count>".
run_in() {
    local dir="$1" out rc
    out="$( cd "$dir" && LFT_SIGPIPE_MIN_FILES=1 ./scripts/check-sigpipe-ratchet.sh 2>&1 )"
    rc=$?
    printf '%s %s' "$rc" "$(printf '%s' "$out" | sed -n 's/.*pipelines: \([0-9]*\) .*/\1/p')"
}

new_tree() {
    local dir="$FIXTURE_BASE/$1"
    mkdir -p "$dir/scripts" "$dir/tests/hooks"
    cp "$GATE" "$dir/scripts/"
    printf '%s' "$dir"
}

echo "-- PREMISE: the gate counts the real tree, and a scan of nothing is a failure"

real="$(./scripts/check-sigpipe-ratchet.sh 2>&1)"
real_count="$(printf '%s' "$real" | sed -n 's/.*pipelines: \([0-9]*\) .*/\1/p')"
if [ -n "$real_count" ] && [ "$real_count" -gt 0 ]; then
    pass "PREMISE: the real tree counts $real_count pipeline(s), so the scan is reading something"
else
    fail "PREMISE: the real tree counted nothing -- every assertion below would be vacuous"
fi

# The #1779 shape: run it where its globs match nothing. It must refuse, not report zero.
empty="$(new_tree empty)"
rm -f "$empty/scripts/"*.sh 2>/dev/null
cp "$GATE" "$empty/scripts/"
empty_out="$( cd "$empty" && ./scripts/check-sigpipe-ratchet.sh 2>&1 )"
empty_rc=$?
if [ "$empty_rc" -eq 0 ]; then
    fail "PREMISE: the gate PASSED on a near-empty tree -- a scan of nothing must not be a clean bill"
elif grep -q 'expected at least' <<<"$empty_out"; then
    pass "PREMISE: a corpus below the floor is refused, and says why"
else
    # Non-zero is shared by a missing file (127), a syntax error (2) and a failed cp. Only the
    # floor's own message proves the floor is what refused.
    fail "PREMISE: the gate failed on a near-empty tree but not with the floor message -- exit $empty_rc"
fi

echo ""
echo "-- FIRING: one more pipeline must break the ceiling"

over="$(new_tree over)"
cat > "$over/tests/hooks/test-planted.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
if printf 'a\n' | grep -q a; then echo yes; fi
FIXEOF
# Planted in BOTH halves of the corpus, one each. With fixtures only in tests/hooks/, deleting
# `scripts/*.sh` from the gate's glob left this whole suite 7/7 green while 14 real pipelines
# silently dropped out -- and the gate then invited ratcheting the ceiling down to a number that
# would never see them again. Coverage of a two-glob scan needs a fixture per glob.
cat > "$over/scripts/planted-gate.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
if printf 'b\n' | grep -q b; then echo yes; fi
FIXEOF
read -r rc count <<<"$(LFT_SIGPIPE_CEILING=0 run_in "$over")"
if [ "$rc" -ne 0 ] && [ -n "$count" ] && [ "$count" -eq 2 ]; then
    pass "FIRING: a pipeline planted in each corpus half is counted (2) and breaks a ceiling of 0"
elif [ "$rc" -ne 0 ] && [ "${count:-0}" -eq 1 ]; then
    fail "FIRING: only ONE of the two planted pipelines was counted -- half the corpus is unscanned"
else
    fail "FIRING: the planted pipelines did not break the ceiling (exit $rc, count ${count:-<none>})"
fi

echo ""
echo "-- BOUNDING: the deliberate edges of what gets counted"

# 1. No pipefail, no fault. Counting these would make the ceiling a measure of shell style.
nopf="$(new_tree nopipefail)"
cat > "$nopf/tests/hooks/test-no-pipefail.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -u
if printf 'a\n' | grep -q a; then echo yes; fi
FIXEOF
read -r _ count <<<"$(run_in "$nopf")"
if [ -n "$count" ] && [ "$count" -eq 0 ]; then
    pass "BOUNDING: a pipeline in a file without pipefail is not counted"
else
    fail "BOUNDING: count=${count:-<none>} in a file with no pipefail -- the ceiling now measures style, not this defect"
fi

# 2. Prose describing the idiom must not inflate the count. This is #2032's trap: a real removal
#    cancelled by a comment about it, so the total never moves and the fix looks ineffective.
prose="$(new_tree prose)"
cat > "$prose/tests/hooks/test-prose-only.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
# Never write: producer | grep -q PATTERN
#   because  x | grep -q y  can report the opposite of the truth.
echo ok
FIXEOF
read -r _ count <<<"$(run_in "$prose")"
if [ -n "$count" ] && [ "$count" -eq 0 ]; then
    pass "BOUNDING: the idiom named in comments is not counted"
else
    fail "BOUNDING: count=${count:-<none>} from comment text -- describing the defect would raise the ceiling"
fi

# 3. The gate must not count itself. It has to spell the pattern to search for it, and its own
#    test has to spell it to plant fixtures -- the self-match case (section 5c rule 6), which has
#    bitten this repo three times.
self="$(new_tree selfmatch)"
cp "$GATE" "$self/scripts/check-sigpipe-ratchet.sh"
cp "${BASH_SOURCE[0]}" "$self/tests/hooks/test-sigpipe-ratchet.sh"
read -r _ count <<<"$(run_in "$self")"
if [ -n "$count" ] && [ "$count" -eq 0 ]; then
    pass "BOUNDING: the gate and its own test are excluded from the count"
else
    fail "BOUNDING: count=${count:-<none>} from the gate or its test -- it is measuring itself"
fi

# 4. The safe idiom must NOT be counted, or the gate punishes the fix it recommends.
safe="$(new_tree safe)"
cat > "$safe/tests/hooks/test-safe-idiom.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
out="$(printf 'a\n')"
if grep -q a <<<"$out"; then echo yes; fi
FIXEOF
read -r _ count <<<"$(run_in "$safe")"
if [ -n "$count" ] && [ "$count" -eq 0 ]; then
    pass "BOUNDING: the here-string idiom the gate recommends is not counted"
else
    fail "BOUNDING: count=${count:-<none>} for the recommended fix -- the gate would block its own advice"
fi

# 5. The corpus is deliberately one level deep. scripts/common/ holds 15 files that set pipefail,
#    including the unattended gateway scripts, and they are NOT counted. Pinning that means
#    widening the scan later is a decision someone makes, not an accident -- and if someone does
#    widen it, this goes red and the ceiling has to be re-derived rather than silently jumping.
deep="$(new_tree deepdir)"
mkdir -p "$deep/scripts/common"
cat > "$deep/scripts/common/planted-deep.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
if printf 'c\n' | grep -q c; then echo yes; fi
FIXEOF
read -r _ count <<<"$(run_in "$deep")"
if [ -n "$count" ] && [ "$count" -eq 0 ]; then
    pass "BOUNDING: scripts/common/ is outside the corpus and is not counted"
else
    fail "BOUNDING: count=${count:-<none>} -- the scan has been widened past one directory level; re-derive the ceiling"
fi

echo ""
echo "-- FIRING: the branch that the ratchet exists to reach"

# A ratchet has three outcomes and this suite covered two: over the ceiling (the FIRING case
# above) and below the floor (the PREMISE case). The third -- the count having FALLEN -- is the
# one the whole design is for, and it was the uncovered one.
#
# It did not work. `$SELF` at the end of the gate was never assigned, so under `set -u` the
# branch aborted with "SELF: unbound variable" and exit 1: the gate failed the build on a tree
# strictly BETTER than its ceiling required, in CI and pre-push alike (#2312). It stayed hidden
# for two days because the count sat exactly AT the ceiling the entire time, so nobody reached
# the branch -- section 5d, an assertion covering one branch of a subject that has three.
#
# Asserts the advice NAMES the gate, not merely that the exit code is 0. "Exited zero" is shared
# with every other way this could pass (5c rule 1), and the defect was inside the message.
under="$(new_tree undercount)"
under_out="$( cd "$under" && LFT_SIGPIPE_MIN_FILES=1 LFT_SIGPIPE_CEILING=99 ./scripts/check-sigpipe-ratchet.sh 2>&1 )"
under_rc=$?
if [ "$under_rc" -ne 0 ]; then
    fail "FIRING: a count BELOW the ceiling exited $under_rc -- the gate fails a tree better than it asks for: $under_out"
elif grep -q 'check-sigpipe-ratchet.sh' <<<"$under_out" && grep -q 'has fallen' <<<"$under_out"; then
    pass "FIRING: a count below the ceiling succeeds and names the file whose ceiling to lower"
else
    fail "FIRING: the fallen-count advice did not name the gate, so nobody can act on it: $under_out"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
