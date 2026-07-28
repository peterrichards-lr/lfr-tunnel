#!/usr/bin/env bash
# scripts/deprovision-aws-ec2.sh
# Tears down what scripts/provision-aws-ec2.sh created: releases the Elastic IP,
# terminates the instance, and removes the security group.
# See docs/server/aws_setup_guide.md §8.
set -e

# Defaults
REGION="us-east-1"
NAME_TAG=""
PROFILE=""
DELETE_KEY_PAIR="false"
KEY_NAME=""

usage() {
  echo "Usage: $0 --profile <aws-cli-profile> --name-tag <name> [--region <aws-region>] [--delete-key-pair] [--key-name <name>]"
  echo "  --profile:         AWS CLI named profile to use (required — never falls back to [default])."
  echo "  --name-tag:        Name tag of the instance/security group to tear down (required — must match"
  echo "                     the --name-tag originally passed to provision-aws-ec2.sh)."
  echo "  --region:          AWS region (default: us-east-1)"
  echo "  --delete-key-pair: Also delete the EC2 key pair (off by default — it may be shared by another"
  echo "                     instance, e.g. the central gateway and an edge node reusing the same key)."
  echo "  --key-name:        Key pair name to delete if --delete-key-pair is set (default: lfr-tunnel-gateway)"
  echo ""
  echo "!! WARNING: This terminates the EC2 instance and releases its Elastic IP. If DNS records"
  echo "!! (Cloudflare A records) point at that IP, they WILL break until you re-provision and update"
  echo "!! DNS to the new IP. See docs/server/aws_setup_guide.md §8."
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) PROFILE="$2"; shift 2 ;;
    --name-tag) NAME_TAG="$2"; shift 2 ;;
    --region) REGION="$2"; shift 2 ;;
    --delete-key-pair) DELETE_KEY_PAIR="true"; shift ;;
    --key-name) KEY_NAME="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "❌ Unknown argument: $1"; usage ;;
  esac
done

command -v aws >/dev/null 2>&1 || { echo "❌ Error: AWS CLI not found."; exit 1; }
if [ -z "$PROFILE" ]; then
  echo "❌ Error: --profile is required. This script never uses the ambient [default] AWS profile."
  usage
fi
if [ -z "$NAME_TAG" ]; then
  echo "❌ Error: --name-tag is required, so the right instance gets torn down."
  usage
fi
export AWS_PROFILE="$PROFILE"

echo "=> Using AWS CLI profile: $AWS_PROFILE"
echo "!! WARNING: Tearing down '$NAME_TAG' in $REGION — this terminates the instance and releases its Elastic IP !!"

# 1. Find any non-terminated instance(s) tagged with this Name
INSTANCE_IDS="$(aws ec2 describe-instances --region "$REGION" \
  --filters "Name=tag:Name,Values=$NAME_TAG" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[].Instances[].InstanceId' --output text)"

if [ -z "$INSTANCE_IDS" ]; then
  echo "=> No active instances found tagged Name=$NAME_TAG in $REGION."
else
  for INSTANCE_ID in $INSTANCE_IDS; do
    echo "=> Releasing any Elastic IP(s) associated with $INSTANCE_ID..."
    ALLOC_IDS="$(aws ec2 describe-addresses --region "$REGION" \
      --filters "Name=instance-id,Values=$INSTANCE_ID" \
      --query 'Addresses[].AllocationId' --output text)"
    for ALLOC_ID in $ALLOC_IDS; do
      ASSOC_ID="$(aws ec2 describe-addresses --region "$REGION" --allocation-ids "$ALLOC_ID" \
        --query 'Addresses[0].AssociationId' --output text)"
      if [ -n "$ASSOC_ID" ] && [ "$ASSOC_ID" != "None" ]; then
        aws ec2 disassociate-address --region "$REGION" --association-id "$ASSOC_ID" >/dev/null
      fi
      aws ec2 release-address --region "$REGION" --allocation-id "$ALLOC_ID"
      echo "   Released Elastic IP allocation $ALLOC_ID"
    done

    echo "=> Terminating instance $INSTANCE_ID..."
    aws ec2 terminate-instances --region "$REGION" --instance-ids "$INSTANCE_ID" >/dev/null
  done

  echo "=> Waiting for termination to complete..."
  # shellcheck disable=SC2086
  aws ec2 wait instance-terminated --region "$REGION" --instance-ids $INSTANCE_IDS
fi

# 2. Remove the security group (only possible once the instance is fully terminated)
if aws ec2 describe-security-groups --region "$REGION" --group-names "$NAME_TAG" >/dev/null 2>&1; then
  echo "=> Deleting security group '$NAME_TAG'..."
  aws ec2 delete-security-group --region "$REGION" --group-name "$NAME_TAG"
else
  echo "=> No security group named '$NAME_TAG' found; skipping."
fi

# 3. Optionally remove the key pair (off by default — it may be shared across multiple instances).
#    The local .pem file on disk is never touched by this script.
if [ "$DELETE_KEY_PAIR" = "true" ]; then
  KEY_NAME="${KEY_NAME:-lfr-tunnel-gateway}"
  echo "=> Deleting key pair '$KEY_NAME' from AWS (local .pem file left untouched)..."
  aws ec2 delete-key-pair --region "$REGION" --key-name "$KEY_NAME" || echo "   (already gone or not found)"
fi

echo ""
echo "=== Teardown complete for '$NAME_TAG' in $REGION ==="
