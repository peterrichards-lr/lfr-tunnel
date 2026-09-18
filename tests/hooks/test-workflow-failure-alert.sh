#!/usr/bin/env bash
#
# test-workflow-failure-alert.sh -- the failure mode of an alerter is silence (#1730)
#
# A monitor that has stopped monitoring looks exactly like a monitor with nothing to report.
# That is the same class of bug as the one it was built for: `Deploy Documentation` failed
# twelve times in a row across a week and the only evidence was an Actions tab nobody opens.
# So this does not merely assert that the workflow file exists -- it drives the detector
# through every branch of its decision and checks what came out.
#
# Six behaviours, each one a way the design could be wrong rather than a line of coverage:
#
#   FIRES        -- N consecutive failures produce an alert, proved against the real recorded
#                   history of the incident, not a hand-written approximation of it.
#   DOES NOT     -- below the threshold, nothing. An alerter that fires on a single flake gets
#     OVERFIRE      muted, and a muted alerter is worse than none.
#   DOES NOT     -- with an alert already open, silence. This is the dedup, and it is the half
#     REPEAT        most likely to be quietly lost in a refactor, because losing it still looks
#                   like a working alerter right up until it is muted.
#   RE-ARMS      -- recovery closes the alert, so the next breakage is alertable again. Without
#                   this the system alerts exactly once, ever.
#   COUNTS THE   -- cancellations do not silently reset a live streak; pull_request runs do not
#     RIGHT RUNS    count at all, because their failures already show on the PR.
#   COUNTS THE   -- a success only breaks a streak if it actually re-ran the job, and the step,
#     RIGHT GREEN   that failed. A docs-only merge whose Windows leg was path-filtered is not a
#                   recovery (#2030). Section 6, in FIRING/BOUNDING vocabulary.
#   IS WIRED UP  -- the workflow actually runs on a schedule and actually invokes the script
#                   with the permissions it needs.
#
# Kept to bash 3.2 (see AGENTS.md): no associative arrays, no mapfile, no ${var^^}.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DETECT="${REPO_ROOT}/scripts/detect-workflow-failure-streak.sh"
WORKFLOW="${REPO_ROOT}/.github/workflows/workflow-failure-alert.yml"
HISTORY="${SCRIPT_DIR}/fixtures/docs-workflow-runs.json"
CI_HISTORY="${SCRIPT_DIR}/fixtures/ci-windows-skipped-runs.json"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

for required in "$DETECT" "$WORKFLOW" "$HISTORY" "$CI_HISTORY"; do
    [ -f "$required" ] || { echo "FATAL: $required missing"; exit 1; }
done
command -v jq >/dev/null 2>&1 || { echo "FATAL: jq is required by this test"; exit 1; }

TMPDIR_TEST=$(mktemp -d -t lft-wf-alert-test.XXXXXX) || exit 1
trap 'rm -rf "$TMPDIR_TEST"' EXIT

echo "Testing the master-only / scheduled workflow failure alerter..."
echo

# ---------------------------------------------------------------------------------------------
# Fixture construction.
#
# A fixture is one JSON object holding everything the detector would otherwise fetch: the
# workflow list, the run history per workflow id, and the currently open alert issues. Passing
# --fixture makes the detector read that instead of the GitHub API, which is what lets the
# threshold, the dedup and the recovery path all be exercised offline and deterministically.
# ---------------------------------------------------------------------------------------------

# fixture_from_conclusions <file> <alerts-json> <conclusion...>
# Builds a single-workflow fixture. Each conclusion may be given as "event:conclusion" to
# override the default `push` event.
fixture_from_conclusions() {
    local out="$1" alerts="$2"
    shift 2
    local runs="[]" spec event conclusion
    for spec in "$@"; do
        case "$spec" in
            *:*) event="${spec%%:*}"; conclusion="${spec#*:}" ;;
            *)   event="push"; conclusion="$spec" ;;
        esac
        runs=$(printf '%s' "$runs" | jq -c --arg e "$event" --arg c "$conclusion" \
            '. + [{status: "completed", event: $e, conclusion: (if $c == "null" then null else $c end)}]')
    done
    jq -n --argjson runs "$runs" --argjson alerts "$alerts" '{
        workflows: [{id: 999, path: ".github/workflows/subject.yml", name: "Subject"}],
        runs: {"999": $runs},
        alerts: $alerts
    }' > "$out"
}

NO_ALERTS='[]'
ALERT_OPEN='[{"number": 4242, "body": "<!-- workflow-failure-alert:999 -->\nSubject is failing."}]'

# verdict <fixture> [extra args...] -> the verdict word for the single subject workflow
verdict() {
    local fx="$1"
    shift
    "$DETECT" --fixture "$fx" "$@" 2>/dev/null | awk -F'\t' '{print $1}' | head -1
}

# streak <fixture> -> the streak number the detector computed
streak() {
    "$DETECT" --fixture "$1" 2>/dev/null | head -1 | sed -n 's/.*streak=\([0-9]*\).*/\1/p'
}

# ---------------------------------------------------------------------------------------------
# 1. It fires -- on the real incident.
#
# The recorded history of `Deploy Documentation` as it stood on 3 September 2026: run 31 back to
# run 20 all failed. Dropping the three post-recovery runs from the head reproduces exactly what
# the API would have returned during the week nobody noticed. If this alerter had existed, this
# is the input it would have seen.
# ---------------------------------------------------------------------------------------------
BROKEN="${TMPDIR_TEST}/real-broken.json"
jq '{
      workflows: [{id: 331907295, path: ".github/workflows/docs.yml", name: "Deploy Documentation"}],
      runs: {"331907295": (.runs[3:])},
      alerts: []
    }' "$HISTORY" > "$BROKEN"

REAL_STREAK=$(streak "$BROKEN")
if [ "$REAL_STREAK" = "12" ]; then
    pass "the real 2026-08-27..09-03 history reads as a 12-run failure streak"
else
    fail "expected a streak of 12 from the recorded incident, computed '$REAL_STREAK'"
fi

OUT=$("$DETECT" --fixture "$BROKEN" 2>/dev/null)
if printf '%s' "$OUT" | grep -q '^ALERT.*name=Deploy Documentation$'; then
    pass "it alerts on the incident it was built for"
else
    fail "no ALERT for the real docs incident -- the alerter would have stayed silent again"
    printf '        got: %s\n' "$OUT"
fi

# ---------------------------------------------------------------------------------------------
# 2. The threshold, at its boundary and on both sides of it.
#
# Off-by-one here is invisible in production: one-too-eager mutes the alert with flake noise,
# one-too-lax leaves a genuine breakage unreported for an extra cycle. Neither announces itself.
# ---------------------------------------------------------------------------------------------
BELOW="${TMPDIR_TEST}/below.json"
AT="${TMPDIR_TEST}/at.json"
ABOVE="${TMPDIR_TEST}/above.json"
fixture_from_conclusions "$BELOW" "$NO_ALERTS" failure failure success success
fixture_from_conclusions "$AT"    "$NO_ALERTS" failure failure failure success
fixture_from_conclusions "$ABOVE" "$NO_ALERTS" failure failure failure failure success

[ "$(verdict "$BELOW")" = "OK" ] \
    && pass "2 consecutive failures do not alert (a double flake is not a broken workflow)" \
    || fail "2 consecutive failures alerted -- the alerter will be muted by flakes"

[ "$(verdict "$AT")" = "ALERT" ] \
    && pass "3 consecutive failures alert (the threshold boundary, inclusive)" \
    || fail "3 consecutive failures did not alert -- the default threshold is not 3"

[ "$(verdict "$ABOVE")" = "ALERT" ] \
    && pass "4 consecutive failures alert" \
    || fail "4 consecutive failures did not alert"

# A single success anywhere in the streak resets it. Two failures either side of a success are
# not four consecutive failures, and an alerter that cannot tell the difference is measuring
# flakiness rather than brokenness.
INTERLEAVED="${TMPDIR_TEST}/interleaved.json"
fixture_from_conclusions "$INTERLEAVED" "$NO_ALERTS" failure failure success failure failure
[ "$(verdict "$INTERLEAVED")" = "OK" ] \
    && pass "a success inside the window resets the streak" \
    || fail "failure,failure,success,failure,failure alerted -- the streak is not consecutive"

# The threshold is a knob, not a constant. If --threshold stops being honoured, the operator's
# only tuning control is silently gone.
[ "$(verdict "$BELOW" --threshold 2)" = "ALERT" ] \
    && pass "--threshold 2 alerts on a 2-run streak" \
    || fail "--threshold is not honoured"
[ "$(verdict "$AT" --threshold 5)" = "OK" ] \
    && pass "--threshold 5 stays quiet on a 3-run streak" \
    || fail "--threshold is not honoured in the raising direction"

# ---------------------------------------------------------------------------------------------
# 3. Dedup -- it does not fire twice.
#
# The identical input that produced ALERT above must produce SKIP once an alert is open. This is
# the single most important assertion in the file: a poller with no memory re-alerts on every
# sweep, gets muted, and is then strictly worse than no alerting at all.
# ---------------------------------------------------------------------------------------------
DEDUP="${TMPDIR_TEST}/dedup.json"
fixture_from_conclusions "$DEDUP" "$ALERT_OPEN" failure failure failure failure
DEDUP_OUT=$("$DETECT" --fixture "$DEDUP" 2>/dev/null)
if printf '%s' "$DEDUP_OUT" | grep -q '^SKIP.*issue=4242'; then
    pass "an already-alerted workflow stays quiet (no duplicate issue per sweep)"
else
    fail "the open alert did not suppress a second alert -- this would spam and then be muted"
    printf '        got: %s\n' "$DEDUP_OUT"
fi

# The dedup must key on the workflow, not merely on "some alert issue is open". Otherwise the
# first broken workflow silences every other one.
OTHER="${TMPDIR_TEST}/other-workflow.json"
jq '.alerts = [{"number": 4242, "body": "<!-- workflow-failure-alert:111 -->"}]' "$DEDUP" > "$OTHER"
[ "$(verdict "$OTHER")" = "ALERT" ] \
    && pass "an alert for a different workflow does not suppress this one" \
    || fail "dedup is keyed on the label, not the workflow -- one breakage would mask all others"

# ---------------------------------------------------------------------------------------------
# 4. Recovery re-arms it.
#
# The full real history, recovery runs included, with the alert still open. Without this branch
# the alert issue stays open forever and the workflow can never alert again -- it fires exactly
# once in its lifetime, which is indistinguishable from working until the second incident.
# ---------------------------------------------------------------------------------------------
HEALED="${TMPDIR_TEST}/real-healed.json"
jq '{
      workflows: [{id: 331907295, path: ".github/workflows/docs.yml", name: "Deploy Documentation"}],
      runs: {"331907295": .runs},
      alerts: [{number: 4242, body: "<!-- workflow-failure-alert:331907295 -->"}]
    }' "$HISTORY" > "$HEALED"
HEALED_OUT=$("$DETECT" --fixture "$HEALED" 2>/dev/null)
if printf '%s' "$HEALED_OUT" | grep -q '^RESOLVE.*issue=4242'; then
    pass "recovery closes the alert, re-arming it for the next incident"
else
    fail "a recovered workflow did not close its alert -- it could only ever alert once"
    printf '        got: %s\n' "$HEALED_OUT"
fi

# ...and nothing to close when nothing is open.
NOTHING="${TMPDIR_TEST}/nothing.json"
fixture_from_conclusions "$NOTHING" "$NO_ALERTS" success success success
[ "$(verdict "$NOTHING")" = "OK" ] \
    && pass "a healthy workflow with no open alert is a no-op" \
    || fail "a healthy workflow produced something other than OK"

# ---------------------------------------------------------------------------------------------
# 5. It counts the right runs.
# ---------------------------------------------------------------------------------------------

# A cancelled run is not a verdict. Several workflows here cancel in-progress runs when a newer
# commit lands, so treating a cancellation as a success would reset a streak that is still very
# much running -- the streak would never reach the threshold on a busy day.
CANCELLED="${TMPDIR_TEST}/cancelled.json"
fixture_from_conclusions "$CANCELLED" "$NO_ALERTS" failure cancelled failure failure success
[ "$(verdict "$CANCELLED")" = "ALERT" ] \
    && pass "a cancelled run neither breaks nor pads the streak" \
    || fail "a cancelled run reset a live failure streak"

# ...and equally must not be counted as a failure, or a burst of superseded runs alerts on a
# perfectly healthy workflow.
CANCEL_ONLY="${TMPDIR_TEST}/cancel-only.json"
fixture_from_conclusions "$CANCEL_ONLY" "$NO_ALERTS" cancelled cancelled cancelled cancelled success
[ "$(verdict "$CANCEL_ONLY")" = "OK" ] \
    && pass "cancelled runs alone do not alert" \
    || fail "cancelled runs counted as failures -- superseded runs would page someone"

# pull_request runs are out of scope by construction: their failures are already reported on the
# PR. Counting them would make this duplicate the required checks and alert on ordinary red PRs.
PR_RUNS="${TMPDIR_TEST}/pr-runs.json"
fixture_from_conclusions "$PR_RUNS" "$NO_ALERTS" \
    pull_request:failure pull_request:failure pull_request:failure pull_request:failure
[ "$(verdict "$PR_RUNS")" = "OK" ] \
    && pass "pull_request failures are ignored (already visible on the PR)" \
    || fail "pull_request runs counted -- this would alert on every red PR"

# An in-progress run is not yet a verdict. If it were counted the streak would depend on when
# the sweep happened to fire.
INCOMPLETE="${TMPDIR_TEST}/incomplete.json"
jq '.runs["999"] = [{status: "in_progress", event: "push", conclusion: null}] + .runs["999"]' \
    "$AT" > "$INCOMPLETE"
[ "$(streak "$INCOMPLETE")" = "3" ] \
    && pass "an in-progress run does not affect the streak" \
    || fail "an in-progress run changed the streak"

# No history at all must be silence, not an alert. A brand-new workflow has zero runs, and
# `0 >= threshold` is the sort of comparison that goes wrong when a threshold reaches 0.
EMPTY="${TMPDIR_TEST}/empty.json"
jq -n '{workflows: [{id: 999, path: "x.yml", name: "Subject"}], runs: {"999": []}, alerts: []}' > "$EMPTY"
[ "$(verdict "$EMPTY")" = "OK" ] \
    && pass "a workflow with no run history does not alert" \
    || fail "a workflow with no runs alerted"

# The detector must refuse a zero threshold rather than alert on everything.
if "$DETECT" --fixture "$EMPTY" --threshold 0 >/dev/null 2>&1; then
    fail "--threshold 0 was accepted -- it would alert on every workflow forever"
else
    pass "--threshold 0 is rejected"
fi

# ---------------------------------------------------------------------------------------------
# 6. A success only counts as recovery if it re-ran what broke (#2030).
#
# `streak_of` was never wrong about this -- it has always treated `skipped` as "not a verdict".
# It was fed RUN-level conclusions, and a run whose failing job was skipped by a path filter
# still reports `success`. So these cases are about the detector's INPUT, and the CONTROL for
# each FIRING case is the same fixture with its job data removed: that is exactly the view the
# detector had before this change, driven through the same code.
#
# The fixture is the real thing (see its own header): master run `c2f702e9` failed on
# `Test Suite (windows-latest)`, and the next master run `05d58a51` -- #2025, docs only --
# reported `success` with that leg green and its `Run Tests` STEP skipped.
# ---------------------------------------------------------------------------------------------

# ci_fixture <out> <alerts-json> [jq-transform-of-the-whole-fixture]
ci_fixture() {
    local out="$1" alerts="$2" transform="${3:-.}"
    jq --argjson alerts "$alerts" "{
          workflows: [{id: 291989718, path: \".github/workflows/ci.yml\", name: \"CI\"}],
          runs: {\"291989718\": .runs},
          alerts: \$alerts
        } | $transform" "$CI_HISTORY" > "$out"
}

# The Windows leg of the newest (docs-only) run, and the steps inside it that were skipped.
WIN='.runs["291989718"][0].jobs[] | select(.name == "Test Suite (windows-latest)")'
WIN_SKIPPED_STEPS="$WIN"' | .steps[] | select(.conclusion == "skipped")'
RERAN_THE_TESTS="($WIN_SKIPPED_STEPS) |= (.conclusion = \"success\")"
NO_JOBS='.runs["291989718"] |= map(del(.jobs))'

SKIPPED_LEG="${TMPDIR_TEST}/2030-skipped-leg.json"
SKIPPED_LEG_RUNLEVEL="${TMPDIR_TEST}/2030-skipped-leg-runlevel.json"
ci_fixture "$SKIPPED_LEG" "$NO_ALERTS"
ci_fixture "$SKIPPED_LEG_RUNLEVEL" "$NO_ALERTS" "$NO_JOBS"

# -- FIRING. The incident itself, at the tightest possible threshold. One failure, then a
#    docs-only merge, and the sweeper called it recovered while master was still red.
if [ "$(verdict "$SKIPPED_LEG" --threshold 1)" = "ALERT" ] && [ "$(streak "$SKIPPED_LEG")" = "1" ]; then
    pass "FIRING    c2f702e9 -> 05d58a51: a success whose Windows leg was skipped is not recovery"
else
    fail "FIRING    the real #2030 history did not alert -- a red master is still cleared by a docs merge"
    printf '        got: %s\n' "$("$DETECT" --fixture "$SKIPPED_LEG" --threshold 1 2>/dev/null)"
fi

# -- CONTROL for the case above. The same two runs judged on their run-level conclusions alone,
#    which is all the detector could see before this change. If this ever starts alerting, the
#    case above has stopped being evidence of anything (SKILL 5c rule 3).
if [ "$(verdict "$SKIPPED_LEG_RUNLEVEL" --threshold 1)" = "OK" ]; then
    pass "CONTROL   the same history WITHOUT job data reads as recovery -- the run level cannot see it"
else
    fail "CONTROL   the run-level-only view alerted, so the FIRING case above proves nothing"
fi

# -- FIRING. And at the shipped threshold, not only at 1: the masked success must let the streak
#    keep accumulating through it, exactly as a cancellation does.
THREE="${TMPDIR_TEST}/2030-three.json"
THREE_RUNLEVEL="${TMPDIR_TEST}/2030-three-runlevel.json"
MORE_FAILURES='.runs["291989718"] += [{id: 1, status: "completed", event: "push", conclusion: "failure"},
                                      {id: 2, status: "completed", event: "push", conclusion: "failure"}]'
ci_fixture "$THREE" "$NO_ALERTS" "$MORE_FAILURES"
ci_fixture "$THREE_RUNLEVEL" "$NO_ALERTS" "$MORE_FAILURES | $NO_JOBS"

if [ "$(verdict "$THREE")" = "ALERT" ] && [ "$(streak "$THREE")" = "3" ]; then
    pass "FIRING    a path-filtered success does not reset a live streak (3 at the default threshold)"
else
    fail "FIRING    the streak did not survive a path-filtered success; computed '$(streak "$THREE")'"
fi
[ "$(verdict "$THREE_RUNLEVEL")" = "OK" ] \
    && pass "CONTROL   the same three failures are invisible at the run level" \
    || fail "CONTROL   the run-level view alerted on the three-failure fixture"

# -- FIRING. The half the issue title is about: the alert must not CLOSE either. A RESOLVE here
#    closes a live incident and re-arms nothing, because the next sweep sees the same green.
LIVE_ALERT='[{"number": 4242, "body": "<!-- workflow-failure-alert:291989718 -->"}]'
STILL_RED="${TMPDIR_TEST}/2030-still-red.json"
STILL_RED_RUNLEVEL="${TMPDIR_TEST}/2030-still-red-runlevel.json"
ci_fixture "$STILL_RED" "$LIVE_ALERT"
ci_fixture "$STILL_RED_RUNLEVEL" "$LIVE_ALERT" "$NO_JOBS"

[ "$(verdict "$STILL_RED")" != "RESOLVE" ] \
    && pass "FIRING    a skipped-leg green does not close an open alert while master is still red" \
    || fail "FIRING    the open alert was RESOLVEd by a docs-only merge -- the #2030 headline"
[ "$(verdict "$STILL_RED_RUNLEVEL")" = "RESOLVE" ] \
    && pass "CONTROL   the run-level view closes it, which is what happened" \
    || fail "CONTROL   the run-level view did not RESOLVE, so the case above proves nothing"

# -- FIRING. The job-level shape too. ci.yml generates every matrix leg deliberately (#1365), so
#    the real defect is a step skip inside a green job -- but a job-level `if:` is used elsewhere
#    in this repository and produces an absent or `skipped` job, which must read the same way.
GONE="${TMPDIR_TEST}/2030-job-gone.json"
JOB_SKIPPED="${TMPDIR_TEST}/2030-job-skipped.json"
ci_fixture "$GONE" "$NO_ALERTS" \
    '.runs["291989718"][0].jobs |= map(select(.name != "Test Suite (windows-latest)"))'
ci_fixture "$JOB_SKIPPED" "$NO_ALERTS" \
    "($WIN) |= (.conclusion = \"skipped\" | .steps = [])"

[ "$(verdict "$GONE" --threshold 1)" = "ALERT" ] \
    && pass "FIRING    a failing job that is absent from the next run is not a verdict on it" \
    || fail "FIRING    a job that vanished counted as recovery"
[ "$(verdict "$JOB_SKIPPED" --threshold 1)" = "ALERT" ] \
    && pass "FIRING    a failing job that reports 'skipped' in the next run is not a verdict on it" \
    || fail "FIRING    a skipped job counted as recovery"

# -- BOUNDING. The one that stops this being worse than the bug. A real recovery -- the same
#    jobs, the same steps, and the step that failed actually re-run and green -- must break the
#    streak. Without this the sweeper alerts forever on healthy workflows, and an alerter people
#    learn to ignore is an alerter that is off.
#
#    Derived from the FIRING fixture by changing only the step conclusions that were skipped, so
#    the two differ in exactly the field under test and nothing else.
RECOVERED="${TMPDIR_TEST}/2030-recovered.json"
ci_fixture "$RECOVERED" "$NO_ALERTS" "$RERAN_THE_TESTS"

if [ "$(verdict "$RECOVERED" --threshold 1)" = "OK" ] && [ "$(streak "$RECOVERED")" = "0" ]; then
    pass "BOUNDING  a genuine recovery -- the failing step re-run and green -- still breaks the streak"
else
    fail "BOUNDING  a real recovery did not break the streak; this would alert forever on a healthy workflow"
    printf '        got: %s\n' "$("$DETECT" --fixture "$RECOVERED" --threshold 1 2>/dev/null)"
fi

# -- BOUNDING. ...and it closes the alert, which is the re-arming half.
RECOVERED_ALERTED="${TMPDIR_TEST}/2030-recovered-alerted.json"
ci_fixture "$RECOVERED_ALERTED" "$LIVE_ALERT" "$RERAN_THE_TESTS"
[ "$(verdict "$RECOVERED_ALERTED")" = "RESOLVE" ] \
    && pass "BOUNDING  a genuine recovery still closes the open alert" \
    || fail "BOUNDING  a genuine recovery left the alert open -- it could only ever alert once"

# -- BOUNDING. Only the immediately-older decisive run is probed. This is what keeps the sweep
#    cheap: a workflow that is green now costs no job fetch at all, however much red is further
#    down the lookback. If this case ever goes red, the walk has started fetching jobs for runs
#    it has already settled past, and the sweep's cost is no longer two calls per broken
#    workflow.
SEPARATED="${TMPDIR_TEST}/2030-separated.json"
ci_fixture "$SEPARATED" "$NO_ALERTS" \
    '.runs["291989718"] = [.runs["291989718"][0],
                           {id: 1, status: "completed", event: "push", conclusion: "success"},
                           .runs["291989718"][1]]'
[ "$(verdict "$SEPARATED" --threshold 1)" = "OK" ] \
    && pass "BOUNDING  a failure already settled by an intervening success is not re-litigated" \
    || fail "BOUNDING  the walk probed past a settled verdict -- the sweep is no longer 2 calls"

# -- BOUNDING. No job data at all is the old answer, unchanged. Every case above section 6 is
#    written in that shape, so this pins the fallback they all depend on rather than leaving it
#    to be inferred from their passing.
[ "$(streak "$SKIPPED_LEG_RUNLEVEL")" = "0" ] \
    && pass "BOUNDING  a run carrying no job data is still judged on its run-level conclusion" \
    || fail "BOUNDING  a jobless run changed meaning -- every pre-existing fixture case is affected"

# ---------------------------------------------------------------------------------------------
#    All of the above is inert if the workflow does not run, or runs without the permissions to
#    open an issue -- and a permissions error fails in the silent direction.
# ---------------------------------------------------------------------------------------------
grep -qE '^\s+- cron:' "$WORKFLOW" \
    && pass "the alert workflow runs on a schedule" \
    || fail "the alert workflow has no cron trigger -- nothing would ever sweep"

grep -qF 'workflow_dispatch:' "$WORKFLOW" \
    && pass "the alert workflow can be run manually" \
    || fail "no workflow_dispatch -- the sweep could not be exercised on demand"

grep -qF 'scripts/detect-workflow-failure-streak.sh' "$WORKFLOW" \
    && pass "the alert workflow invokes the detector" \
    || fail "the alert workflow does not invoke scripts/detect-workflow-failure-streak.sh"

grep -qE '^\s+issues: write' "$WORKFLOW" \
    && pass "the alert workflow can write issues" \
    || fail "no 'issues: write' permission -- the alert could never be opened"

grep -qE '^\s+actions: read' "$WORKFLOW" \
    && pass "the alert workflow can read run history" \
    || fail "no 'actions: read' permission -- the sweep could not see any runs"

# The workflow must NOT have a pull_request trigger. It would then emit a check run on every PR
# for a job that reports on master's health, and worse, would count its own PR runs.
if grep -qE '^\s+pull_request' "$WORKFLOW"; then
    fail "the alert workflow has a pull_request trigger -- it is a master-health sweep, not a PR check"
else
    pass "the alert workflow is not a PR check"
fi

# The label the dedup query filters on has to be the one the workflow creates, or every sweep
# reads an empty alert list and opens duplicates.
WF_LABEL=$(grep -oE 'gh label create [a-z-]+' "$WORKFLOW" | head -1 | awk '{print $4}')
SCRIPT_LABEL=$(grep -oE 'ALERT_LABEL:-[a-z-]+' "$DETECT" | head -1 | sed 's/.*:-//')
if [ -n "$WF_LABEL" ] && [ "$WF_LABEL" = "$SCRIPT_LABEL" ]; then
    pass "the workflow creates the same label the detector queries ('$WF_LABEL')"
else
    fail "label mismatch: workflow creates '${WF_LABEL:-<none>}', detector queries '${SCRIPT_LABEL:-<none>}'"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
