#!/usr/bin/env bash
#
# detect-workflow-failure-streak.sh -- alert when a master-only or scheduled workflow keeps
# failing where nobody is looking (#1730).
#
# THE PROBLEM. `Deploy Documentation` failed on every single run from 27 August to 3 September
# 2026 -- twelve consecutive failures, a week with no published documentation, and no signal to
# anyone. It runs only on a push to master and is not a required check, so its failure appears
# on no PR, in no merge, and in no required-check summary. Only the Actions tab, which nobody
# reads when nothing has told them to. #1731 fixed the docs case by building the docs on PRs
# that touch them. This covers the general shape: `release.yml`, `prioritize-issues.yml`, and
# anything added later that is master-only or cron-driven has exactly the same property.
#
# WHAT IT DOES. For every active workflow in the repository, read the recent run history,
# discard the runs whose failures are already visible somewhere (pull_request runs report on
# the PR), and count the streak of consecutive failures at the head of the list. At or above
# the threshold, open ONE issue. Not a second one while that issue is open. Close it when the
# workflow goes green again.
#
# FOUR DESIGN DECISIONS, each of which has an obvious wrong answer:
#
#   1. THRESHOLD (default 3). One failure is usually a flake -- a runner hiccup, a transient
#      network error, a rate limit -- and alerting on one trains people to ignore the alert.
#      Three consecutive is not a flake. Three is also fast enough at this repo's real
#      cadences: the docs streak above would have alerted on 27 August rather than being found
#      by accident on 3 September, and a six-hourly cron reaches three inside a day. The goal
#      is not zero latency, it is that a week-long silent failure becomes a day-long one.
#
#   2. DEDUP STATE lives in the existence of an open issue, not in a file or a cache. A poller
#      with no memory re-alerts on every sweep, gets muted within a day, and is then strictly
#      worse than no alerting at all. An issue is the state: `open` means "already alerted, say
#      nothing more", and closing it on recovery re-arms the alert for next time. No secrets, no
#      storage, no extra moving part that can itself go stale.
#
#   3. DESTINATION is a GitHub issue, labelled and assigned. The repository has no Slack webhook
#      and no mail secret (checked -- the Slack app in #909 is still blocked on workspace
#      approval), so anything else would mean inventing an infrastructure dependency for an
#      alerter whose whole job is to be more reliable than the thing it watches. An unassigned
#      alert is noise, so ALERT_ASSIGNEE is set to the repository owner by the workflow.
#
#   4. It watches ITSELF. This sweeper is a scheduled workflow, so it appears in its own
#      workflow list and alerts on its own failure streak. That covers the ordinary failure
#      modes (a bad jq filter, an API change, a permissions regression) but honestly not all of
#      them: a sweeper broken so badly it cannot create an issue cannot report that. Complete
#      self-monitoring needs a second, independent watcher, which is not worth its cost here.
#
# Exit status is 0 whether or not it alerted -- the issue is the output. A non-zero exit is
# reserved for the script itself failing to do its job, which is what makes decision 4 work.
#
# Runs on the maintainer's machine too (`make check-workflow-failures`), so it is kept to bash
# 3.2: no associative arrays, no mapfile, no ${var^^}. See AGENTS.md.

set -uo pipefail

THRESHOLD=3
LOOKBACK=30
FIXTURE=""
DRY_RUN=0
REPO="${GITHUB_REPOSITORY:-}"
ALERT_LABEL="${ALERT_LABEL:-ci-failure}"
ALERT_ASSIGNEE="${ALERT_ASSIGNEE:-}"

usage() {
    cat <<'USAGE'
Usage: detect-workflow-failure-streak.sh [options]

  --threshold N    consecutive failures before alerting (default 3)
  --lookback N     how many recent runs to read per workflow (default 30)
  --repo O/R       repository to sweep (default $GITHUB_REPOSITORY, else the gh default)
  --dry-run        report the verdicts, create and close nothing
  --fixture FILE   read workflows, runs and open alerts from a JSON file instead of the API.
                   Implies --dry-run. This is how the logic is tested offline, and how it can
                   be replayed against real history.

Prints one tab-separated verdict line per workflow:

  ALERT    streak=N  issue=-    id=<id>  name=<workflow name>
  SKIP     streak=N  issue=<n>  id=<id>  name=<workflow name>   (already alerted; stays quiet)
  RESOLVE  streak=0  issue=<n>  id=<id>  name=<workflow name>   (recovered; alert closed)
  OK       streak=N  issue=-    id=<id>  name=<workflow name>
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --threshold) THRESHOLD="${2:-}"; shift 2 ;;
        --lookback)  LOOKBACK="${2:-}"; shift 2 ;;
        --repo)      REPO="${2:-}"; shift 2 ;;
        --dry-run)   DRY_RUN=1; shift ;;
        --fixture)   FIXTURE="${2:-}"; DRY_RUN=1; shift 2 ;;
        -h|--help)   usage; exit 0 ;;
        *) echo "ERROR: unknown argument '$1'" >&2; usage >&2; exit 2 ;;
    esac
done

case "$THRESHOLD" in
    ''|*[!0-9]*) echo "ERROR: --threshold must be a positive integer, got '$THRESHOLD'" >&2; exit 2 ;;
esac
[ "$THRESHOLD" -ge 1 ] || { echo "ERROR: --threshold must be at least 1" >&2; exit 2; }
case "$LOOKBACK" in
    ''|*[!0-9]*) echo "ERROR: --lookback must be a positive integer, got '$LOOKBACK'" >&2; exit 2 ;;
esac

command -v jq >/dev/null 2>&1 || { echo "ERROR: jq is required" >&2; exit 1; }
if [ -z "$FIXTURE" ]; then
    command -v gh >/dev/null 2>&1 || { echo "ERROR: the gh CLI is required" >&2; exit 1; }
    if [ -z "$REPO" ]; then
        REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null)
        [ -n "$REPO" ] || { echo "ERROR: could not determine the repository; pass --repo" >&2; exit 1; }
    fi
elif [ ! -f "$FIXTURE" ]; then
    echo "ERROR: fixture '$FIXTURE' does not exist" >&2
    exit 1
fi

# Which runs get a vote.
#
# `pull_request` and `pull_request_target` runs are excluded because their failures are already
# reported on the pull request -- that is the blind spot this does NOT have. Everything else
# (push to master, tag push, schedule, workflow_dispatch) is a candidate. Every push trigger in
# this repository is restricted to master, so no branch filter is needed on top; if that ever
# stops being true, feature-branch failures would start counting and this filter needs a
# `head_branch` clause.
#
# Only `completed` runs vote. A run still in progress is not yet a verdict, and counting a
# queued run as anything would make the streak depend on when the sweep happened to fire.
RUN_FILTER='map(select(.status == "completed"))
            | map(select(.event != "pull_request" and .event != "pull_request_target"))
            | .[].conclusion'

# Newest-first conclusions for one workflow, one per line.
conclusions_for() {
    if [ -n "$FIXTURE" ]; then
        jq -r --arg id "$1" "(.runs[\$id] // []) | $RUN_FILTER" "$FIXTURE"
    else
        gh api "/repos/$REPO/actions/workflows/$1/runs?per_page=$LOOKBACK" \
            --jq ".workflow_runs | $RUN_FILTER"
    fi
}

# The whole threshold decision, as a pure function of a newest-first conclusion list on stdin.
# Prints "<streak> <latest decisive conclusion>".
#
# `cancelled`, `skipped`, `neutral` and `action_required` are NOT verdicts: they neither extend
# a streak nor break one. Treating a cancellation as a success would silently reset a streak
# that is still running -- and cancellations are common here, since several workflows cancel
# in-progress runs when a newer commit lands. Treating it as a failure would alert on a
# perfectly healthy workflow whose runs were superseded.
streak_of() {
    awk '
        BEGIN { streak = 0; latest = "none"; settled = 0 }
        {
            c = $0
            if (c == "" || c == "null" || c == "cancelled" || c == "skipped" \
                || c == "neutral" || c == "action_required") next
            if (latest == "none") latest = c
            if (settled) next
            if (c == "failure" || c == "timed_out" || c == "startup_failure") streak++
            else settled = 1
        }
        END { print streak, latest }
    '
}

# Dedup state, read once. An open issue carrying a workflow's marker means "already alerted".
#
# Read failures are fatal on purpose. If this returned an empty list on error, every workflow
# would look un-alerted and the sweeper would open a duplicate issue for each one on every
# sweep -- the exact spam-then-mute failure that makes an alerter worse than nothing. A loud
# failure here is caught by decision 4 above.
#
# The guard covers auth, network and rate-limit failures. It does not cover a missing label:
# `gh issue list --label <unknown>` returns `[]` and exits 0 (measured), which is harmless
# because it is also the true answer -- nothing can be alerted before the first alert exists,
# and the workflow creates the label before it ever gets that far.
ALERTS_FILE=$(mktemp -t lft-wf-alerts.XXXXXX) || exit 1
trap 'rm -f "$ALERTS_FILE"' EXIT
if [ -n "$FIXTURE" ]; then
    jq '.alerts // []' "$FIXTURE" > "$ALERTS_FILE" || exit 1
else
    if ! gh issue list --repo "$REPO" --state open --label "$ALERT_LABEL" \
        --limit 200 --json number,body > "$ALERTS_FILE" 2>/dev/null; then
        echo "ERROR: could not read open '$ALERT_LABEL' issues -- refusing to sweep, because" >&2
        echo "       without that state every workflow looks un-alerted and this would open a" >&2
        echo "       duplicate issue per workflow per sweep." >&2
        exit 1
    fi
fi

marker_for() { printf '<!-- workflow-failure-alert:%s -->' "$1"; }

open_alert_for() {
    jq -r --arg m "$(marker_for "$1")" \
        '[.[] | select((.body // "") | contains($m)) | .number] | first // empty' "$ALERTS_FILE"
}

open_alert() {
    local id="$1" name="$2" streak="$3" path="$4" body
    body="$(marker_for "$id")

\`$name\` has failed **$streak consecutive runs**. It is master-only or scheduled, so nothing
else reports this: not a pull request, not a merge, not the required-check summary.

| | |
|---|---|
| Workflow | \`$path\` |
| Consecutive failures | $streak |
| Alert threshold | $THRESHOLD |
| Runs | https://github.com/$REPO/actions/workflows/$(basename "$path") |

Opened automatically by \`.github/workflows/workflow-failure-alert.yml\` (#1730).

**This issue is the alerter's dedup state.** While it is open no further alert is raised for
this workflow, so do not close it to tidy up -- closing it re-arms the alert. It closes itself
on the next sweep after the workflow goes green again."

    if [ "$DRY_RUN" -eq 1 ]; then
        return 0
    fi
    # Assignment is best-effort. An assignee who is not a collaborator makes `gh issue create`
    # fail outright, and losing the alert entirely to fix up its metadata is the wrong trade --
    # silence is this system's failure mode.
    if [ -n "$ALERT_ASSIGNEE" ]; then
        gh issue create --repo "$REPO" --label "$ALERT_LABEL" --assignee "$ALERT_ASSIGNEE" \
            --title "CI: \`$name\` is failing on every run" --body "$body" >/dev/null 2>&1 && return 0
        echo "WARN: could not assign the alert to '$ALERT_ASSIGNEE'; opening it unassigned" >&2
    fi
    gh issue create --repo "$REPO" --label "$ALERT_LABEL" \
        --title "CI: \`$name\` is failing on every run" --body "$body" >/dev/null
}

close_alert() {
    local number="$1" name="$2"
    [ "$DRY_RUN" -eq 1 ] && return 0
    gh issue close "$number" --repo "$REPO" --reason completed \
        --comment "\`$name\` has passed again. Closing so the alert re-arms; it will reopen as a new issue if the workflow fails $THRESHOLD times in a row again." >/dev/null
}

if [ -n "$FIXTURE" ]; then
    WORKFLOWS=$(jq -r '.workflows[] | [.id, .path, .name] | @tsv' "$FIXTURE")
else
    # Dependabot's own `dynamic/` workflows are not in this repository and cannot be fixed here,
    # so alerting on them would produce an issue with no possible action.
    WORKFLOWS=$(gh api --paginate "/repos/$REPO/actions/workflows" \
        --jq '.workflows[] | select(.state == "active")
              | select(.path | startswith("dynamic/") | not)
              | [.id, .path, .name] | @tsv')
fi

if [ -z "$WORKFLOWS" ]; then
    echo "ERROR: no workflows found -- the sweep would report a clean pass over nothing" >&2
    exit 1
fi

while IFS="$(printf '\t')" read -r wf_id wf_path wf_name; do
    [ -n "$wf_id" ] || continue

    verdict=$(conclusions_for "$wf_id" | streak_of)
    streak=${verdict% *}
    latest=${verdict#* }
    existing=$(open_alert_for "$wf_id")

    if [ "$streak" -ge "$THRESHOLD" ]; then
        if [ -n "$existing" ]; then
            printf 'SKIP\tstreak=%s\tissue=%s\tid=%s\tname=%s\n' "$streak" "$existing" "$wf_id" "$wf_name"
        else
            open_alert "$wf_id" "$wf_name" "$streak" "$wf_path" || exit 1
            printf 'ALERT\tstreak=%s\tissue=-\tid=%s\tname=%s\n' "$streak" "$wf_id" "$wf_name"
        fi
    elif [ -n "$existing" ] && [ "$latest" = "success" ]; then
        close_alert "$existing" "$wf_name" || exit 1
        printf 'RESOLVE\tstreak=%s\tissue=%s\tid=%s\tname=%s\n' "$streak" "$existing" "$wf_id" "$wf_name"
    else
        printf 'OK\tstreak=%s\tissue=%s\tid=%s\tname=%s\n' "$streak" "${existing:--}" "$wf_id" "$wf_name"
    fi
done <<WORKFLOWS
$WORKFLOWS
WORKFLOWS

if [ "$DRY_RUN" -eq 1 ]; then
    echo "(dry run: nothing was created or closed)" >&2
fi
exit 0
