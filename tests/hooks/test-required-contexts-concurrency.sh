#!/usr/bin/env bash
# test-required-contexts-concurrency.sh — tests check 6 of scripts/check-required-contexts.sh (#2358)
#
# Check 6 refuses a concurrency group on a workflow that backs a required context while its
# pull_request trigger fires on events that do not change the commit. The defect it guards held
# #2354 BLOCKED for 16 hours with every check green: two Issue Link Check runs landed on one SHA,
# the group cancelled the pending one before its job started, and the required context waited
# for a check run that would never exist.
#
# The check is a parser over YAML trigger syntax, so most cases below are about the forms it
# has to read -- inline and block `types:`, job-level `concurrency:`, a `types:` belonging to a
# different trigger -- in both directions. A parser that misses a form fails open, and that is
# a gate reporting OK over a workflow it never understood.
#
# Every case runs with --offline: check 5 needs the GitHub API, and this test must run in CI
# with no admin token. Checks 1-4 still run, against the repo's real workflows, so a fixture
# that broke one of them would fail for the wrong reason -- which is why each case also asserts
# the check-6 message, not just the exit code.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TARGET="${REPO_ROOT}/scripts/check-required-contexts.sh"

[ -f "$TARGET" ] || { echo "FATAL: $TARGET missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

TREE="$WORK/tree"
mkdir -p "$TREE/scripts" "$TREE/.github/workflows"
cp "$TARGET" "$TREE/scripts/check-required-contexts.sh"

reset_workflows() {
    for wf in ci.yml e2e-sso.yml issue-link-check.yml; do
        cp "$REPO_ROOT/.github/workflows/$wf" "$TREE/.github/workflows/$wf"
    done
}

# The job section every issue-link-check fixture shares, so check 1 still finds the context.
JOB='permissions:
  contents: read

jobs:
  check-issue-link:
    name: Verify PR references an issue
    runs-on: ubuntu-latest
    steps:
      - run: true'

# write_issue_link <header> — replace issue-link-check.yml with <header> plus the shared job.
write_issue_link() {
    printf '%s\n\n%s\n' "$1" "$JOB" > "$TREE/.github/workflows/issue-link-check.yml"
}

CHECK6='declares a concurrency group and backs a required context'

# run_case <description> <expect: refused|allowed> [needle]
run_case() {
    local desc="$1" expect="$2" needle="${3:-}"
    local out rc
    out=$(cd "$TREE" && bash ./scripts/check-required-contexts.sh --offline 2>&1)
    rc=$?
    if [ "$expect" = refused ]; then
        if [ "$rc" -ne 0 ] && grep -qF "$CHECK6" <<<"$out" \
            && { [ -z "$needle" ] || grep -qF "$needle" <<<"$out"; }; then
            pass "$desc"
            return
        fi
        fail "$desc -- expected check 6 to refuse it${needle:+ naming '$needle'} (exit $rc)"
    else
        if [ "$rc" -eq 0 ] && ! grep -qF "$CHECK6" <<<"$out"; then
            pass "$desc"
            return
        fi
        fail "$desc -- expected it to pass (exit $rc)"
    fi
    printf '%s\n' "$out" | sed 's/^/        /'
}

echo "Testing scripts/check-required-contexts.sh check 6 (concurrency vs same-SHA triggers)"
echo ""

# ------------------------------------------------------------------------------------------
# The repo as it stands. ci.yml and e2e-sso.yml both carry a group on the default triggers,
# so this is also the "commit-changing triggers are allowed" case on real files.
# ------------------------------------------------------------------------------------------
reset_workflows
run_case "the repo's own workflows pass" allowed

# ------------------------------------------------------------------------------------------
# The regression: issue-link-check.yml as it was when #2354 stuck.
# ------------------------------------------------------------------------------------------
write_issue_link 'name: Issue Link Check

on:
  pull_request:
    types: [opened, edited, synchronize, reopened, labeled, unlabeled]
    branches:
      - master

concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number }}
  cancel-in-progress: false'
run_case "the #2354 workflow is refused, naming the same-SHA types" refused "edited labeled unlabeled"

# ------------------------------------------------------------------------------------------
# Parser forms.
# ------------------------------------------------------------------------------------------
write_issue_link 'name: Issue Link Check

on:
  pull_request:
    types:
      - opened
      - labeled
    branches:
      - master

concurrency:
  group: x'
run_case "block-form types: are read" refused "labeled"

# A job-level group cancels pending runs the same way a workflow-level one does.
printf '%s\n' "$JOB" | sed 's/^    runs-on: ubuntu-latest$/    runs-on: ubuntu-latest\
    concurrency:\
      group: per-job/' > "$WORK/job"
printf '%s\n\n%s\n' 'name: Issue Link Check

on:
  pull_request:
    types: [opened, labeled]' "$(cat "$WORK/job")" > "$TREE/.github/workflows/issue-link-check.yml"
run_case "a job-level concurrency group counts too" refused "labeled"

write_issue_link 'name: Issue Link Check

on:
  pull_request:
    types: [opened, synchronize, reopened]

concurrency:
  group: x
  cancel-in-progress: true'
run_case "a group on commit-changing types only is allowed" allowed

write_issue_link 'name: Issue Link Check

on:
  pull_request:
  issues:
    types: [labeled]

concurrency:
  group: x'
run_case "types: under another trigger are not pull_request types" allowed

write_issue_link 'name: Issue Link Check

on:
  pull_request:
    types: [opened, edited, labeled]'
run_case "same-SHA types without a group are allowed" allowed

echo ""
echo "  passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
