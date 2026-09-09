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

for gate in check-edr-safety.sh check-nolint-ratchet.sh check-test-home-isolation.sh; do
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
for gate in check-edr-safety.sh check-nolint-ratchet.sh check-test-home-isolation.sh; do
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

if (cd "$REPO_ROOT" && LFT_HOME_ISOLATION_MIN_PACKAGES=999999 ./scripts/check-test-home-isolation.sh >/dev/null 2>&1); then
    fail "check-test-home-isolation.sh ignores LFT_HOME_ISOLATION_MIN_PACKAGES, so its floor cannot be exercised"
else
    pass "check-test-home-isolation.sh honours LFT_HOME_ISOLATION_MIN_PACKAGES"
fi

# check-test-home-isolation.sh mentions os.UserHomeDir() in its own comments, which is one of the
# patterns it searches for -- the self-match that made the first version of this file pass for the
# wrong reason. It cannot happen here: the search is --include='*_test.go', so a .sh file is never
# in the corpus. Asserted rather than reasoned about, because the include is what makes it true.
if grep -q "include='\*_test.go'" "$REPO_ROOT/scripts/check-test-home-isolation.sh"; then
    pass "check-test-home-isolation.sh restricts its corpus to _test.go, so it cannot match itself"
else
    fail "check-test-home-isolation.sh no longer restricts its corpus to _test.go -- it contains its own patterns in comments and would self-match"
fi

# The gates that already had a guard. These were asserted by GREPPING THEIR SOURCE for words
# like "zero" and "found no", which is not the property: it passes for a guard that has been
# commented out, for one that computes its floor wrongly, and for one whose message merely
# mentions the word. A guard nobody has tried to break is decoration. Replaced with running
# each of them, out of tree, and requiring a refusal (#1779).
#
# Same isolation as above -- the gate is copied to $WORK/gates and run from $WORK/empty, so
# the .cjs/.mjs path resolution (relative to the script's own directory) finds no corpus.
for gate in check-css-modifiers.cjs check-i18n-keys.cjs check-html-balance.mjs check-theme-contrast.cjs; do
    cp "$REPO_ROOT/scripts/$gate" "$WORK/gates/"
    if (cd "$WORK/empty" && node "$WORK/gates/$gate" >/dev/null 2>&1); then
        fail "$gate PASSES with no corpus at all -- the guard that fixed #1744/#1774 is inert"
    else
        pass "$gate refuses when there is no corpus to examine"
    fi
done

# check-theme-tokens.mjs is deliberately in its own case: it resolves its inputs against the
# WORKING DIRECTORY rather than the script's own directory, so the loop above would test
# something different for it. Run from a directory with no pkg/ it refuses -- but by throwing an
# uncaught ENOENT from readdirSync, before reaching its own `refs.size === 0` guard. Exit 1
# either way, so it fails closed and the property holds; recorded here because the two are not
# the same thing and a future reader should not mistake the crash for the guard firing.
cp "$REPO_ROOT/scripts/check-theme-tokens.mjs" "$WORK/gates/"
if (cd "$WORK/empty" && node "$WORK/gates/check-theme-tokens.mjs" >/dev/null 2>&1); then
    fail "check-theme-tokens.mjs PASSES with no corpus at all -- the #1774 guard is inert"
else
    pass "check-theme-tokens.mjs refuses when there is no corpus to examine"
fi

# ---------------------------------------------------------------------------
# Gates given a floor in #1779. Each was measured reporting success over nothing first; the
# message each one printed is quoted where it is asserted.
# ---------------------------------------------------------------------------

# check-theme-contrast.cjs. Before the floor, four theme files with no fill tokens between
# them produced "check-theme-contrast: OK -- 0 contrast checks across 1 theme(s)", exit 0.
# The pre-existing guard counts theme FILES, which a vacuous run already gets right.
mkdir -p "$WORK/nofill/scripts" "$WORK/nofill/pkg/server/static/themes"
cp "$REPO_ROOT/scripts/check-theme-contrast.cjs" "$WORK/nofill/scripts/"
printf ':root { --bg-card-solid: #ffffff; --text-main: #000000; }\n' \
    >"$WORK/nofill/pkg/server/static/themes/nofill.css"
if (cd "$WORK/nofill" && node scripts/check-theme-contrast.cjs >/dev/null 2>&1); then
    fail "check-theme-contrast.cjs PASSES having performed 0 contrast checks, as long as a .css file exists"
else
    pass "check-theme-contrast.cjs refuses a run that measured no colours"
fi

# check_docs_review.py, full-repo mode. Before the floor, an empty directory produced
# "Scanning 0 markdown files..." followed by the green tick, exit 0.
if python3 "$REPO_ROOT/scripts/check_docs_review.py" --dir "$WORK/empty" >/dev/null 2>&1; then
    fail "check_docs_review.py PASSES a full-repo audit of a directory with no markdown in it"
else
    pass "check_docs_review.py refuses a full-repo audit that found no markdown"
fi

# check_docs_review.py, changed-files mode. Before the guard, .md paths that do not resolve
# were dropped silently and the run reported success over zero files. CI builds that list with
# `git diff --name-only`, unquoted, from the repository root -- so a wrong working directory
# arrives here looking exactly like a clean docs tree.
if python3 "$REPO_ROOT/scripts/check_docs_review.py" --changed-files \
    "$WORK/empty/no-such-file.md" >/dev/null 2>&1; then
    fail "check_docs_review.py PASSES when every markdown path it was given is unreadable"
else
    pass "check_docs_review.py refuses when the markdown paths it was given do not resolve"
fi

# ...and the narrowness control for it, which is the reason the guard asks git rather than just
# checking the filesystem. A PR that DELETES a markdown file names it in the diff too, and
# refusing that would be a false refusal on a legitimate change. This case passes both before
# and after the guard: it bounds it rather than testing it.
DELETED_MD=$(git -C "$REPO_ROOT" log --diff-filter=D --name-only --format= -20 -- '*.md' \
    | grep -m1 '\.md$' || true)
if [ -z "$DELETED_MD" ]; then
    printf '  \033[33mSKIP\033[0m  no deleted .md in recent history to use as the deletion control\n'
elif (cd "$REPO_ROOT" && python3 scripts/check_docs_review.py --changed-files "$DELETED_MD" >/dev/null 2>&1); then
    pass "check_docs_review.py still accepts a deleted markdown file (narrowness control)"
else
    fail "check_docs_review.py refuses a markdown file that this repo genuinely deleted ($DELETED_MD) -- the anti-vacuity guard is too wide and will fail any PR that removes a doc"
fi

# check-required-contexts.sh. Checks 1-3 iterate REQUIRED_JOBS, and check 5 -- the one that
# would notice the list was short -- prints NOT VERIFIED and carries on in the mode CI runs.
# So an emptied mirror printed "OK: required contexts all report" over zero contexts.
if (cd "$REPO_ROOT" && LFT_CONTEXTS_MIN_JOBS=999 ./scripts/check-required-contexts.sh --offline 2>&1 |
    grep -q 'below the floor of 999'); then
    pass "check-required-contexts.sh honours LFT_CONTEXTS_MIN_JOBS"
else
    fail "check-required-contexts.sh ignores LFT_CONTEXTS_MIN_JOBS, so its mirror floor cannot be exercised"
fi

# The floors must be overridable in the other direction too, or the counterpart assertions above
# would be untestable in place.
#
# These assert the MESSAGE, not the exit status, and the reason is not hypothetical. Written
# with `if <cmd> ; then fail` the check_docs_review.py case PASSED against the version that has
# no floor at all -- because a full-repo audit of the real tree already exits non-zero, on
# staleness. The floor was never evaluated and the assertion could not tell. That is the exact
# defect this whole issue is about (github-workflow §5c), arriving in the test written to
# catch it.
if (cd "$REPO_ROOT" && LFT_CONTRAST_MIN_CHECKS=999999 node ./scripts/check-theme-contrast.cjs 2>&1 |
    grep -q 'below the floor of 999999'); then
    pass "check-theme-contrast.cjs honours LFT_CONTRAST_MIN_CHECKS"
else
    fail "check-theme-contrast.cjs ignores LFT_CONTRAST_MIN_CHECKS, so its floor cannot be exercised"
fi

if (cd "$REPO_ROOT" && LFT_DOCS_MIN_FILES=999999 python3 ./scripts/check_docs_review.py --dir . 2>&1 |
    grep -q 'below the floor of 999999'); then
    pass "check_docs_review.py honours LFT_DOCS_MIN_FILES"
else
    fail "check_docs_review.py ignores LFT_DOCS_MIN_FILES, so its floor cannot be exercised"
fi

# And each must still be usable against the real tree. A floor set above reality is an outage.
if (cd "$REPO_ROOT" && node ./scripts/check-theme-contrast.cjs >/dev/null 2>&1); then
    pass "check-theme-contrast.cjs still passes against the real themes"
else
    fail "check-theme-contrast.cjs now fails on a clean checkout -- the floor is too high, or a colour is genuinely wrong"
fi

# check_docs_review.py's full-repo mode is informational and CAN legitimately exit non-zero on
# this tree, over pre-existing staleness. So the counterpart is asserted on what it scanned
# rather than on how it exited -- the floor must not be tripping, whatever else it reports.
# `|| true` because this file runs under `set -e` and the run legitimately exits non-zero.
DOCS_REAL=$(cd "$REPO_ROOT" && python3 ./scripts/check_docs_review.py --dir . 2>&1 || true)
if printf '%s\n' "$DOCS_REAL" | grep -q 'NO-SCAN'; then
    fail "check_docs_review.py reports NO-SCAN against the real tree -- the floor is above reality:
    $(printf '%s\n' "$DOCS_REAL" | grep 'NO-SCAN' | head -1)"
elif printf '%s\n' "$DOCS_REAL" | grep -qE 'Scanning [1-9][0-9]* markdown files'; then
    pass "check_docs_review.py still scans the real tree without tripping its floor"
else
    fail "check_docs_review.py did not report scanning any markdown files on the real tree:
    $(printf '%s\n' "$DOCS_REAL" | head -2 | tr '\n' ' ')"
fi

if (cd "$REPO_ROOT" && ./scripts/check-required-contexts.sh --offline >/dev/null 2>&1); then
    pass "check-required-contexts.sh still passes against the real workflows"
else
    fail "check-required-contexts.sh now fails on a clean checkout -- the mirror floor is too high, or a context is genuinely unemitted"
fi

printf '\n'
if [ "$FAIL" -eq 0 ]; then
    printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
else
    printf '\033[31m%d of %d checks failed\033[0m\n' "$FAIL" "$((PASS + FAIL))"
    exit 1
fi
