#!/usr/bin/env bash
#
# test-e2e-accessible-names.sh -- scripts/check-e2e-accessible-names.py detects the collision it
# names, and its exemption list cannot go stale quietly (#2311).
#
# The gate's value is entirely in two behaviours, and a passing run distinguishes neither of them
# from a scan that read nothing:
#
#   FIRING   a name that is a strict prefix of a different shipped string is reported
#   BOUNDING an ALLOWED entry that no longer collides is reported as stale, so the list can only
#            shrink -- SKILL section 5b rule 5, "a deferral must be a ratchet, not an exclusion"
#
# Fixtures are built in a temp tree rather than in the repo: the gate globs tests/e2e/ui/tests/
# from its working directory, so a planted spec in the real tree would be read by the real run.
#
# bash 3.2 compatible (macOS /bin/bash). See AGENTS.md.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

GATE="scripts/check-e2e-accessible-names.py"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -f "$GATE" ]; then
    fail "PREMISE: $GATE is missing -- if it moved, move this test with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

echo "-- PREMISE: the gate passes against the real tree"
real_out="$(python3 "$GATE" 2>&1)"
real_rc=$?
if [ "$real_rc" -eq 0 ]; then
    pass "PREMISE: the real tree is clean, so a failure below is about the fixture"
else
    fail "PREMISE: the real tree already fails -- fix that first: $real_out"
fi

FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/lft-e2e-names.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT

# A tree the gate will accept: its own floors need 10+ specs and a few hundred bundle values.
mkdir -p "$FIXTURE/scripts" "$FIXTURE/tests/e2e/ui/tests" "$FIXTURE/pkg/server/i18n"
cp "$GATE" "$FIXTURE/scripts/"
cp pkg/server/i18n/Language.properties "$FIXTURE/pkg/server/i18n/"
i=1
while [ "$i" -le 12 ]; do
    printf "test('x', async ({ page }) => { await page.getByRole('button', { name: 'Save' }).click(); });\n" \
        >"$FIXTURE/tests/e2e/ui/tests/filler$i.spec.ts"
    i=$((i + 1))
done

echo ""
echo "-- FIRING: a name that is a prefix of a shipped string is reported"

# 'Primary' is a strict prefix of aria_primary_nav's value. This is the real defect, replanted.
printf "test('x', async ({ page }) => { await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible(); });\n" \
    >"$FIXTURE/tests/e2e/ui/tests/planted.spec.ts"
out="$( cd "$FIXTURE" && python3 scripts/check-e2e-accessible-names.py 2>&1 )"
rc=$?
if [ "$rc" -eq 0 ]; then
    fail "FIRING: a planted 'Primary' assertion was not reported -- the gate cannot see its own subject"
elif grep -q "Primary Navigation" <<<"$out"; then
    pass "FIRING: the collision is reported, and names the string it also matches"
else
    fail "FIRING: the gate failed but did not name the colliding string, so nobody can act on it: $out"
fi

echo ""
echo "-- BOUNDING: the exemption list cannot go stale quietly"

rm -f "$FIXTURE/tests/e2e/ui/tests/planted.spec.ts"
# Nothing in the fixture asserts any ALLOWED name, so every entry is carrying nothing.
out="$( cd "$FIXTURE" && python3 scripts/check-e2e-accessible-names.py 2>&1 )"
rc=$?
if [ "$rc" -eq 0 ]; then
    fail "BOUNDING: an ALLOWED entry that appears nowhere was accepted -- the list can grow stale, which makes it a suppression"
elif grep -q "STALE ALLOWED" <<<"$out"; then
    pass "BOUNDING: an exemption that no longer collides is reported, so the list can only shrink"
else
    fail "BOUNDING: the gate failed for some other reason: $out"
fi

echo ""
echo "-- BOUNDING: a scan of nothing is refused"

mkdir -p "$FIXTURE/empty/scripts"
cp "$GATE" "$FIXTURE/empty/scripts/"
out="$( cd "$FIXTURE/empty" && python3 scripts/check-e2e-accessible-names.py 2>&1 )"
rc=$?
if [ "$rc" -eq 0 ]; then
    fail "BOUNDING: the gate reported success with no specs and no bundle (#1779)"
else
    pass "BOUNDING: a tree with no specs is refused rather than reported clean"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
