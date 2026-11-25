#!/usr/bin/env bash
# ci-runner-retry.sh -- decide whether a finished CI run failed only because GitHub never gave a
# job a runner, and so should be re-run once (#2603).
#
#   scripts/ci-runner-retry.sh <run-id>
#
# Prints one line and exits 0:
#   rerun: <jobs>   every job that did not pass was lost to runner capacity (CI Gate aside)
#   skip: <reason>  anything else
# Exits 2 if the run's jobs cannot be read, so the workflow fails visibly rather than guessing.
#
# WHY: "Test Suite (macos-latest)" is a required context and the matrix always expands (#1365).
# When GitHub's macOS pool is short, the leg is never acquired: it is cancelled after ~15 minutes
# with no runner, no steps and the annotation below, the PR is blocked, and nothing was tested.
# 4 of ~78 macOS legs from 2026-11-11 to 11-24, none on Linux or Windows.
#
# THE ANNOTATION IS THE SIGNAL, NOT "cancelled with no runner". ci.yml has cancel-in-progress, so
# a run superseded by a newer push also has cancelled jobs that never got a runner. Re-running
# that would test a stale commit. Only GitHub's own "not acquired" annotation tells them apart.
#
# Any other failure in the run means no retry: a genuine failure needs a person, and re-running
# it as well would just repeat it. CI Gate is the one exception, because it fails as a consequence
# of the lost leg and `gh run rerun --failed` re-runs it with it.
#
# Fixtures (tests/hooks/test-ci-runner-retry.sh): CI_RETRY_JOBS_JSON is a file shaped like the
# API's {"jobs": [...]}, and CI_RETRY_ANNOTATIONS_DIR holds <job-id>.json annotation arrays.
set -euo pipefail

NOT_ACQUIRED='was not acquired by Runner'
GATE_JOB='CI Gate'

run_id="${1:?usage: $0 <run-id>}"

read_jobs() {
	if [ -n "${CI_RETRY_JOBS_JSON:-}" ]; then
		cat "$CI_RETRY_JOBS_JSON"
	else
		# Attempt 1 only: the workflow acts on first attempts, and a later attempt's job list
		# would mix in jobs that were never part of the run being judged.
		gh api --paginate "repos/{owner}/{repo}/actions/runs/${run_id}/attempts/1/jobs?per_page=100" \
			--jq '.jobs[]' | jq -s '{jobs: .}'
	fi
}

read_annotations() {
	if [ -n "${CI_RETRY_ANNOTATIONS_DIR:-}" ]; then
		cat "${CI_RETRY_ANNOTATIONS_DIR}/$1.json" 2>/dev/null || echo '[]'
	else
		gh api "repos/{owner}/{repo}/check-runs/$1/annotations"
	fi
}

if ! jobs=$(read_jobs) || ! jq -e '.jobs | type == "array"' >/dev/null 2>&1 <<<"$jobs"; then
	echo "error: could not read the jobs of run ${run_id}" >&2
	exit 2
fi

not_passed=$(jq -c '[.jobs[] | select(.conclusion != "success" and .conclusion != "skipped" and .conclusion != "neutral")]' <<<"$jobs")
if [ "$(jq 'length' <<<"$not_passed")" -eq 0 ]; then
	echo "skip: every job passed or was skipped"
	exit 0
fi

lost=()
while IFS=$'\t' read -r id name; do
	[ -n "$id" ] || continue
	if read_annotations "$id" | jq -e --arg s "$NOT_ACQUIRED" 'any(.[]; (.message // "") | contains($s))' >/dev/null; then
		lost+=("$name")
	fi
done < <(jq -r '.[] | select(.conclusion == "cancelled" and ((.runner_name // "") == "")) | [.id, .name] | @tsv' <<<"$not_passed")

if [ "${#lost[@]}" -eq 0 ]; then
	echo "skip: no job was lost to runner capacity"
	exit 0
fi

lost_json=$(printf '%s\n' "${lost[@]}" | jq -R . | jq -s .)
others=$(jq -r --argjson lost "$lost_json" --arg gate "$GATE_JOB" \
	'[.[] | select((.name as $n | $lost | index($n)) == null and .name != $gate) | "\(.name)=\(.conclusion)"] | join(", ")' <<<"$not_passed")
if [ -n "$others" ]; then
	echo "skip: genuine failures as well: ${others}"
	exit 0
fi

joined=$(printf '%s, ' "${lost[@]}")
echo "rerun: ${joined%, }"
