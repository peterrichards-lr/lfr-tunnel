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
SES_SERVICE_NAME="Amazon Simple Email Service"
CURRENCY="USD"
LINKED_ACCOUNT=""

usage() {
  echo "Usage: $0 --profile <aws-cli-profile> --monthly-budget-usd <amount> --alert-emails <email1,email2,...> [options]"
  echo "  --profile:               AWS CLI named profile to use (required — never falls back to [default])."
  echo "  --monthly-budget-usd:    Monthly budget amount for the tag:Project=<project-tag> scope (required)."
  echo "                           Despite the flag name, the actual currency is set by --currency below —"
  echo "                           pass whichever amount matches that currency, not necessarily USD."
  echo "  --alert-emails:          Comma-separated list of email addresses to notify (required)."
  echo "  --project-tag:           Tag value to scope the budget to (default: lfr-tunnel — must match"
  echo "                           Project=<value> as applied by provision-aws-ec2.sh). Ignored if"
  echo "                           --linked-account is set."
  echo "  --budget-name:           Name for the AWS Budget (default: lfr-tunnel)."
  echo "  --currency:              ISO currency code for the budget's Unit (default: USD). Must match the"
  echo "                           AWS account's actual billing currency, or the amount/percentage math"
  echo "                           won't line up with real spend. Some accounts only support USD regardless"
  echo "                           of what currency they're actually invoiced in — the CreateBudget API will"
  echo "                           reject an unsupported currency with an explicit error if so."
  echo "  --alert-threshold-pct:   Percent of the budget that triggers the ACTUAL-spend alert (default: 80)."
  echo "                           A FORECASTED-spend alert at 100% is always also created."
  echo "  --include-ses:           Fold Service=$SES_SERVICE_NAME into the SAME budget above via an OR filter"
  echo "                           expression (tag:Project=<project-tag> OR Service=$SES_SERVICE_NAME), so"
  echo "                           --monthly-budget-usd covers everything this project costs as one number —"
  echo "                           off by default since SES costs aren't resource-tagged the way EC2 is, so"
  echo "                           combining them requires the newer FilterExpression API instead of the"
  echo "                           simpler tag-only CostFilters used when this flag is omitted. Requires tag:Project"
  echo "                           to already be an ACTIVE Cost Allocation Tag — in an AWS Organizations member"
  echo "                           account, only the payer/management account can activate cost allocation tags,"
  echo "                           so this may be blocked entirely until someone with payer-account access does that."
  echo "                           Mutually exclusive with --linked-account (see below)."
  echo "  --linked-account:        Scope the budget to this AWS account ID via the LINKED_ACCOUNT dimension"
  echo "                           instead of a tag — captures EVERYTHING in that account (EC2 infra AND SES"
  echo "                           together) with no Cost Allocation Tag dependency at all, so it works even"
  echo "                           when tag activation is blocked by the payer-account restriction above. Only"
  echo "                           accurate while the account is genuinely dedicated to this project — if other"
  echo "                           unrelated work later shares the account, its spend gets swept in too, and"
  echo "                           you'd need to switch back to tag-based filtering at that point. Mutually"
  echo "                           exclusive with --include-ses (LINKED_ACCOUNT already covers SES within it)."
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
    --currency) CURRENCY="$2"; shift 2 ;;
    --alert-threshold-pct) ALERT_THRESHOLD_PCT="$2"; shift 2 ;;
    --include-ses) INCLUDE_SES="true"; shift ;;
    --linked-account) LINKED_ACCOUNT="$2"; shift 2 ;;
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
if [ -n "$LINKED_ACCOUNT" ] && [ "$INCLUDE_SES" = "true" ]; then
  echo "❌ Error: --linked-account and --include-ses are mutually exclusive — LINKED_ACCOUNT already covers"
  echo "   all Service spend (including SES) within that account, so --include-ses would be redundant."
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
#    API equivalent) or the calls below will return an empty/error response. In an AWS
#    Organizations MEMBER account, cost allocation tags can only be viewed/activated from
#    the payer/management account -- this step will just find nothing here, not error,
#    if that's the situation (use --linked-account instead in that case).
if [ -n "$LINKED_ACCOUNT" ]; then
  echo "=> Skipping cost allocation tag activation — not needed for LINKED_ACCOUNT-based filtering."
else
echo "=> Checking which cost-allocation tags AWS has discovered so far..."
CANDIDATE_TAGS=("Project" "Role" "Owner" "CostCenter")
DISCOVERED="$(aws ce list-cost-allocation-tags --type UserDefined --query 'CostAllocationTags[].TagKey' --output text 2>/dev/null || true)"
if [ -z "$DISCOVERED" ]; then
  echo "⚠️  No user-defined cost allocation tags visible yet (or Cost Explorer isn't enabled for this account,"
  echo "   or this is an AWS Organizations member account — cost allocation tags are payer-account-only there)."
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

# 3. Build the budget's filter: LINKED_ACCOUNT if that mode is on (captures everything
#    in the account, no tag dependency); tag:Project OR Service=SES via FilterExpression
#    if --include-ses was passed (CostFilters can't OR across two different filter
#    dimensions, so combining them needs the newer, mutually-exclusive-with-CostFilters
#    FilterExpression instead); plain tag-only CostFilters otherwise.
if [ -n "$LINKED_ACCOUNT" ]; then
  FILTER_FIELD="\"CostFilters\": {\"LinkedAccount\": [\"$LINKED_ACCOUNT\"]}"
  FILTER_DESCRIPTION="LinkedAccount=$LINKED_ACCOUNT (accurate only while this account is dedicated to the project)"
elif [ "$INCLUDE_SES" = "true" ]; then
  echo "=> Verifying '$SES_SERVICE_NAME' is a billing Service name AWS recognizes for this account..."
  TODAY_START="$(date -u +%Y-%m-01 2>/dev/null || true)"
  TODAY_END="$(date -u +%Y-%m-%d 2>/dev/null || true)"
  if [ -n "$TODAY_START" ] && [ -n "$TODAY_END" ] && [ "$TODAY_START" != "$TODAY_END" ]; then
    SES_DIMENSION_MATCH="$(aws ce get-dimension-values --dimension SERVICE \
      --time-period "Start=$TODAY_START,End=$TODAY_END" --search-string "Email" \
      --query "DimensionValues[?Value=='$SES_SERVICE_NAME'].Value" --output text 2>/dev/null || true)"
    if [ -z "$SES_DIMENSION_MATCH" ]; then
      echo "⚠️  Cost Explorer hasn't recorded any billed usage matching '$SES_SERVICE_NAME' yet this month"
      echo "   (expected if SES is brand new here) — proceeding anyway. If SES spend never shows up in this"
      echo "   budget, double check the exact Service name via:"
      echo "   aws ce get-dimension-values --dimension SERVICE --time-period Start=$TODAY_START,End=$TODAY_END --search-string Email"
    fi
  fi

  FILTER_FIELD="\"FilterExpression\": {\"Or\": [{\"Tags\": {\"Key\": \"Project\", \"Values\": [\"$PROJECT_TAG\"]}}, {\"Dimensions\": {\"Key\": \"SERVICE\", \"Values\": [\"$SES_SERVICE_NAME\"]}}]}"
  FILTER_DESCRIPTION="tag Project=$PROJECT_TAG OR Service=$SES_SERVICE_NAME"
else
  FILTER_FIELD="\"CostFilters\": {\"TagKeyValue\": [\"user:Project\$$PROJECT_TAG\"]}"
  FILTER_DESCRIPTION="tag Project=$PROJECT_TAG"
fi

# 4. Create (or skip, if one already exists) the AWS Budget.
echo "=> Checking for existing budget '$BUDGET_NAME'..."
if aws budgets describe-budget --account-id "$ACCOUNT_ID" --budget-name "$BUDGET_NAME" >/dev/null 2>&1; then
  echo "=> Budget '$BUDGET_NAME' already exists; leaving it as-is. Edit it in the Budgets console if you need"
  echo "   to change the amount, threshold, filter, or subscriber list."
else
  echo "=> Creating budget '$BUDGET_NAME': $MONTHLY_BUDGET_USD $CURRENCY/month, scoped to $FILTER_DESCRIPTION..."

  BUDGET_JSON="$(cat <<EOF
{
  "BudgetName": "$BUDGET_NAME",
  "BudgetType": "COST",
  "TimeUnit": "MONTHLY",
  "BudgetLimit": {"Amount": "$MONTHLY_BUDGET_USD", "Unit": "$CURRENCY"},
  $FILTER_FIELD
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

echo ""
echo "=== Done ==="
echo "Account:  $ACCOUNT_ID"
echo "Budget:   $BUDGET_NAME ($MONTHLY_BUDGET_USD $CURRENCY/month, scoped to $FILTER_DESCRIPTION, alerts at"
echo "          ${ALERT_THRESHOLD_PCT}% actual / 100% forecasted)"
echo ""
echo "One remaining manual step (Cost Explorer has no API for saved/bookmarked reports):"
echo "  1. Open https://console.aws.amazon.com/costmanagement/home#/cost-explorer"
if [ -n "$LINKED_ACCOUNT" ]; then
  echo "  2. Filter: Linked Account -> $LINKED_ACCOUNT."
  echo "     Revisit this filter once anything unrelated starts sharing this account -- it stops being an"
  echo "     accurate view of just this project's spend at that point."
else
  echo "  2. Group by: Tag -> Project. Filter: Tag -> Project -> $PROJECT_TAG."
  if [ "$INCLUDE_SES" = "true" ]; then
    echo "     Cost Explorer's own console filters are AND'd across dimensions (unlike the Budget above, which"
    echo "     uses an OR), so a single saved report can't show both at once -- save a SECOND report filtered on"
    echo "     Service -> $SES_SERVICE_NAME to see SES spend, as the practical equivalent of one combined view."
  fi
fi
echo "  3. Click 'Save as' to bookmark it as a dashboard-style saved report."
echo "See docs/server/aws_setup_guide.md §8 for the full walkthrough."
