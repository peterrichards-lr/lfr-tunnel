#!/usr/bin/env bash
# scripts/common/setup-aws-cost-dashboard.sh
# Sets up AWS-native cost visibility for the tags already applied by
# provision-aws-ec2.sh (Project/Role/Owner/CostCenter): activates them as Cost
# Allocation Tags and creates a Budget scoped to Project=<project-tag> with email
# alerts. See docs/server/aws_setup_guide.md §7/§8 for the manual console step this
# script can't do for you (saving the actual Cost Explorer report).
set -e

# This is a generic, reusable script -- it carries no default values of its own
# (beyond --project-tag/--budget-name, which name a real default used elsewhere in this
# project's tagging scheme, and --alert-threshold-pct, a genuine tunable). Every other
# parameter must be supplied explicitly by the caller.
PROFILE=""
MONTHLY_BUDGET_USD=""
ALERT_EMAILS=""
PROJECT_TAG="lfr-tunnel"
BUDGET_NAME="lfr-tunnel"
ALERT_THRESHOLD_PCT="80"
INCLUDE_SES="false"
SES_MONTHLY_BUDGET_USD=""
SES_BUDGET_NAME="lfr-tunnel-ses"
SES_SERVICE_NAME="Amazon Simple Email Service"

usage() {
  echo "Usage: $0 --profile <aws-cli-profile> --monthly-budget-usd <amount> --alert-emails <email1,email2,...> [options]"
  echo "  --profile:               AWS CLI named profile to use (required — never falls back to [default])."
  echo "  --monthly-budget-usd:    Monthly USD budget amount for the tag:Project=<project-tag> scope (required)."
  echo "  --alert-emails:          Comma-separated list of email addresses to notify (required)."
  echo "  --project-tag:           Tag value to scope the budget to (default: lfr-tunnel — must match"
  echo "                           Project=<value> as applied by provision-aws-ec2.sh)."
  echo "  --budget-name:           Name for the AWS Budget (default: lfr-tunnel)."
  echo "  --alert-threshold-pct:   Percent of the budget that triggers the ACTUAL-spend alert (default: 80)."
  echo "                           A FORECASTED-spend alert at 100% is always also created."
  echo "  --include-ses:           Also create a second budget scoped to Service=$SES_SERVICE_NAME"
  echo "                           (off by default — SES costs aren't resource-tagged the way EC2 is,"
  echo "                           so they can't share the tag-scoped budget above; this adds a separate"
  echo "                           one instead of migrating that budget off its existing filter format)."
  echo "  --ses-monthly-budget-usd: Monthly USD budget amount for the SES-scoped budget"
  echo "                           (required if --include-ses is set)."
  echo "  --ses-budget-name:       Name for the SES-scoped AWS Budget (default: lfr-tunnel-ses)."
  echo "See docs/server/aws_setup_guide.md §8 for the manual console step this script can't automate:"
  echo "saving a Cost Explorer report grouped by tag:Project (Cost Explorer has no public API for"
  echo "saved/bookmarked reports)."
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) PROFILE="$2"; shift 2 ;;
    --monthly-budget-usd) MONTHLY_BUDGET_USD="$2"; shift 2 ;;
    --alert-emails) ALERT_EMAILS="$2"; shift 2 ;;
    --project-tag) PROJECT_TAG="$2"; shift 2 ;;
    --budget-name) BUDGET_NAME="$2"; shift 2 ;;
    --alert-threshold-pct) ALERT_THRESHOLD_PCT="$2"; shift 2 ;;
    --include-ses) INCLUDE_SES="true"; shift ;;
    --ses-monthly-budget-usd) SES_MONTHLY_BUDGET_USD="$2"; shift 2 ;;
    --ses-budget-name) SES_BUDGET_NAME="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "❌ Unknown argument: $1"; usage ;;
  esac
done

command -v aws >/dev/null 2>&1 || { echo "❌ Error: AWS CLI not found. Install it and run 'aws configure --profile <name>' first."; exit 1; }
if [ -z "$PROFILE" ]; then
  echo "❌ Error: --profile is required. This script never uses the ambient [default] AWS profile."
  usage
fi
if [ -z "$MONTHLY_BUDGET_USD" ] || [ -z "$ALERT_EMAILS" ]; then
  echo "❌ Error: --monthly-budget-usd and --alert-emails are both required."
  usage
fi
if [ "$INCLUDE_SES" = "true" ] && [ -z "$SES_MONTHLY_BUDGET_USD" ]; then
  echo "❌ Error: --ses-monthly-budget-usd is required when --include-ses is set."
  usage
fi
export AWS_PROFILE="$PROFILE"
echo "=> Using AWS CLI profile: $AWS_PROFILE"

ACCOUNT_ID="$(aws sts get-caller-identity --query 'Account' --output text)"
echo "=> Target account: $ACCOUNT_ID"

# 1. Activate cost allocation tags. These only become activatable once AWS has
#    observed them on at least one billed resource — provision-aws-ec2.sh applies all
#    four to every instance/security group it creates, but activation can take up to
#    24h to reflect a brand-new tag. Cost Explorer itself must already be enabled for
#    the account (a one-time manual console toggle under Billing Preferences with no
#    API equivalent) or the calls below will return an empty/error response.
echo "=> Checking which cost-allocation tags AWS has discovered so far..."
CANDIDATE_TAGS=("Project" "Role" "Owner" "CostCenter")
DISCOVERED="$(aws ce list-cost-allocation-tags --type UserDefined --query 'CostAllocationTags[].TagKey' --output text 2>/dev/null || true)"
if [ -z "$DISCOVERED" ]; then
  echo "⚠️  No user-defined cost allocation tags visible yet (or Cost Explorer isn't enabled for this account)."
  echo "   Enable it once at https://console.aws.amazon.com/costmanagement/home#/cost-explorer, then re-run this script."
else
  TO_ACTIVATE=()
  for TAG in "${CANDIDATE_TAGS[@]}"; do
    if echo "$DISCOVERED" | tr '\t' '\n' | grep -qx "$TAG"; then
      TO_ACTIVATE+=("TagKey=$TAG,Status=Active")
    else
      echo "   Skipping '$TAG' — not yet observed on a billed resource, so AWS won't accept activating it."
    fi
  done
  if [ "${#TO_ACTIVATE[@]}" -gt 0 ]; then
    echo "=> Activating cost allocation tags: ${TO_ACTIVATE[*]}"
    aws ce update-cost-allocation-tags-status --cost-allocation-tags-status "${TO_ACTIVATE[@]}"
  fi
fi

# 2. Build the shared subscriber/notification payload (80%-actual + 100%-forecasted),
#    reused by every budget this script creates below.
build_subscribers_json() {
  local json="[" first=1
  local IFS=','
  for EMAIL in $ALERT_EMAILS; do
    [ $first -eq 0 ] && json+=","
    json+="{\"SubscriptionType\":\"EMAIL\",\"Address\":\"$EMAIL\"}"
    first=0
  done
  echo "${json}]"
}
SUBSCRIBERS_JSON="$(build_subscribers_json)"

notifications_json() {
  local threshold_pct="$1"
  cat <<EOF
[
  {
    "Notification": {"NotificationType": "ACTUAL", "ComparisonOperator": "GREATER_THAN", "Threshold": $threshold_pct, "ThresholdType": "PERCENTAGE"},
    "Subscribers": $SUBSCRIBERS_JSON
  },
  {
    "Notification": {"NotificationType": "FORECASTED", "ComparisonOperator": "GREATER_THAN", "Threshold": 100, "ThresholdType": "PERCENTAGE"},
    "Subscribers": $SUBSCRIBERS_JSON
  }
]
EOF
}

# 3. Create (or skip, if one already exists) an AWS Budget scoped to tag:Project.
echo "=> Checking for existing budget '$BUDGET_NAME'..."
if aws budgets describe-budget --account-id "$ACCOUNT_ID" --budget-name "$BUDGET_NAME" >/dev/null 2>&1; then
  echo "=> Budget '$BUDGET_NAME' already exists; leaving it as-is. Edit it in the Budgets console if you need"
  echo "   to change the amount, threshold, or subscriber list."
else
  echo "=> Creating budget '$BUDGET_NAME': \$$MONTHLY_BUDGET_USD/month, scoped to tag Project=$PROJECT_TAG..."

  BUDGET_JSON="$(cat <<EOF
{
  "BudgetName": "$BUDGET_NAME",
  "BudgetType": "COST",
  "TimeUnit": "MONTHLY",
  "BudgetLimit": {"Amount": "$MONTHLY_BUDGET_USD", "Unit": "USD"},
  "CostFilters": {"TagKeyValue": ["user:Project\$$PROJECT_TAG"]}
}
EOF
)"

  aws budgets create-budget \
    --account-id "$ACCOUNT_ID" \
    --budget "$BUDGET_JSON" \
    --notifications-with-subscribers "$(notifications_json "$ALERT_THRESHOLD_PCT")"
  echo "=> Budget created. AWS will email each subscriber a confirmation link that must be accepted"
  echo "   before alerts start delivering."
fi

# 4. Optionally, a second Budget scoped to Service=$SES_SERVICE_NAME — SES's own line
#    item isn't resource-tagged the way EC2/Elastic IPs are, so it can't share the
#    tag:Project filter above; this is a separate budget rather than a filter merge.
if [ "$INCLUDE_SES" = "true" ]; then
  echo "=> Verifying '$SES_SERVICE_NAME' is a billing Service name AWS recognizes for this account..."
  TODAY_START="$(date -u +%Y-%m-01 2>/dev/null || true)"
  TODAY_END="$(date -u +%Y-%m-%d 2>/dev/null || true)"
  if [ -n "$TODAY_START" ] && [ -n "$TODAY_END" ] && [ "$TODAY_START" != "$TODAY_END" ]; then
    SES_DIMENSION_MATCH="$(aws ce get-dimension-values --dimension SERVICE \
      --time-period "Start=$TODAY_START,End=$TODAY_END" --search-string "Email" \
      --query "DimensionValues[?Value=='$SES_SERVICE_NAME'].Value" --output text 2>/dev/null || true)"
    if [ -z "$SES_DIMENSION_MATCH" ]; then
      echo "⚠️  Cost Explorer hasn't recorded any billed usage matching '$SES_SERVICE_NAME' yet this month"
      echo "   (expected if SES is brand new here) — proceeding anyway. If the SES budget below shows \$0"
      echo "   forever, double check the exact Service name via:"
      echo "   aws ce get-dimension-values --dimension SERVICE --time-period Start=$TODAY_START,End=$TODAY_END --search-string Email"
    fi
  fi

  echo "=> Checking for existing budget '$SES_BUDGET_NAME'..."
  if aws budgets describe-budget --account-id "$ACCOUNT_ID" --budget-name "$SES_BUDGET_NAME" >/dev/null 2>&1; then
    echo "=> Budget '$SES_BUDGET_NAME' already exists; leaving it as-is."
  else
    echo "=> Creating budget '$SES_BUDGET_NAME': \$$SES_MONTHLY_BUDGET_USD/month, scoped to Service=$SES_SERVICE_NAME..."

    SES_BUDGET_JSON="$(cat <<EOF
{
  "BudgetName": "$SES_BUDGET_NAME",
  "BudgetType": "COST",
  "TimeUnit": "MONTHLY",
  "BudgetLimit": {"Amount": "$SES_MONTHLY_BUDGET_USD", "Unit": "USD"},
  "CostFilters": {"Service": ["$SES_SERVICE_NAME"]}
}
EOF
)"

    aws budgets create-budget \
      --account-id "$ACCOUNT_ID" \
      --budget "$SES_BUDGET_JSON" \
      --notifications-with-subscribers "$(notifications_json "$ALERT_THRESHOLD_PCT")"
    echo "=> SES budget created. AWS will email each subscriber a confirmation link that must be accepted"
    echo "   before alerts start delivering."
  fi
fi

echo ""
echo "=== Done ==="
echo "Account:  $ACCOUNT_ID"
echo "Budget:   $BUDGET_NAME (\$$MONTHLY_BUDGET_USD/month, tag Project=$PROJECT_TAG, alerts at ${ALERT_THRESHOLD_PCT}% actual / 100% forecasted)"
if [ "$INCLUDE_SES" = "true" ]; then
  echo "Budget:   $SES_BUDGET_NAME (\$$SES_MONTHLY_BUDGET_USD/month, Service=$SES_SERVICE_NAME, alerts at ${ALERT_THRESHOLD_PCT}% actual / 100% forecasted)"
fi
echo ""
echo "One remaining manual step (Cost Explorer has no API for saved/bookmarked reports):"
echo "  1. Open https://console.aws.amazon.com/costmanagement/home#/cost-explorer"
echo "  2. Group by: Tag -> Project. Filter: Tag -> Project -> $PROJECT_TAG."
if [ "$INCLUDE_SES" = "true" ]; then
  echo "     To see SES in the same report, add a second Filter: Service -> $SES_SERVICE_NAME (Cost Explorer"
  echo "     filters across different dimensions are AND'd, so this needs its own filter combination/report"
  echo "     rather than one that includes both EC2-tagged and SES costs together)."
fi
echo "  3. Click 'Save as' to bookmark it as a dashboard-style saved report."
echo "See docs/server/aws_setup_guide.md §8 for the full walkthrough."
