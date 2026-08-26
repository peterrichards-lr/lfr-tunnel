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
# This cannot read branch protection (GITHUB_TOKEN has contents:read; protection needs
# admin), so the required list is mirrored below. If the required contexts change in the
# GitHub UI, update this list in the same PR.

set -euo pipefail

# Mirrors branches/master/protection -> required_status_checks.contexts, as job names.
# Matrix contexts are listed by job name; GitHub appends "(<matrix value>)" itself.
declare -A REQUIRED_JOBS=(
    ["Test Suite"]=".github/workflows/ci.yml"
    ["Lint & Format Check"]=".github/workflows/ci.yml"
    ["Documentation Review Check"]=".github/workflows/ci.yml"
    ["E2E Docker Integration Test"]=".github/workflows/ci.yml"
    ["E2E Playwright UI Test"]=".github/workflows/ci.yml"
    ["E2E Keycloak SSO Integration Test"]=".github/workflows/e2e-sso.yml"
    ["Verify PR references an issue"]=".github/workflows/issue-link-check.yml"
)

fail=0

# 1. Every required context must be emitted by a job that exists.
for name in "${!REQUIRED_JOBS[@]}"; do
    file="${REQUIRED_JOBS[$name]}"
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
done

# 2. No workflow containing a required job may be path-filtered at the workflow level.
for file in $(printf '%s\n' "${REQUIRED_JOBS[@]}" | sort -u); do
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
done

# 3. A matrix job backing a required context must not carry a job-level `if:`.
#    Non-matrix jobs may: a "skipped" conclusion satisfies a required check.
for name in "${!REQUIRED_JOBS[@]}"; do
    file="${REQUIRED_JOBS[$name]}"
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
done

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
        if ! grep -qxF "$job" <<<"$gate_needs"; then
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

if [ "$fail" -ne 0 ]; then
    exit 1
fi

echo "OK: required contexts all report, and CI Gate covers every job."
