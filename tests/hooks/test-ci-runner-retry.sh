#!/usr/bin/env bash
# test-ci-runner-retry.sh -- CI re-runs a run once only when GitHub never gave a job a runner (#2603)
#
# The fixtures are real runs, trimmed to the fields the decision reads:
#   lost-runner        45444982356 (#2597, 2026-11-24): macOS "not acquired by Runner", CI Gate failed
#   superseded         45426182978: a master push cancelled by a newer one -- macOS cancelled with no
#                      runner and CI Gate failed, the SAME shape, but GitHub's annotation says it was
#                      superseded. Re-running it would test a stale commit.
#   genuine-failure    45401845619: a real failure
#   lost-plus-failure  lost-runner with the ubuntu leg turned into a genuine failure
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DECIDE="${REPO_ROOT}/scripts/ci-runner-retry.sh"
WF="${REPO_ROOT}/.github/workflows/ci-runner-retry.yml"
CI="${REPO_ROOT}/.github/workflows/ci.yml"
FIX="${SCRIPT_DIR}/fixtures/ci-runner-retry"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

decide() {
	CI_RETRY_JOBS_JSON="${FIX}/$1/jobs.json" CI_RETRY_ANNOTATIONS_DIR="${FIX}/$1/annotations" "$DECIDE" 0
}

expect() { # case, wanted prefix, why
	local got
	got=$(decide "$1")
	case "$got" in
		"$2"*) pass "$1 -> $got" ;;
		*) fail "$1: got '$got', want '$2…' ($3)" ;;
	esac
}

echo "Testing the CI runner-retry decision..."
expect lost-runner "rerun: Test Suite (macos-latest)" "a leg GitHub never started must be retried"
expect superseded "skip:" "a run cancelled by a newer push is not runner capacity"
expect genuine-failure "skip:" "a real failure needs a person"
expect lost-plus-failure "skip: genuine failures as well" "a lost leg must not get a real failure re-run with it"

echo "Testing the workflow wiring..."
ci_name=$(sed -n 's/^name: *//p' "$CI" | head -1)
if grep -qE "workflows: \[${ci_name}\]" "$WF"; then
	pass "triggers on the workflow named '${ci_name}'"
else
	fail "ci-runner-retry.yml does not trigger on '${ci_name}', the name in ci.yml"
fi
if grep -q 'github.event.workflow_run.run_attempt == 1' "$WF"; then
	pass "retries the first attempt only"
else
	fail "no run_attempt == 1 guard: a still-short runner pool would loop"
fi
# shellcheck disable=SC2016 # the literal workflow text, not an expansion
if grep -qF 'gh run rerun "$RUN_ID" --failed' "$WF"; then
	pass "re-runs only the failed jobs"
else
	fail "the re-run is not 'gh run rerun \"\$RUN_ID\" --failed'"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
