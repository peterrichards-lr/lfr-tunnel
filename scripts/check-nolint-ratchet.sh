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
# Raised 748 -> 752 by #1152, deliberately and with the four named here, because the script
# asks for exactly that rather than a silent bump. All four are deferred cleanup in the new
# location-stats code where the error is unactionable or already reported elsewhere, and each
# carries a comment beside it saying which:
#
#   pkg/db/location_stats.go       tx.Rollback   -- returns sql.ErrTxDone after a good Commit
#   pkg/db/location_stats.go       stmt.Close    -- driver resource release, after the writes
#   pkg/db/location_stats.go       rows.Close    -- same failure rows.Err() already reports
#   pkg/db/location_stats_test.go  rows.Close    -- test read-back
#
# Three further suppressions this feature originally had were removed rather than counted: a
# failed stats write, a failed periodic flush and a failed flush on shutdown now log, because
# each of those silently loses a whole period of counts.
CEILING="${LFT_NOLINT_CEILING:-750}"

count() {
    grep -rho 'nolint:[a-z,]*' --include='*.go' pkg/ cmd/ 2>/dev/null \
        | grep -c 'errcheck' || true
}

# How many Go files the count above actually looked at.
#
# Without this the ratchet passes on a scan of NOTHING (#1779). `2>/dev/null` hides a missing
# pkg/ or cmd/, the count comes back 0, 0 is under any ceiling, and the gate reports success --
# then helpfully suggests LOWERING the ceiling to 0, which would permanently disarm it. Measured:
# run from an empty directory it exits 0 with "The count has fallen".
#
# A wrong working directory, a rename, or a future change to the --include set all produce that
# shape, and none of them look like a failure.
corpus_size() {
    find pkg cmd -name '*.go' -type f 2>/dev/null | wc -l | tr -d ' '
}

# A floor, not an exact figure: the point is to tell "hundreds of files" from "none", not to
# encode a number that needs maintaining. The tree holds several hundred.
MIN_GO_FILES="${LFT_NOLINT_MIN_FILES:-100}"
CORPUS="$(corpus_size)"
if [ "$CORPUS" -lt "$MIN_GO_FILES" ]; then
    echo "FAILED: only $CORPUS Go files found under pkg/ and cmd/ (expected at least $MIN_GO_FILES)."
    echo "The suppression count is therefore meaningless -- a scan of nothing counts zero and"
    echo "would pass. Run this from the repository root."
    exit 1
fi

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
