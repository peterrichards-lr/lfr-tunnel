#!/bin/bash
# Guards the failure mode behind #1386: a job backing a REQUIRED status check must always
# report, so it must not carry a job-level `if:`.
#
# GitHub records a job skipped by a job-level `if:` as a check run with conclusion
# "skipped", and a "skipped" conclusion does not satisfy a context listed in
# required_status_checks. A path filter on such a job therefore does not make a PR faster,
# it makes it unmergeable -- which is exactly what happened to every docs-only PR between
# #1363 and #1386.
#
# This cannot read branch protection (GITHUB_TOKEN has contents:read, and protection needs
# admin), so the required list is mirrored below. If someone changes the required contexts
# in the GitHub UI, update this list in the same PR.

set -euo pipefail

WORKFLOW=".github/workflows/ci.yml"

# Mirrors repos/<owner>/<repo>/branches/master/protection -> required_status_checks.contexts
# Matrix contexts are listed by their job name; GitHub appends "(<matrix value>)" itself.
REQUIRED_JOB_NAMES=(
    "Test Suite"
    "Lint & Format Check"
    "Documentation Review Check"
    "E2E Docker Integration Test"
    "E2E Playwright UI Test"
    "E2E Keycloak SSO Integration Test"
    "Verify PR references an issue"
)

fail=0

for name in "${REQUIRED_JOB_NAMES[@]}"; do
    # "Verify PR references an issue" lives in its own workflow file.
    if [ "$name" = "Verify PR references an issue" ]; then
        if ! grep -qF "name: $name" .github/workflows/issue-link-check.yml; then
            echo "ERROR: required context '$name' is not produced by any job." >&2
            fail=1
        fi
        continue
    fi

    if ! grep -qF "name: $name" "$WORKFLOW"; then
        echo "ERROR: required context '$name' is not produced by any job in $WORKFLOW." >&2
        echo "       A required context nobody emits blocks every PR forever." >&2
        fail=1
        continue
    fi

    # Look at the job block that declares this name and reject a job-level `if:`.
    # Job-level keys sit at 4 spaces; step-level `if:` sits at 8 and is fine.
    block=$(awk -v want="    name: $name" '
        $0 == want { inblock = 1; next }
        inblock && /^  [a-zA-Z0-9_-]+:/ { exit }
        inblock { print }
    ' "$WORKFLOW")

    if echo "$block" | grep -qE '^    if:'; then
        echo "ERROR: job '$name' has a job-level 'if:'." >&2
        echo "       It backs a required status check, so skipping it reports \"skipped\"," >&2
        echo "       which never satisfies the requirement (#1386)." >&2
        echo "       Guard the steps instead, so the job still runs and still reports." >&2
        fail=1
    fi
done

if [ "$fail" -ne 0 ]; then
    exit 1
fi

echo "OK: every required status check is emitted by a job that always reports."
