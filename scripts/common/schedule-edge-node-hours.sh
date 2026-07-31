#!/usr/bin/env bash
# scripts/common/schedule-edge-node-hours.sh
# Creates (or updates) a nightly stop/start schedule for a single edge node's EC2
# instance via AWS EventBridge Scheduler, so a stateless edge node that only serves a
# geographically-concentrated audience doesn't run 24/7 for no reason. Uses
# EventBridge Scheduler's native per-schedule timezone support, so the stop/start
# times are specified and evaluated in the node's own local time -- no manual
# UTC/DST conversion required.
#
# Deliberately does NOT schedule the central control plane -- it needs to be
# reachable whenever ANY edge node in ANY region might have traffic, which in a
# multi-region deployment is effectively all the time. Don't point this script at
# the central instance.
#
# See docs/server/aws_setup_guide.md for the manual console equivalent.
set -e

PROFILE=""
REGION=""
INSTANCE_ID=""
NAME_TAG=""
TIMEZONE=""
STOP_TIME="00:00"
START_TIME="08:00"
SCHEDULE_GROUP="lfr-tunnel-edge-nodes"
ROLE_NAME="lfr-tunnel-edge-scheduler-role"

usage() {
  echo "Usage: $0 --profile <aws-cli-profile> --region <aws-region> --instance-id <i-xxxx> --name-tag <name> --timezone <IANA-tz> [options]"
  echo "  --profile:     AWS CLI named profile to use (required -- never falls back to [default])."
  echo "  --region:      AWS region the instance lives in (required)."
  echo "  --instance-id: EC2 instance ID to schedule stop/start for (required)."
  echo "  --name-tag:    Human-readable name for this node, used to name the two schedules"
  echo "                 (<name-tag>-stop / <name-tag>-start) and as an IAM policy statement ID (required)."
  echo "  --timezone:    IANA timezone name the stop/start times are evaluated in, e.g."
  echo "                 'America/Sao_Paulo', 'Asia/Kolkata' (required). This is what makes the"
  echo "                 schedule track the node's own local time across DST changes automatically."
  echo "  --stop-time:   Local HH:MM to stop the instance (default: $STOP_TIME)."
  echo "  --start-time:  Local HH:MM to start the instance (default: $START_TIME)."
  echo "  --schedule-group: EventBridge Scheduler group name to organize these schedules under"
  echo "                 (default: $SCHEDULE_GROUP, shared across all edge nodes)."
  echo "Re-running for the same --instance-id updates its existing schedules/IAM policy in place"
  echo "rather than erroring or duplicating them."
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) PROFILE="$2"; shift 2 ;;
    --region) REGION="$2"; shift 2 ;;
    --instance-id) INSTANCE_ID="$2"; shift 2 ;;
    --name-tag) NAME_TAG="$2"; shift 2 ;;
    --timezone) TIMEZONE="$2"; shift 2 ;;
    --stop-time) STOP_TIME="$2"; shift 2 ;;
    --start-time) START_TIME="$2"; shift 2 ;;
    --schedule-group) SCHEDULE_GROUP="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "❌ Unknown argument: $1"; usage ;;
  esac
done

command -v aws >/dev/null 2>&1 || { echo "❌ Error: AWS CLI not found."; exit 1; }
if [ -z "$PROFILE" ]; then
  echo "❌ Error: --profile is required. This script intentionally never uses the ambient [default] AWS profile."
  usage
fi
if [ -z "$REGION" ] || [ -z "$INSTANCE_ID" ] || [ -z "$NAME_TAG" ] || [ -z "$TIMEZONE" ]; then
  echo "❌ Error: --region, --instance-id, --name-tag, and --timezone are all required."
  usage
fi
export AWS_PROFILE="$PROFILE"
echo "=> Using AWS CLI profile: $AWS_PROFILE"

ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
INSTANCE_ARN="arn:aws:ec2:${REGION}:${ACCOUNT_ID}:instance/${INSTANCE_ID}"
echo "=== Scheduling stop/start for '$NAME_TAG' ($INSTANCE_ID, $TIMEZONE) ==="

# 1. IAM role EventBridge Scheduler assumes to call EC2 Start/StopInstances on our
#    behalf. Shared across every edge node's schedules -- created once, reused after.
TRUST_POLICY=$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "scheduler.amazonaws.com"},
    "Action": "sts:AssumeRole",
    "Condition": {"StringEquals": {"aws:SourceAccount": "$ACCOUNT_ID"}}
  }]
}
EOF
)

if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
  echo "=> IAM role '$ROLE_NAME' already exists; reusing it."
else
  echo "=> Creating IAM role '$ROLE_NAME'..."
  aws iam create-role --role-name "$ROLE_NAME" \
    --assume-role-policy-document "$TRUST_POLICY" >/dev/null
  # Newly-created IAM roles aren't immediately usable account-wide -- give it a moment
  # before EventBridge Scheduler tries to assume it further down.
  sleep 10
fi
ROLE_ARN="$(aws iam get-role --role-name "$ROLE_NAME" --query 'Role.Arn' --output text)"

# 2. Inline policy granting Start/StopInstances, scoped to exactly the instance ARNs
#    we've been asked to schedule so far -- append this instance's ARN if it isn't
#    already covered, rather than granting account-wide EC2 start/stop rights.
POLICY_NAME="lfr-tunnel-edge-scheduler-policy"
EXISTING_RESOURCES="[]"
if aws iam get-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" >/dev/null 2>&1; then
  EXISTING_RESOURCES="$(aws iam get-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" \
    --query 'PolicyDocument.Statement[0].Resource' --output json)"
fi

NEW_RESOURCES="$(python3 -c "
import json
existing = json.loads('''$EXISTING_RESOURCES''')
if isinstance(existing, str):
    existing = [existing]
arn = '$INSTANCE_ARN'
if arn not in existing:
    existing.append(arn)
print(json.dumps(existing))
")"

echo "=> Updating IAM policy '$POLICY_NAME' to cover $INSTANCE_ARN..."
POLICY_DOC=$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["ec2:StartInstances", "ec2:StopInstances"],
    "Resource": $NEW_RESOURCES
  }]
}
EOF
)
aws iam put-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" \
  --policy-document "$POLICY_DOC"

# 3. Schedule group -- pure organization, shared across every edge node.
if aws scheduler get-schedule-group --name "$SCHEDULE_GROUP" --region "$REGION" >/dev/null 2>&1; then
  echo "=> Schedule group '$SCHEDULE_GROUP' already exists; reusing it."
else
  echo "=> Creating schedule group '$SCHEDULE_GROUP'..."
  aws scheduler create-schedule-group --name "$SCHEDULE_GROUP" --region "$REGION" >/dev/null
fi

# 4. The two schedules themselves. EventBridge Scheduler's "universal target" lets a
#    schedule call an arbitrary AWS API action directly (arn:aws:scheduler:::aws-sdk:<service>:<action>)
#    without a Lambda in between. ScheduleExpressionTimezone means STOP_TIME/START_TIME
#    are evaluated in the node's own local time, including across DST -- not UTC.
create_or_update_schedule() {
  local suffix="$1" hhmm="$2" action="$3"
  local name="${NAME_TAG}-${suffix}"
  local hour="${hhmm%%:*}" minute="${hhmm##*:}"
  local cron="cron(${minute} ${hour} * * ? *)"
  local target_arn="arn:aws:scheduler:::aws-sdk:ec2:${action}"
  local input="{\"InstanceIds\":[\"${INSTANCE_ID}\"]}"

  local escaped_input="${input//\"/\\\"}"
  local target="{\"Arn\":\"$target_arn\",\"RoleArn\":\"$ROLE_ARN\",\"Input\":\"$escaped_input\"}"

  if aws scheduler get-schedule --name "$name" --group-name "$SCHEDULE_GROUP" --region "$REGION" >/dev/null 2>&1; then
    echo "=> Updating existing schedule '$name' ($hhmm $TIMEZONE)..."
    aws scheduler update-schedule --name "$name" --group-name "$SCHEDULE_GROUP" --region "$REGION" \
      --schedule-expression "$cron" \
      --schedule-expression-timezone "$TIMEZONE" \
      --flexible-time-window '{"Mode": "OFF"}' \
      --target "$target" >/dev/null
  else
    echo "=> Creating schedule '$name' ($hhmm $TIMEZONE)..."
    aws scheduler create-schedule --name "$name" --group-name "$SCHEDULE_GROUP" --region "$REGION" \
      --schedule-expression "$cron" \
      --schedule-expression-timezone "$TIMEZONE" \
      --flexible-time-window '{"Mode": "OFF"}' \
      --target "$target" >/dev/null
  fi
}

create_or_update_schedule "stop" "$STOP_TIME" "stopInstances"
create_or_update_schedule "start" "$START_TIME" "startInstances"

echo ""
echo "=== Done ==="
echo "Node:       $NAME_TAG ($INSTANCE_ID)"
echo "Stop:       $STOP_TIME $TIMEZONE daily -> ${NAME_TAG}-stop"
echo "Start:      $START_TIME $TIMEZONE daily -> ${NAME_TAG}-start"
echo "IAM role:   $ROLE_ARN (shared across all edge node schedules)"
echo ""
echo "To remove this node's schedule later:"
echo "  aws scheduler delete-schedule --name ${NAME_TAG}-stop --group-name $SCHEDULE_GROUP --region $REGION --profile $PROFILE"
echo "  aws scheduler delete-schedule --name ${NAME_TAG}-start --group-name $SCHEDULE_GROUP --region $REGION --profile $PROFILE"
echo "(the IAM policy's Resource entry for this instance ARN is left in place -- harmless if the"
echo " instance is later terminated, but remove it manually from '$POLICY_NAME' if you want to tidy up)"
