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

echo ""
echo "-- and the git hooks must run them too, so a break is found before the push (#2027)"

HOOK="${REPO_ROOT}/scripts/pre-push-hook.sh"

if [ ! -f "$HOOK" ]; then
    fail "pre-push-hook.sh not found at $HOOK -- if it moved, move this guard with it"
else
    # The JS gates are covered BY CONSTRUCTION: the hook discovers them with a glob rather than
    # a list, so a gate added tomorrow runs tomorrow with nothing to remember. That is a stronger
    # property than name-matching, and it is what this asserts -- a name list in the hook would
    # pass this test while silently omitting the newest gate, which is #1929 exactly.
    # Comments stripped before ANY of this. The hook's own prose names these gates -- it
    # explains which one bit and why -- so a bare grep matches the explanation and reports
    # coverage that does not exist. Measured: with comments included, deleting the ratchet from
    # the hook's run list left this test green. That is the self-match the workflow skill warns
    # about, found by mutating this guard rather than by reading it.
    HOOK_CODE="$(grep -vE "^[[:space:]]*#" "$HOOK")"

    # Anchored on the ASSIGNMENT that feeds the run loop, not on any `find` in the file. An
    # earlier draft matched a second find inside a warning message, so replacing the real
    # discovery with a hardcoded list left this green -- a marker that occurs anyway proves
    # nothing (workflow skill §5c).
    if echo "$HOOK_CODE" | grep -qE "^JS_GATES=.*find scripts .*check-\*\.cjs.*check-\*\.mjs" &&
       echo "$HOOK_CODE" | grep -qE "for gate in \\\$JS_GATES"; then
        pass "the hook discovers .cjs/.mjs gates by glob, so new ones are covered automatically"
    else
        fail "the hook no longer discovers .cjs/.mjs gates by glob -- a hardcoded list omits the next gate added"
    fi

    # The shell gates have no such glob (they are not all runnable the same way), so they are
    # named, and anything deliberately left out has to say so HERE. Compared for equality: an
    # entry that stops being true fails this test rather than rotting quietly, the same ratchet
    # shape as untrackedGoroutineExceptions and V1_KNOWN_INERT.
    #
    # check-test-coverage-signal.sh reads coverage on STDIN (CI pipes `go test` output into it),
    # so there is nothing for a hook to hand it.
    HOOK_EXEMPT="check-test-coverage-signal.sh"

    for g in $(find "${REPO_ROOT}/scripts" -maxdepth 1 -name 'check-*.sh' -exec basename {} \; | sort); do
        grep -qF "scripts/$g" "$CI" || continue   # only gates CI actually runs are in scope
        if echo "$HOOK_CODE" | grep -qF "$g"; then
            if echo "$HOOK_EXEMPT" | grep -qF "$g"; then
                fail "$g is BOTH run by the hook and listed as exempt -- delete the stale exemption"
            else
                pass "$g is run by the pre-push hook"
            fi
        elif echo "$HOOK_EXEMPT" | grep -qF "$g"; then
            pass "$g is exempt from the hook, deliberately"
        else
            fail "$g runs in CI but not in either git hook -- a 13-minute cycle to learn a 1s fact (#2027)"
        fi
    done
fi

echo ""

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
