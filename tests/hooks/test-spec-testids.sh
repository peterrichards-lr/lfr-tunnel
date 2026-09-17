#!/usr/bin/env bash
# test-spec-testids.sh -- assert check-spec-testids.cjs actually fires (#1974).
#
# The defect it guards: an E2E assertion keyed on a data-testid that exists nowhere.
#
#     await expect(panel.locator('[data-testid="geo-attribution"]')).toHaveCount(0);
#
# That is the right property to assert -- but toHaveCount(0) is satisfied just as well by the
# testid having been renamed out of ui/src as by the element genuinely being absent, so the
# assertion goes permanently, silently green on the day the thing it watches breaks. It cannot
# be shown to fail from inside the suite either: rendering the geo credit needs a geo-IP
# database the E2E stack deliberately does not ship.
#
# Every case plants a fixture and demands a specific verdict, including the vacuity cases where
# one side parses to nothing and a naive implementation reports agreement (#1779).
#
# Cases are labelled, as tests/hooks/test-gate-scope-boundaries.sh labels its own:
#   PREMISE  -- without it every FIRING case below could be passing because the gate refuses
#               everything rather than because it detects the defect.
#   FIRING   -- fails against the state before the gate existed.
#   BOUNDING -- passes on purpose, pinning a deliberate edge so that widening the gate past it
#               turns this suite red and the widening becomes somebody's decision.
#   CONTROL  -- the gate broken deliberately; a green never shown to go red is not evidence.
#
# bash 3.2 compatible (macOS /bin/bash): no associative arrays, no mapfile. See AGENTS.md.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-spec-testids.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/spec-testid.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-spec-testids.cjs is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# plant <dir> <spec-body> <v2-markup> <v1-markup>
#
# The gate is production; only the corpus is a fixture. Copied in from the working tree rather
# than read from HEAD, so a change being made is what gets tested.
plant() {
    local dir="$1" spec="$2" v2="$3" v1="$4"
    mkdir -p "$dir/scripts" "$dir/ui/src" "$dir/pkg/server" "$dir/tests/e2e/ui/tests"
    cp "$GATE" "$dir/scripts/"
    printf '%s\n' "$spec" > "$dir/tests/e2e/ui/tests/example.spec.ts"
    printf '%s\n' "$v2" > "$dir/ui/src/App.tsx"
    printf '%s\n' "$v1" > "$dir/pkg/server/dashboard.html"
}

# run_case <label> <expected-exit> <spec-body> <v2-markup> <v1-markup>
run_case() {
    local label="$1" want="$2" spec="$3" v2="$4" v1="$5"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    plant "$dir" "$spec" "$v2" "$v1"

    ( cd "$dir" && node scripts/check-spec-testids.cjs >/dev/null 2>&1 )
    local got=$?
    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-spec-testids.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Spec testid gate cases:"
echo ""

# PREMISE. A reference the markup genuinely satisfies must pass, or every FIRING case below
# would be satisfied by a gate that simply refuses everything.
run_case "a testid the markup renders" 0 \
    "await expect(page.locator('[data-testid=\"geo-attribution\"]')).toHaveCount(0);" \
    '<span data-testid="geo-attribution">MaxMind</span>' \
    '<div id="v1"></div>'

# FIRING. The #1974 defect exactly: the spec still names it, the markup no longer renders it,
# and the assertion it backs is an absence assertion that cannot notice.
run_case "a testid no markup defines" 1 \
    "await expect(page.locator('[data-testid=\"geo-attribution\"]')).toHaveCount(0);" \
    '<span data-testid="geo-credit">MaxMind</span>' \
    '<div id="v1"></div>'

# FIRING. Playwright's own accessor form must be read too. Twelve of the fifteen references in
# this repo are getByTestId(), so a gate that only understood the CSS-attribute form would have
# covered the reported instance and none of its siblings (github-workflow SKILL 5b).
run_case "getByTestId names a testid no markup defines" 1 \
    "const gate = page.getByTestId('policy-consent-gate');" \
    '<div data-testid="something-else" />' \
    '<div id="v1"></div>'

# FIRING. A comment naming the testid must not rescue it. Documenting that markup lost an
# attribute is not restoring it, and check-print-selectors.cjs shipped with exactly this hole.
run_case "a markup comment naming the testid does not count as markup" 1 \
    "await expect(page.locator('[data-testid=\"geo-attribution\"]')).toHaveCount(0);" \
    '{/* data-testid="geo-attribution" was removed in #1938 */}
<span data-testid="geo-credit">MaxMind</span>' \
    '<div id="v1"></div>'

# FIRING. The same on the V1 side, where comments are HTML rather than JSX.
run_case "an HTML comment naming the testid does not count as markup" 1 \
    "await expect(page.locator('[data-testid=\"iron-curtain\"]')).toHaveCount(0);" \
    '<div data-testid="something-else" />' \
    '<!-- data-testid="iron-curtain" used to live here --><div id="v1"></div>'

# BOUNDING. A commented-out assertion is not a live reference. Failing the build over one would
# be a false red, and the fix someone would reach for is deleting the gate.
run_case "a testid named only in a commented-out spec line is not a reference" 0 \
    "// await expect(page.locator('[data-testid=\"removed-thing\"]')).toHaveCount(0);
await expect(page.getByTestId('still-here')).toBeVisible();" \
    '<div data-testid="still-here" />' \
    '<div id="v1"></div>'

# BOUNDING. One pool, both arms: a testid rendered only by Portal V1 satisfies a spec that names
# it, because this gate checks EXISTENCE and does not model which arm a spec drives. If it ever
# learns to, this case goes red and that is the signal to confirm the narrowing is wanted.
run_case "a V1-only testid satisfies a reference" 0 \
    "await expect(page.getByTestId('iron-curtain')).toHaveCount(0);" \
    '<div data-testid="something-else" />' \
    '<div data-testid="iron-curtain"></div>'

# BOUNDING. A URL in the markup must not swallow the rest of its line. The line-comment stripper
# would otherwise delete a real data-testid sitting after an href and invent a failure -- the
# harness failing instead of the subject (github-workflow SKILL 5c.5).
run_case "a URL on the same line as a testid is not read as a comment" 0 \
    "await expect(page.getByTestId('credit-link')).toBeVisible();" \
    '<a href="https://www.maxmind.com" data-testid="credit-link">MaxMind</a>' \
    '<div id="v1"></div>'

# BOUNDING. A dynamic reference has no literal to compare, so it must be skipped rather than
# reported dead. Reporting it would make the gate unusable the first time someone writes one.
run_case "a dynamic getByTestId is skipped, not reported dead" 0 \
    'await expect(page.getByTestId(`row-${id}`)).toBeVisible();
await expect(page.getByTestId("still-here")).toBeVisible();' \
    '<div data-testid="still-here" />' \
    '<div id="v1"></div>'

# CONTROL / anti-vacuity. Markup with no testid in it at all. Both sides "agree" only in the
# sense that one of them is empty, and a naive implementation passes here forever -- which is
# the state the whole gate is meant to make impossible (#1779).
run_case "markup with no testids at all must NOT be reported as agreement" 1 \
    "await expect(page.getByTestId('anything')).toBeVisible();" \
    '<div className="sidebar" />' \
    '<div id="v1"></div>'

# CONTROL / anti-vacuity, the other side. A spec suite that names no testid means the reference
# matcher has broken or the specs moved; either way there is nothing to verify and reporting
# success would be a scan of nothing.
run_case "a spec suite naming no testid must NOT pass vacuously" 1 \
    "await expect(page.getByRole('button')).toBeVisible();" \
    '<div data-testid="still-here" />' \
    '<div id="v1"></div>'

# CONTROL. The floor must be reachable in place, or the two cases above could only ever be
# exercised by contriving a fixture -- and a floor nobody can trip is a floor nobody has tested.
# The MESSAGE is the assertion: "exited non-zero" is shared by a syntax error and a missing
# node, and against the real tree this gate already has plenty to say (github-workflow SKILL 5c.1).
#
# Captured rather than piped: this file runs under `set -o pipefail`, so a pipeline ending in a
# successful grep still reports the gate's own non-zero exit, and the assertion would read a
# correct refusal as a missing one. That is how the first run of this case failed.
FLOOR_OUT=$( (cd "$REPO_ROOT" && LFT_TESTID_MIN_REFS=999999 node scripts/check-spec-testids.cjs 2>&1) || true )
if printf '%s' "$FLOOR_OUT" | grep -q 'below the floor of 999999'; then
    pass "CONTROL  the gate honours LFT_TESTID_MIN_REFS, so its vacuity floor can be exercised"
else
    fail "CONTROL  the gate ignores LFT_TESTID_MIN_REFS, so its vacuity floor cannot be exercised"
fi

# CONTROL. The counterpart to every case above: the gate must still pass against the real tree.
# A gate that fails everywhere is not a guard, it is an outage.
if (cd "$REPO_ROOT" && node scripts/check-spec-testids.cjs >/dev/null 2>&1); then
    pass "CONTROL  the gate still passes against the real specs and markup"
else
    fail "CONTROL  the gate fails on a clean checkout -- a spec names a testid nothing renders, or the gate is wrong"
    ( cd "$REPO_ROOT" && node scripts/check-spec-testids.cjs 2>&1 | sed 's/^/        /' )
fi

# CONTROL, the one that matters most. Break the real thing the reported defect describes --
# rename data-testid="geo-attribution" in ui/src -- and require the gate to go red NAMING it.
# The reference lives in geo_distribution_parity.spec.ts as a toHaveCount(0) assertion, which
# is exactly the shape that cannot notice its own testid disappearing.
MUT="$WORK/mutant"
mkdir -p "$MUT/scripts" "$MUT/ui" "$MUT/pkg" "$MUT/tests/e2e/ui"
cp -R "$REPO_ROOT/ui/src" "$MUT/ui/src" 2>/dev/null
cp -R "$REPO_ROOT/pkg/server" "$MUT/pkg/server" 2>/dev/null
cp -R "$REPO_ROOT/tests/e2e/ui/tests" "$MUT/tests/e2e/ui/tests" 2>/dev/null
if [ ! -f "$MUT/ui/src/pages/AdminAnalytics.tsx" ]; then
    fail "CONTROL  could not stage a copy of the real tree -- the live-mutation case did not run"
else
    cp "$GATE" "$MUT/scripts/"
    # sed -i is not portable between GNU and BSD; write through a temp file instead.
    sed 's/data-testid="geo-attribution"/data-testid="geo-credit"/' \
        "$MUT/ui/src/pages/AdminAnalytics.tsx" > "$MUT/.mutated" &&
        mv "$MUT/.mutated" "$MUT/ui/src/pages/AdminAnalytics.tsx"
    if grep -q 'data-testid="geo-attribution"' "$MUT/ui/src/pages/AdminAnalytics.tsx"; then
        fail "CONTROL  the mutation did not apply -- the testid is still there, so the case proved nothing"
    else
        OUT=$( (cd "$MUT" && node scripts/check-spec-testids.cjs 2>&1) ) && RC=0 || RC=$?
        if [ "$RC" -eq 0 ]; then
            fail "CONTROL  renaming data-testid=\"geo-attribution\" in ui/src left the gate GREEN -- this is #1974, unguarded"
        elif printf '%s' "$OUT" | grep -q 'geo-attribution'; then
            pass "CONTROL  renaming the real testid turns the gate red, naming geo-attribution"
        else
            fail "CONTROL  the gate went red for some other reason than the renamed testid:
        $(printf '%s' "$OUT" | head -3 | tr '\n' ' ')"
        fi
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
