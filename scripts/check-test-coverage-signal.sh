#!/usr/bin/env bash
# check-test-coverage-signal.sh — notice a functional change that brings no test with it (#1660)
#
# Two changes landed today that nothing would have noticed:
#
#   #1632 moved `rm -rf pkg/server/ui-dist` and its .gitkeep restore out of `build:`. The guard
#         watching that wipe stopped seeing its subject and went quiet while still exiting 0.
#   #1648 added padNodeDaily with six unit tests, but nothing asserts the handler CALLS it.
#
# This catches the first shape and not the second, and that limit is the point: it detects
# ABSENCE, not insufficiency. #1648 changed Go and changed Go tests, so it passes here. A green
# result means "a test file was touched", never "this change is tested".
#
# Advisory by design. It annotates and exits 0. The reason checks like this get switched off is
# false positives, and there are real ones -- refactors, renames, dependency bumps, and above all
# a change already covered by an existing test, which no path-matching can know.
#
# The escape hatch is where most of the value is. `no-test-needed: <reason>` in the PR body turns
# "nobody noticed there was no test" into "someone said why not, and a reviewer can disagree".
#
# Usage: printf '%s\n' <changed files> | PR_BODY="..." check-test-coverage-signal.sh
set -uo pipefail

# Paths whose change suggests a test should exist. Deliberately not `docs/` or `.github/`.
FUNCTIONAL='^(pkg/.*\.go|ui/src/.*|pkg/server/static/.*|pkg/server/dashboard\.html|scripts/.*|Makefile)$'
# Paths that satisfy it.
TESTS='(_test\.go$|^tests/)'
# Go test files are under pkg/ too, so they match FUNCTIONAL first -- excluded explicitly.
NOT_FUNCTIONAL='(_test\.go$|^tests/)'

CHANGED=$(cat)

functional=$(printf '%s\n' "$CHANGED" | grep -E "$FUNCTIONAL" 2>/dev/null | grep -Ev "$NOT_FUNCTIONAL" 2>/dev/null || true)
tests=$(printf '%s\n' "$CHANGED" | grep -E "$TESTS" 2>/dev/null || true)

if [ -z "$functional" ]; then
    echo "No functional change in this diff -- nothing to ask about."
    exit 0
fi

if [ -n "$tests" ]; then
    echo "Functional change with test changes alongside it."
    echo "Note: that means a test file was touched, NOT that this change is covered."
    exit 0
fi

# `no-test-needed:` must carry a reason. A bare marker is the thing this exists to prevent --
# it would let the check be silenced without anyone saying anything.
reason=$(printf '%s' "${PR_BODY:-}" | grep -iE '^[[:space:]]*no-test-needed:[[:space:]]*\S' || true)
if [ -n "$reason" ]; then
    echo "Functional change with no test, and a stated reason:"
    printf '  %s\n' "$reason"
    exit 0
fi

# ::warning:: so it surfaces on the PR without failing it.
echo "::warning title=No test accompanies this change::This PR changes functional code but no test file. If that is right, add a 'no-test-needed: <reason>' line to the PR body so the decision is recorded rather than assumed."
echo ""
echo "Functional files changed with no accompanying test:"
printf '%s\n' "$functional" | sed 's/^/  /'
echo ""
echo "Add a test, or put a line in the PR body saying why one is not needed:"
echo "  no-test-needed: pure rename, behaviour covered by TestPadNodeDaily"
exit 0
