#!/usr/bin/env bash
# test-ci-runs-every-gate.sh -- every comparison gate must actually run somewhere (#1929).
#
# The defect: scripts/check-status-vocabulary.cjs, check-alert-vocabulary.cjs,
# check-print-selectors.cjs and check-load-failure-surfaced.cjs all existed, all had dedicated
# hook tests proving they fire, and NONE of them ran in any CI job or either git hook. They
# fired only when someone typed the make target by hand.
#
# That is the worst shape a guard can take: it looks present in review, its own tests pass, and
# it protects nothing. Two of the four were written specifically to stop a defect recurring.
#
# This is the guard on the guards. It is deliberately a plain existence check rather than
# anything clever -- the thing it must catch is a NEW gate that nobody wired in, and the whole
# point is that such a gate is silent.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CI="${REPO_ROOT}/.github/workflows/ci.yml"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "-- every scripts/check-*.{cjs,mjs} must be referenced by a CI job"

if [ ! -f "$CI" ]; then
    fail "ci.yml not found at $CI -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

gates=()
while IFS= read -r g; do gates+=("$g"); done < <(
    find "${REPO_ROOT}/scripts" -maxdepth 1 \( -name 'check-*.cjs' -o -name 'check-*.mjs' \) -exec basename {} \; | sort
)

# PREMISE / anti-vacuity. If the glob matches nothing, every assertion below is trivially
# satisfied and this guard would pass forever on a tree with no gates at all.
if [ "${#gates[@]}" -eq 0 ]; then
    fail "found no check-*.cjs/.mjs gates at all -- refusing to report that an empty set is covered"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi
pass "PREMISE  found ${#gates[@]} gate(s) to check"

for g in "${gates[@]}"; do
    if grep -qF "scripts/$g" "$CI"; then
        pass "$g is run by CI"
    else
        fail "$g runs in NO CI job -- add it to the Lint & Format Check job, or it protects nothing"
    fi
done

# CONTROL. The check must be capable of failing: a gate name that cannot appear in ci.yml
# has to be reported missing. Without this the loop above could be matching everything.
if grep -qF "scripts/check-this-does-not-exist.cjs" "$CI"; then
    fail "CONTROL  a nonexistent gate was found in ci.yml -- the matcher is not discriminating"
else
    pass "CONTROL  a gate absent from ci.yml is correctly detected as absent"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
