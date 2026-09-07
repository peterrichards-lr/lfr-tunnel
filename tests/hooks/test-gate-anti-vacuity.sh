#!/bin/sh
# test-gate-anti-vacuity.sh -- a gate must not report success on a scan of nothing (#1779).
#
# Three gates have been found this week to be silently narrower than anyone assumed, and in each
# case the limit was written in a comment rather than asserted: check-css-modifiers.cjs scanned
# only Portal V2 (25 undefined classes invisible), check-theme-tokens.mjs read only stylesheets
# (20 inline references invisible), and ci.yml's platform_sensitive omitted pkg/config (Windows
# reported SUCCESS in five seconds without running, and master went red twice).
#
# The shape underneath all three is the same: a check that examined nothing is indistinguishable
# from a check that found nothing wrong. Two more gates were measured to have it outright --
# both exited 0 from an empty directory:
#
#   check-edr-safety.sh    "EDR Safety Check Passed: no unguarded 'go test' or 'go run'"
#   check-nolint-ratchet.sh "The count has fallen" -- and suggested lowering the ceiling to 0,
#                           which would have disarmed it permanently
#
# The EDR one is the guard against the pattern that has cost three full environment reinstalls,
# so a version of it that cannot fail is worse than no version at all: it reads as proof.
set -eu

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PASS=0
FAIL=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

# The gate under test is kept OUTSIDE the directory it scans.
#
# The first version of this test copied it to $WORK/scripts/ and ran from $WORK -- so the gate
# scanned itself. check-edr-safety.sh greps for the literal strings "go test" and "go run", which
# its own patterns contain, so with its guard removed it still exited non-zero: a self-match, not
# the empty-scan failure being asserted. It passed for the wrong reason, which is the exact defect
# this whole issue is about.
mkdir -p "$WORK/gates" "$WORK/empty"

for gate in check-edr-safety.sh check-nolint-ratchet.sh; do
    cp "$REPO_ROOT/scripts/$gate" "$WORK/gates/"
    chmod +x "$WORK/gates/$gate"

    # Executed directly so the shebang is honoured, NOT via `sh`. Both gates are
    # `#!/usr/bin/env bash` and use bash arrays; on macOS `sh` is bash and this passed locally,
    # while CI's `sh` is dash, where the arrays are a syntax error. The gate then died for that
    # reason and the counterpart assertion below read it as "the floor is too high" -- a green
    # local run and a red CI one, for a defect in the test rather than in either gate.
    if (cd "$WORK/empty" && "$WORK/gates/$gate" >/dev/null 2>&1); then
        fail "$gate PASSES on an empty tree -- a scan of nothing must not report success"
    else
        pass "$gate fails on an empty tree"
    fi
done

# The counterpart. A guard that fails everywhere is not a guard, it is an outage -- so each must
# still pass against the real tree, where there genuinely is a corpus to examine.
for gate in check-edr-safety.sh check-nolint-ratchet.sh; do
    if (cd "$REPO_ROOT" && "./scripts/$gate" >/dev/null 2>&1); then
        pass "$gate still passes against the real tree"
    else
        fail "$gate now fails on a clean checkout -- the floor is too high, or something is genuinely wrong"
    fi
done

# The floors are overridable, so the failure can be reproduced deliberately without contriving
# an empty directory. If these stop being read the guard becomes untestable in place.
if (cd "$REPO_ROOT" && LFT_EDR_MIN_FILES=999999 ./scripts/check-edr-safety.sh >/dev/null 2>&1); then
    fail "check-edr-safety.sh ignores LFT_EDR_MIN_FILES, so its floor cannot be exercised"
else
    pass "check-edr-safety.sh honours LFT_EDR_MIN_FILES"
fi

if (cd "$REPO_ROOT" && LFT_NOLINT_MIN_FILES=999999 ./scripts/check-nolint-ratchet.sh >/dev/null 2>&1); then
    fail "check-nolint-ratchet.sh ignores LFT_NOLINT_MIN_FILES, so its floor cannot be exercised"
else
    pass "check-nolint-ratchet.sh honours LFT_NOLINT_MIN_FILES"
fi

# The two gates that already had a guard, asserted so a later edit cannot quietly remove it.
for gate in check-css-modifiers.cjs check-theme-tokens.mjs; do
    if grep -qiE "scanned 0|found no|examined nothing|no documents|zero" "$REPO_ROOT/scripts/$gate"; then
        pass "$gate still carries an empty-scan guard"
    else
        fail "$gate lost its empty-scan guard -- it was the fix for #1744/#1774"
    fi
done

printf '\n'
if [ "$FAIL" -eq 0 ]; then
    printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
else
    printf '\033[31m%d of %d checks failed\033[0m\n' "$FAIL" "$((PASS + FAIL))"
    exit 1
fi
