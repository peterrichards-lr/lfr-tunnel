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
#
# `cmd/.*\.go` and every `pkg/**/*.html` are here as of #1779, which audited each gate for what
# it does not look at. Both were outside for no stated reason: cmd/ holds the two binaries'
# entry points and has real tests next to them (cmd/lfr-tunnel/main_test.go), and the pattern
# named pkg/server/dashboard.html specifically -- so blocked.html, passcode.html and the client
# inspector's 1600-line page were all invisible while the page next to them was not. The scope
# is now stated by directory and extension rather than by naming the one file someone thought of.
FUNCTIONAL='^(pkg/.*\.(go|html)|cmd/.*\.go|ui/src/.*|pkg/server/static/.*|scripts/.*|Makefile)$'
# Paths that satisfy it.
TESTS='(_test\.go$|^tests/)'
# Go test files are under pkg/ too, so they match FUNCTIONAL first -- excluded explicitly.
NOT_FUNCTIONAL='(_test\.go$|^tests/)'

CHANGED=$(cat)

# "The diff was empty" and "the diff never ran" arrive here as the same thing: no bytes on
# stdin. This check is advisory and must NOT be turned into a failing gate to tell them apart
# -- that would block every PR whose diff command hiccupped, which is the opposite of what an
# advisory check is for. But it must not report the two identically either, which is what it
# did: both printed "No functional change in this diff" and exited 0, so a caller whose
# `git diff` had failed got the same green line as a documentation-only PR (#1779).
#
# So: say which one it was, and exit 0 either way. The distinction is in the output, where a
# human or a later gate can act on it.
if [ -z "$CHANGED" ]; then
    echo "No file list was given, so nothing was examined."
    echo "Note: that is NOT the same as 'this diff changes nothing'. An empty list is also what"
    echo "a failed 'git diff' produces, and this check cannot tell the two apart from here."
    exit 0
fi

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
