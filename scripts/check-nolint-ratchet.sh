#!/usr/bin/env bash
# Ceiling on suppressed errcheck findings, so the count can fall but not rise (#1331).
set -euo pipefail

# There are 752 //nolint:errcheck suppressions in the tree. #613 ("Strict Error Handling: Audit
# and Resolve Suppressed I/O Errors") was closed as completed, so either it did not cover these
# or the pattern came back -- and a linter that is enabled and then suppressed 752 times is not
# really enabled.
#
# Fixing them all is not this script's job. Stopping the number from growing is: that turns a
# large debt into a fixed one, which can then be paid down opportunistically. Lower the ceiling
# whenever the real count drops, and the ratchet tightens on its own.
#
# Wired into CI as of #1498. It was not, originally -- the comment on the make target explained
# that ci.yml belonged to another agent under #1328 -- and a gate nobody runs does not gate: the
# count reached 757 against a ceiling of 752 without anything failing.
CEILING="${LFT_NOLINT_CEILING:-748}"

count() {
    grep -rho 'nolint:[a-z,]*' --include='*.go' pkg/ cmd/ 2>/dev/null \
        | grep -c 'errcheck' || true
}

ACTUAL="$(count)"

echo "//nolint:errcheck suppressions: $ACTUAL (ceiling $CEILING)"

if [ "$ACTUAL" -gt "$CEILING" ]; then
    echo
    echo "FAILED: $((ACTUAL - CEILING)) more suppression(s) than the ceiling allows."
    echo "Handle the error, or if it genuinely cannot be acted on, say why in a comment"
    echo "beside the nolint and raise the ceiling in this script deliberately."
    exit 1
fi

if [ "$ACTUAL" -lt "$CEILING" ]; then
    echo "The count has fallen. Lower LFT_NOLINT_CEILING in this script to $ACTUAL to keep the ratchet tight."
fi
