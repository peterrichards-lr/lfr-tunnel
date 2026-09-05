#!/bin/bash
# Guards the failure mode behind #1380/#1386: a REQUIRED status check that produces no
# check run at all leaves its context pending forever, so the PR can never merge.
#
# The rule is narrower than it first appears, and getting it wrong in either direction
# costs something:
#
#   * A job skipped by a job-level `if:` reports a check run with conclusion "skipped",
#     and GitHub DOES accept "skipped" as satisfying a required context. Verified: #1379
#     merged while "E2E Docker Integration Test" and "E2E Playwright UI Test" were both
#     required and skipped. So path-gating a non-matrix job is fine, and forbidding it
#     would throw away the velocity win from #1363 for no reason.
#
#   * A MATRIX job skipped that way reports a single check run under the bare job name.
#     The required contexts are the expanded names -- "Test Suite (ubuntu-latest)" and so
#     on -- and those never appear. Blocks forever (#1380).
#
#   * A workflow-level `paths:` filter means the whole workflow never runs on a
#     non-matching PR, so none of its check runs are created. Blocks forever (#1386),
#     which is what happened to "E2E Keycloak SSO Integration Test".
#
# So: check runs must always be *produced* for required contexts. What conclusion they
# reach is GitHub's business.
#
# The required list below is MIRRORED, not derived. Deriving it at run time is not an
# option for either run that matters: in CI, GITHUB_TOKEN has contents:read and reading
# branch protection needs an admin token, and locally this is a pre-push gate that has to
# work offline. So a mirror -- but a mirror drifts, and #1729 is what that looks like here.
# "CI Gate", the most load-bearing context on the repo, was required by BOTH enforcement
# mechanisms and absent from the mirror, so checks 1-3 quietly ran against every required
# context except that one, and still printed OK.
#
# Hence check 5: whenever the live lists CAN be read, the mirror is compared against them
# and any disagreement fails. When they cannot be read, the run says so out loud -- an
# unverified run must never be indistinguishable from a verified one, which is the same
# defect in a different place. Flags:
#
#   (default)         compare when possible; print NOT VERIFIED and carry on when not.
#                     This is what CI runs, where the token cannot read protection.
#   --verify-mirror   being unable to read the live lists is itself a failure. Use when
#                     you want a definitive answer: `make check-contexts-live`.
#   --offline         skip the comparison entirely.
#
# Kept to bash 3.2 on purpose (#1395). This script exists to be run BEFORE pushing, and
# macOS still ships bash 3.2.57 as /bin/bash, so anything newer makes it CI-only -- which
# is most of its value gone. The first version used `declare -A`, a bash 4 builtin: under
# 3.2 the assignment is parsed as an ordinary indexed array with word splitting, `Suite` is
# read as a variable, and `set -u` turns that into a fatal error before a single check runs.
# So: no associative arrays, no mapfile, no ${var^^}. tests/hooks/test-shell-portability.sh
# runs this file under both bash 3.2 and bash 5 and requires them to agree.

set -euo pipefail

verify_mode=auto
case "${1:-}" in
    --verify-mirror) verify_mode=strict ;;
    --offline)       verify_mode=off ;;
    "")              ;;
    *) echo "usage: $0 [--verify-mirror|--offline]" >&2; exit 2 ;;
esac

# Mirrors branches/master/protection -> required_status_checks.contexts, as job names.
# Matrix contexts are listed by job name; GitHub appends "(<matrix value>)" itself.
#
# "<job name>|<workflow file>", one per line. A delimited list rather than an associative
# array, for the bash 3.2 reason above. "|" rather than a tab because a tab is invisible in
# a diff and one stray space would silently drop an entry -- and an entry silently dropped
# from THIS list means a required context stops being checked while the script still says OK.
REQUIRED_JOBS='Test Suite|.github/workflows/ci.yml
Lint & Format Check|.github/workflows/ci.yml
Documentation Review Check|.github/workflows/ci.yml
E2E Docker Integration Test|.github/workflows/ci.yml
E2E Playwright UI Test|.github/workflows/ci.yml
CI Gate|.github/workflows/ci.yml
E2E Keycloak SSO Integration Test|.github/workflows/e2e-sso.yml
Verify PR references an issue|.github/workflows/issue-link-check.yml'

# Guard against the failure mode the delimiter choice is about: if a line loses its "|", the
# loops below would read an empty file path and quietly check nothing.
while IFS= read -r line; do
    case "$line" in
        *'|'*) ;;
        *) echo "ERROR: malformed REQUIRED_JOBS entry (no '|'): '$line'" >&2; exit 1 ;;
    esac
done <<REQ
$REQUIRED_JOBS
REQ

# Unique workflow files backing a required context.
REQUIRED_FILES=$(printf '%s\n' "$REQUIRED_JOBS" | awk -F'|' 'NF { print $2 }' | sort -u)

fail=0

# 1. Every required context must be emitted by a job that exists.
# A here-doc redirect, not a pipe: a piped `while` runs in a subshell, so `fail=1` set
# inside it would be discarded and the script would exit 0 having found problems.
while IFS='|' read -r name file; do
    [ -n "$name" ] || continue
    if [ ! -f "$file" ]; then
        echo "ERROR: '$file' does not exist, but required context '$name' is expected there." >&2
        fail=1
        continue
    fi
    if ! grep -qF "name: $name" "$file"; then
        echo "ERROR: required context '$name' is not produced by any job in $file." >&2
        echo "       A required context nobody emits stays pending and blocks every PR." >&2
        fail=1
    fi
done <<REQ
$REQUIRED_JOBS
REQ

# 2. No workflow containing a required job may be path-filtered at the workflow level.
while IFS= read -r file; do
    [ -n "$file" ] || continue
    [ -f "$file" ] || continue
    # Only `pull_request:` matters. A `paths:` under `push:` is fine -- required status
    # checks are evaluated on pull requests, and master's own e2e-sso.yml deliberately
    # keeps the push filter while dropping the pull_request one.
    if awk '
        /^on:/          { in_on = 1; next }
        in_on && /^[a-zA-Z]/ { in_on = 0 }
        in_on && /^  pull_request:/ { in_pr = 1; next }
        in_on && /^  [a-zA-Z_-]+:/  { in_pr = 0 }
        in_pr && /^    paths(-ignore)?:/ { found = 1 }
        END { exit(found ? 0 : 1) }
    ' "$file"; then
        echo "ERROR: $file filters 'pull_request' by 'paths:' but contains a required job." >&2
        echo "       On a non-matching PR the workflow never runs, no check run is created," >&2
        echo "       and the required context stays pending forever (#1386)." >&2
        echo "       Gate the steps instead, so the job still runs and still reports." >&2
        fail=1
    fi
done <<FILES
$REQUIRED_FILES
FILES

# 3. A matrix job backing a required context must not carry a job-level `if:`.
#    Non-matrix jobs may: a "skipped" conclusion satisfies a required check.
while IFS='|' read -r name file; do
    [ -n "$name" ] || continue
    [ -f "$file" ] || continue

    block=$(awk -v want="    name: $name" '
        $0 == want { inblock = 1; next }
        inblock && /^  [a-zA-Z0-9_-]+:/ { exit }
        inblock { print }
    ' "$file")

    echo "$block" | grep -qE '^      matrix:' || continue

    if echo "$block" | grep -qE '^    if:'; then
        echo "ERROR: matrix job '$name' has a job-level 'if:'." >&2
        echo "       A skipped matrix job reports ONE check run under the bare job name," >&2
        echo "       so the required per-entry contexts never appear at all (#1380)." >&2
        echo "       Gate the steps instead." >&2
        fail=1
    fi
done <<REQ
$REQUIRED_JOBS
REQ

# 4. Every job in ci.yml must be listed in ci-gate's `needs:`.
#
#    This is the check that matters once "CI Gate" is the required context. The gate
#    treats a `skipped` result as acceptable, which is only safe while every job is a
#    dependency: a job skipped because an upstream job failed reports `skipped`, and the
#    only thing stopping that hiding the failure is that the upstream job is also in the
#    list and reports `failure` itself. A job missing from `needs:` is worse than
#    ungated -- it is ungated while the gate reports green.
GATE_FILE=".github/workflows/ci.yml"
if grep -qE '^  ci-gate:' "$GATE_FILE"; then
    gate_needs=$(awk '
        /^  ci-gate:/ { in_gate = 1; next }
        in_gate && /^  [a-zA-Z0-9_-]+:/ { exit }
        in_gate && /^    needs:/ { in_needs = 1; next }
        in_needs && /^      - / { sub(/^      - /, ""); print; next }
        in_needs && /^    [a-zA-Z]/ { in_needs = 0 }
    ' "$GATE_FILE")

    all_jobs=$(awk '
        /^jobs:/ { in_jobs = 1; next }
        in_jobs && /^  [a-zA-Z0-9_-]+:$/ { sub(/:$/, ""); sub(/^  /, ""); print }
    ' "$GATE_FILE" | grep -v '^ci-gate$')

    for job in $all_jobs; do
        if ! printf '%s\n' "$gate_needs" | grep -qxF "$job"; then
            echo "ERROR: job '$job' is not in ci-gate's needs:." >&2
            echo "       CI Gate accepts a 'skipped' result, which is only safe while every" >&2
            echo "       job is a dependency. A job missing from needs: is ungated while the" >&2
            echo "       gate still reports green." >&2
            fail=1
        fi
    done
else
    echo "NOTE: no ci-gate job yet; skipping gate-coverage check." >&2
fi

# 5. The mirror itself must still match what GitHub actually enforces.
#
#    Checks 1-4 compare the workflows against the mirror. Nothing compared the mirror against
#    the repo, so an entry missing from it removes a required context from every check above
#    while the script still reports OK (#1729) -- coverage that reads as coverage and is none.
#
#    BOTH enforcement mechanisms are read, and the union is what gates a merge. Reading only
#    one is a wrong answer: master carries a ruleset AND classic branch protection, each with
#    its own context list, which is what made #1380 look self-contradictory (the workflow fix
#    was right, the ruleset agreed, and the classic list nobody had read kept it blocked).
#
#    Matrix contexts arrive expanded ("Test Suite (ubuntu-latest)") while the mirror holds job
#    names, so the trailing parenthetical is folded off before comparing. A job name genuinely
#    ending in "(...)" would be folded too; the alternative is putting matrix values in the
#    mirror, which is more remote state to keep in sync, not less.
mirror_names=$(printf '%s\n' "$REQUIRED_JOBS" | awk -F'|' 'NF { print $1 }' | sort -u)

live_contexts=""
live_reason=""
if [ "$verify_mode" = "off" ]; then
    live_reason="--offline was passed"
elif ! command -v gh >/dev/null 2>&1; then
    live_reason="the 'gh' CLI is not installed"
else
    gh_err="${TMPDIR:-/tmp}/lft-required-contexts.$$"
    if classic_contexts=$(gh api "/repos/{owner}/{repo}/branches/master/protection" \
        --jq '.required_status_checks.contexts[]?' 2>"$gh_err"); then
        # /rules/branches/master, not /rulesets/<id>: it returns every rule in force on the
        # branch, so no ruleset id has to be hardcoded here -- which would be a second mirror
        # of remote state, in the file whose bug is a mirror of remote state.
        if ruleset_contexts=$(gh api "/repos/{owner}/{repo}/rules/branches/master" \
            --jq '.[] | select(.type == "required_status_checks")
                  | .parameters.required_status_checks[].context' 2>"$gh_err"); then
            live_contexts=$(printf '%s\n%s\n' "$classic_contexts" "$ruleset_contexts")
        else
            live_reason="reading master's rulesets failed: $(tr '\n' ' ' <"$gh_err" | cut -c1-160)"
        fi
    else
        live_reason="reading master's branch protection failed, which needs an admin token: $(tr '\n' ' ' <"$gh_err" | cut -c1-160)"
    fi
    rm -f "$gh_err"
fi

# An empty union is not "nothing is required" -- master has required contexts. Treat it as a
# read that did not work, so a silently-empty response cannot pass by comparing nothing.
if [ -z "$live_reason" ] && [ -z "$(printf '%s\n' "$live_contexts" | grep . || true)" ]; then
    live_reason="both enforcement lists came back empty, which cannot be right for master"
fi

if [ -n "$live_reason" ]; then
    if [ "$verify_mode" = "strict" ]; then
        echo "ERROR: could not verify REQUIRED_JOBS against live enforcement -- $live_reason." >&2
        echo "       --verify-mirror was asked for a definitive answer and cannot give one." >&2
        fail=1
    else
        echo "NOT VERIFIED: REQUIRED_JOBS was NOT compared against live enforcement -- $live_reason." >&2
        echo "              Checks above compared the workflows against that list, not the list" >&2
        echo "              against the repo. For that, run: make check-contexts-live" >&2
    fi
else
    live_names=$(printf '%s\n' "$live_contexts" \
        | sed -e 's/ ([^()]*)$//' -e 's/[[:space:]]*$//' | grep . | sort -u)

    missing_names=$(printf '%s\n' "$live_names" | while IFS= read -r ctx; do
        [ -n "$ctx" ] || continue
        printf '%s\n' "$mirror_names" | grep -qxF "$ctx" || printf '%s\n' "$ctx"
    done)
    extra_names=$(printf '%s\n' "$mirror_names" | while IFS= read -r name; do
        [ -n "$name" ] || continue
        printf '%s\n' "$live_names" | grep -qxF "$name" || printf '%s\n' "$name"
    done)

    while IFS= read -r ctx; do
        [ -n "$ctx" ] || continue
        echo "ERROR: '$ctx' is required on master but is missing from REQUIRED_JOBS." >&2
        echo "       Every check above therefore skipped it entirely. Add a line:" >&2
        echo "         $ctx|<the workflow file whose job emits it>" >&2
        fail=1
    done <<MISSING
$missing_names
MISSING

    while IFS= read -r name; do
        [ -n "$name" ] || continue
        echo "ERROR: '$name' is in REQUIRED_JOBS but is required by neither master's ruleset" >&2
        echo "       nor its classic branch protection. Remove it here, or add it there -- a" >&2
        echo "       mirror that over-claims is as misleading as one that omits (#1729)." >&2
        fail=1
    done <<EXTRA
$extra_names
EXTRA

    if [ -z "$missing_names" ] && [ -z "$extra_names" ]; then
        echo "VERIFIED: REQUIRED_JOBS matches live enforcement ($(printf '%s\n' "$live_names" \
            | grep -c .) contexts, union of master's ruleset and classic branch protection)."
    fi
fi

if [ "$fail" -ne 0 ]; then
    exit 1
fi

echo "OK: required contexts all report, and CI Gate covers every job."
