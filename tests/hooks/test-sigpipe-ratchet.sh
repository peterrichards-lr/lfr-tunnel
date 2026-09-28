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

# Fixtures live beside the repo, not inside it: the gate globs tests/hooks/ and scripts/ from its
# working directory, so a fixture planted in the real tree would be counted by the real run. And
# /private/tmp is not visible to Docker on macOS (#1377), so one predictable place beside the repo
# avoids relearning that per test.
FIXTURE_BASE="$(dirname "$REPO_ROOT")/.lft-sigpipe-fixture-$$"
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
if ( cd "$empty" && ./scripts/check-sigpipe-ratchet.sh >/dev/null 2>&1 ); then
    fail "PREMISE: the gate PASSED on a near-empty tree -- a scan of nothing must not be a clean bill"
else
    pass "PREMISE: a corpus below the floor is refused rather than counted as zero"
fi

echo ""
echo "-- FIRING: one more pipeline must break the ceiling"

over="$(new_tree over)"
cat > "$over/tests/hooks/test-planted.sh" <<'FIXEOF'
#!/usr/bin/env bash
set -uo pipefail
if printf 'a\n' | grep -q a; then echo yes; fi
FIXEOF
read -r rc count <<<"$(LFT_SIGPIPE_CEILING=0 run_in "$over")"
if [ "$rc" -ne 0 ] && [ "${count:-0}" -ge 1 ]; then
    pass "FIRING: a planted pipeline is counted ($count) and breaks a ceiling of 0"
else
    fail "FIRING: a planted pipeline did not break the ceiling (exit $rc, count ${count:-?})"
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
if [ "${count:-0}" -eq 0 ]; then
    pass "BOUNDING: a pipeline in a file without pipefail is not counted"
else
    fail "BOUNDING: counted ${count} in a file with no pipefail -- the ceiling now measures style, not this defect"
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
if [ "${count:-0}" -eq 0 ]; then
    pass "BOUNDING: the idiom named in comments is not counted"
else
    fail "BOUNDING: counted ${count} from comment text -- describing the defect would raise the ceiling"
fi

# 3. The gate must not count itself. It has to spell the pattern to search for it, and its own
#    test has to spell it to plant fixtures -- the self-match case (section 5c rule 6), which has
#    bitten this repo three times.
self="$(new_tree selfmatch)"
cp "$GATE" "$self/scripts/check-sigpipe-ratchet.sh"
cp "${BASH_SOURCE[0]}" "$self/tests/hooks/test-sigpipe-ratchet.sh"
read -r _ count <<<"$(run_in "$self")"
if [ "${count:-0}" -eq 0 ]; then
    pass "BOUNDING: the gate and its own test are excluded from the count"
else
    fail "BOUNDING: counted ${count} from the gate or its test -- it is measuring itself"
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
if [ "${count:-0}" -eq 0 ]; then
    pass "BOUNDING: the here-string idiom the gate recommends is not counted"
else
    fail "BOUNDING: counted ${count} for the recommended fix -- the gate would block its own advice"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
