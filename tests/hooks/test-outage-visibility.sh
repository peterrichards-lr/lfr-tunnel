#!/usr/bin/env bash
# test-outage-visibility.sh -- the portal must not claim the gateway is up when it cannot reach it
# (#1869).
#
# Asserted by reading Layout.tsx rather than by driving a browser: the failure is a hardcoded
# className, written at edit time, and a Playwright run needs the whole Docker stack for something
# a grep settles. The e2e suite covers what the indicator LOOKS like; this covers that it is
# derived from something at all.
#
# The defect: status-dot--online was rendered in BOTH branches of the status-page-link ternary,
# from state that no failed request ever touched. /api/version was caught to `{ data: {} }` and
# the ten-second /api/me poll swallowed everything that was not a 401, so the portal showed
# "System Online" for the entire duration of an outage.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LAYOUT="${REPO_ROOT}/ui/src/components/Layout.tsx"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Outage visibility:"
echo ""

if [ ! -f "$LAYOUT" ]; then
    fail "ui/src/components/Layout.tsx is missing -- if it moved, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# Comments describe the old defect verbatim, so judge code only.
CODE="$(grep -vE '^\s*(//|\*|/\*)' "$LAYOUT")"

# -- PREMISE. If the indicator is gone entirely the assertions below are vacuous.
if printf '%s' "$CODE" | grep -q "status-dot"; then
    pass "PREMISE   Layout still renders a status indicator"
else
    fail "PREMISE   no status-dot found -- the cases below prove nothing"
fi

# -- FIRING. The defect itself: a literal online class with nothing deciding it.
if printf '%s' "$CODE" | grep -qE "status-dot status-dot--online"; then
    fail "FIRING    the online class is hardcoded; it must be chosen from reachability"
    printf '%s' "$CODE" | grep -nE "status-dot status-dot--online" | sed 's/^/            /'
else
    pass "FIRING    the indicator class is not hardcoded to online"
fi

# -- FIRING. Something must actually track reachability.
if printf '%s' "$CODE" | grep -q "gatewayReachable"; then
    pass "FIRING    reachability is tracked in state"
else
    fail "FIRING    nothing tracks whether the gateway answered"
fi

# -- FIRING. And the poll must update it, or the indicator is only right on first load.
if printf '%s' "$CODE" | grep -q "setGatewayReachable(false)"; then
    pass "FIRING    a failed poll marks the gateway unreachable"
else
    fail "FIRING    the background poll does not record a failure; an outage after load stays hidden"
fi
if printf '%s' "$CODE" | grep -q "setGatewayReachable(true)"; then
    pass "BOUNDING  a successful poll clears it, so the indicator recovers on its own"
else
    fail "BOUNDING  nothing sets reachability back to true; the warning would be sticky"
fi

# -- FIRING. The status page URL comes FROM the gateway, so it has to outlive it.
# Two independent greps rather than one matching the whole call: prettier wraps that line, and an
# assertion coupled to line layout fails on a reformat rather than on the behaviour it guards.
if printf '%s' "$CODE" | grep -q "localStorage.setItem" &&
    printf '%s' "$CODE" | grep -q "lft.statusPageUrl"; then
    pass "FIRING    the status page URL is cached for when the gateway cannot supply it"
else
    fail "FIRING    the status page URL is not cached; the link vanishes exactly when it is wanted"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
