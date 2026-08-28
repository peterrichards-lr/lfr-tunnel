#!/usr/bin/env bash
# commit-msg-hook.sh — reject a negated closing reference in a commit message (#1540)
#
# GitHub matches close/fixes/resolves followed by #<N> and ignores any negation in front of it,
# so "Does not close #1521" in a commit body closes #1521.
#
# Checked here as well as on the PR body in CI, because this repo's squash CONCATENATES commit
# messages (see github-workflow/SKILL.md), so the merge commit carries every commit body -- CI
# only ever sees the PR text.
#
# Has to be commit-msg rather than pre-commit: at pre-commit time the message does not exist yet,
# and COMMIT_EDITMSG still holds the PREVIOUS one. A pre-commit version of this check ran, passed,
# and let the offending commit straight through.
set -uo pipefail

MSG_FILE="${1:-}"
if [ -z "$MSG_FILE" ] || [ ! -f "$MSG_FILE" ]; then
    # Nothing to read means nothing to check; a hook that fails closed on a missing argument
    # would block commits for a reason unrelated to the commit.
    exit 0
fi

TOP=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
CHECK="$TOP/scripts/check-closing-refs.sh"

if [ ! -x "$CHECK" ]; then
    # A branch cut before the check existed must still be committable, and silence here would be
    # the failure the hook shims were written to remove (#1425).
    echo "WARNING: $CHECK not found or not executable, so the closing-reference check did not run." >&2
    exit 0
fi

echo "[Git Hook] Checking the commit message for a negated closing reference..."
if ! "$CHECK" "$MSG_FILE"; then
    exit 1
fi
echo "✅ No negated closing reference."
exit 0
