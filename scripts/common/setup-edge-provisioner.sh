#!/usr/bin/env bash
# scripts/common/setup-edge-provisioner.sh
# Deploys the optional lfr-tunnel-edge-provisioner sidecar (see issue #888,
# cmd/lfr-tunnel-edge-provisioner, pkg/provisioner) onto a central control plane
# host, giving the admin portal's Network Health screen the ability to
# start/stop/restart edge node instances and edit their EventBridge Scheduler
# schedules. Entirely optional -- if you never run this, those portal actions
# are simply absent, not an error state (see docs/server/aws_setup_guide.md §9).
set -e

# This is a generic, reusable script -- it carries no default values of its
# own (beyond --listen-port/--schedule-group, which name real defaults used
# elsewhere in this project -- see pkg/provisioner/config.go's
# DefaultScheduleGroup and this project's convention of binding the sidecar to
# 127.0.0.1:8091). Every other parameter must be supplied explicitly.
SSH_USER=""
KEY_PATH=""
SSH_KEY_ARG=""
VPS_IP=""
NODES=""
LISTEN_PORT="8091"
SCHEDULE_GROUP="lfr-tunnel-edge-nodes"
PROFILE=""
CENTRAL_REGION=""
IAM_NAME="lfr-tunnel-central-edge-provisioner"

usage() {
  echo "Usage: $0 -s <central_vps_ip> -i <identity_file> -u <ssh_user> -n <nodes> \\"
  echo "          --profile <aws-cli-profile> --region <central_region> [options]"
  echo "  -s: Central control plane VPS/EC2 public IP address (required)"
  echo "  -i: Path to SSH private key file (required)"
  echo "  -u: SSH username (required)"
  echo "  -n: Comma-separated node mappings, format <node_id>:<instance_id>:<region>"
  echo "      (required) -- <node_id> MUST match the corresponding entry's 'id' in the"
  echo "      central's server-config.yaml 'edge_nodes' list (this mapping deliberately"
  echo "      lives here, not in server-config.yaml, so the open-source core config never"
  echo "      needs to know about AWS instance IDs/regions). Example:"
  echo "      edge-us:i-0123456789abcdef0:us-east-2,edge-apac:i-0fedcba9876543210:ap-northeast-1"
  echo "  --profile:        AWS CLI named profile to use (required) -- never falls back to"
  echo "                    the ambient [default] profile."
  echo "  --region:         AWS region the CENTRAL instance itself runs in (required) --"
  echo "                    distinct from each edge node's own region in -n above. Used to"
  echo "                    look up the central's instance ID from its IP and to associate"
  echo "                    the IAM instance profile below."
  echo "  --listen-port:    Loopback port the sidecar binds to (default: 8091). Must match"
  echo "                    the port used in the central's server-config.yaml"
  echo "                    'edge_provisioner_url' (e.g. http://127.0.0.1:8091)."
  echo "  --schedule-group: EventBridge Scheduler group these nodes' schedules live in"
  echo "                    (default: lfr-tunnel-edge-nodes) -- must match"
  echo "                    scripts/common/schedule-edge-node-hours.sh's --schedule-group"
  echo "                    for GetSchedule/UpdateSchedule to find the same schedules."
  echo "  --iam-name:       Base name for the IAM role/instance-profile this script creates"
  echo "                    (default: lfr-tunnel-central-edge-provisioner). Change this only"
  echo "                    if that name is already taken by something unrelated."
  echo "See docs/server/aws_setup_guide.md §9 for background on this sidecar."
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -s) VPS_IP="$2"; shift 2 ;;
    -i)
      KEY_PATH="$2"
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY_ARG="-i $KEY_PATH"
      shift 2 ;;
    -u) SSH_USER="$2"; shift 2 ;;
    -n) NODES="$2"; shift 2 ;;
    --profile) PROFILE="$2"; shift 2 ;;
    --region) CENTRAL_REGION="$2"; shift 2 ;;
    --listen-port) LISTEN_PORT="$2"; shift 2 ;;
    --schedule-group) SCHEDULE_GROUP="$2"; shift 2 ;;
    --iam-name) IAM_NAME="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "❌ Unknown argument: $1"; usage ;;
  esac
done

if [ -z "$VPS_IP" ] || [ -z "$KEY_PATH" ] || [ -z "$SSH_USER" ] || [ -z "$NODES" ] || \
   [ -z "$PROFILE" ] || [ -z "$CENTRAL_REGION" ]; then
  echo "❌ Error: -s, -i, -u, -n, --profile, and --region are all required."
  usage
fi
export AWS_PROFILE="$PROFILE"

# Parse and validate -n once, up front -- reused below both for the IAM policy this script
# builds and for the sidecar's own config.yaml.
IFS=',' read -r -a NODE_ARRAY <<< "$NODES"
for ENTRY in "${NODE_ARRAY[@]}"; do
  IFS=':' read -r NODE_ID INSTANCE_ID REGION <<< "$ENTRY"
  if [ -z "$NODE_ID" ] || [ -z "$INSTANCE_ID" ] || [ -z "$REGION" ]; then
    echo "❌ Error: malformed -n entry '$ENTRY' -- expected <node_id>:<instance_id>:<region>"
    exit 1
  fi
done

echo "=== Deploying lfr-tunnel-edge-provisioner sidecar to $VPS_IP ==="

# 0. Create (or reuse) an IAM role/instance-profile granting exactly the AWS permissions
#    this sidecar needs, and attach it to the central instance. Without this, the sidecar
#    starts up looking healthy but every AWS call it makes fails with an opaque IMDS/
#    credentials error, surfacing only as missing data in the portal (e.g. a blank Local
#    Time column) with nothing pointing at the real cause -- this happened once already,
#    hence automating it here instead of leaving it as a manual step to remember. Safe to
#    re-run: create-or-reuse at every step.
ROLE_NAME="${IAM_NAME}-role"
PROFILE_NAME="${IAM_NAME}-profile"
POLICY_NAME="lfr-tunnel-edge-provisioner-policy"

echo "=> Resolving AWS account and central instance ID..."
ACCOUNT_ID="$(aws sts get-caller-identity --query 'Account' --output text)"
CENTRAL_INSTANCE_ID="$(aws ec2 describe-instances --region "$CENTRAL_REGION" \
  --filters "Name=ip-address,Values=$VPS_IP" "Name=instance-state-name,Values=running,stopped" \
  --query 'Reservations[0].Instances[0].InstanceId' --output text 2>/dev/null || true)"
if [ -z "$CENTRAL_INSTANCE_ID" ] || [ "$CENTRAL_INSTANCE_ID" = "None" ]; then
  echo "❌ Error: could not find an EC2 instance with public IP $VPS_IP in region $CENTRAL_REGION."
  exit 1
fi
echo "   Account: $ACCOUNT_ID, central instance: $CENTRAL_INSTANCE_ID"

echo "=> Building least-privilege IAM policy from -n node mappings..."
INSTANCE_RESOURCES="" SCHEDULE_RESOURCES="" PASSROLE_RESOURCES=""
for ENTRY in "${NODE_ARRAY[@]}"; do
  IFS=':' read -r NODE_ID INSTANCE_ID REGION <<< "$ENTRY"
  [ -n "$INSTANCE_RESOURCES" ] && INSTANCE_RESOURCES+=","
  INSTANCE_RESOURCES+="\"arn:aws:ec2:$REGION:$ACCOUNT_ID:instance/$INSTANCE_ID\""
  [ -n "$SCHEDULE_RESOURCES" ] && SCHEDULE_RESOURCES+=","
  SCHEDULE_RESOURCES+="\"arn:aws:scheduler:$REGION:$ACCOUNT_ID:schedule/$SCHEDULE_GROUP/$NODE_ID-*\""
  [ -n "$PASSROLE_RESOURCES" ] && PASSROLE_RESOURCES+=","
  PASSROLE_RESOURCES+="\"arn:aws:iam::$ACCOUNT_ID:role/lfr-tunnel-edge-scheduler-$NODE_ID-role\""
done

POLICY_TMP="/tmp/edge-provisioner-policy.json"
cat > "$POLICY_TMP" << EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {"Sid": "EdgeInstancePower", "Effect": "Allow", "Action": ["ec2:StartInstances", "ec2:StopInstances"], "Resource": [$INSTANCE_RESOURCES]},
    {"Sid": "DescribeInstancesForRestartWaiter", "Effect": "Allow", "Action": ["ec2:DescribeInstances"], "Resource": "*"},
    {"Sid": "EdgeScheduleManagement", "Effect": "Allow", "Action": ["scheduler:GetSchedule", "scheduler:UpdateSchedule"], "Resource": [$SCHEDULE_RESOURCES]},
    {"Sid": "PassEdgeSchedulerExecutionRoles", "Effect": "Allow", "Action": "iam:PassRole", "Resource": [$PASSROLE_RESOURCES], "Condition": {"StringEquals": {"iam:PassedToService": "scheduler.amazonaws.com"}}}
  ]
}
EOF

if aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
  echo "=> IAM role '$ROLE_NAME' already exists; reusing it."
else
  echo "=> Creating IAM role '$ROLE_NAME'..."
  TRUST_TMP="/tmp/edge-provisioner-trust-policy.json"
  cat > "$TRUST_TMP" << 'EOF'
{"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
EOF
  aws iam create-role --role-name "$ROLE_NAME" --assume-role-policy-document "file://$TRUST_TMP" >/dev/null
  rm -f "$TRUST_TMP"
fi

echo "=> Applying permissions policy '$POLICY_NAME' to '$ROLE_NAME' (create-or-update)..."
aws iam put-role-policy --role-name "$ROLE_NAME" --policy-name "$POLICY_NAME" --policy-document "file://$POLICY_TMP"
rm -f "$POLICY_TMP"

if aws iam get-instance-profile --instance-profile-name "$PROFILE_NAME" >/dev/null 2>&1; then
  echo "=> Instance profile '$PROFILE_NAME' already exists; reusing it."
else
  echo "=> Creating instance profile '$PROFILE_NAME' and attaching role '$ROLE_NAME'..."
  aws iam create-instance-profile --instance-profile-name "$PROFILE_NAME" >/dev/null
  aws iam add-role-to-instance-profile --instance-profile-name "$PROFILE_NAME" --role-name "$ROLE_NAME"
  echo "=> Waiting for IAM instance profile propagation..."
  sleep 10
fi

EXISTING_ASSOC="$(aws ec2 describe-iam-instance-profile-associations --region "$CENTRAL_REGION" \
  --filters "Name=instance-id,Values=$CENTRAL_INSTANCE_ID" \
  --query "IamInstanceProfileAssociations[?State=='associated'].{Id:AssociationId,Name:IamInstanceProfile.Arn}[0]" --output json 2>/dev/null || true)"
if [ -n "$EXISTING_ASSOC" ] && [ "$EXISTING_ASSOC" != "null" ]; then
  echo "=> Instance $CENTRAL_INSTANCE_ID already has an IAM instance profile associated:"
  echo "   $EXISTING_ASSOC"
  echo "   Leaving it as-is -- if it's not '$PROFILE_NAME', verify it grants equivalent permissions."
else
  echo "=> Associating instance profile '$PROFILE_NAME' with $CENTRAL_INSTANCE_ID..."
  aws ec2 associate-iam-instance-profile --region "$CENTRAL_REGION" \
    --instance-id "$CENTRAL_INSTANCE_ID" --iam-instance-profile "Name=$PROFILE_NAME" >/dev/null
  echo "=> Waiting for the association to complete..."
  until [ "$(aws ec2 describe-iam-instance-profile-associations --region "$CENTRAL_REGION" \
      --filters "Name=instance-id,Values=$CENTRAL_INSTANCE_ID" \
      --query 'IamInstanceProfileAssociations[0].State' --output text 2>/dev/null)" = "associated" ]; do
    sleep 3
  done
fi

# 1. Build Linux amd64 binary locally
VERSION="$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)"
echo "=> Compiling lfr-tunnel-edge-provisioner for Linux (amd64) with Version=$VERSION..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o bin/lfr-tunnel-edge-provisioner-linux ./cmd/lfr-tunnel-edge-provisioner

# 2. Generate the sidecar's own config.yaml locally from the -n node mappings.
#    Note: token_file is NOT created here -- GenerateOrLoadToken() creates it on the
#    sidecar's own first run, which is exactly why the systemd unit below orders this
#    service Before= lfr-tunneld.service (so the token exists before lfr-tunneld tries
#    to read it from the same path via its own edge_provisioner_token_file setting).
echo "=> Generating edge-provisioner.yaml locally..."
CONFIG_TMP="/tmp/edge-provisioner.yaml"
cat > "$CONFIG_TMP" << EOF
listen_addr: "127.0.0.1:$LISTEN_PORT"
token_file: "/etc/lfr-tunneld/edge-provisioner.token"
schedule_group: "$SCHEDULE_GROUP"
nodes:
EOF

for ENTRY in "${NODE_ARRAY[@]}"; do
  IFS=':' read -r NODE_ID INSTANCE_ID REGION <<< "$ENTRY"
  cat >> "$CONFIG_TMP" << EOF
  $NODE_ID:
    instance_id: "$INSTANCE_ID"
    region: "$REGION"
EOF
done

echo "=> Uploading edge-provisioner.yaml and binary..."
scp $SSH_KEY_ARG "$CONFIG_TMP" $SSH_USER@$VPS_IP:/home/$SSH_USER/edge-provisioner.yaml
rm -f "$CONFIG_TMP"
scp $SSH_KEY_ARG bin/lfr-tunnel-edge-provisioner-linux $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-tunnel-edge-provisioner

# 3. Remote install + systemd unit. Requires the 'lfr-tunnel' system user and
#    /etc/lfr-tunneld to already exist (i.e. run this AFTER setup-central-vps.sh, not
#    before) -- this script never creates the central's core config/systemd unit itself.
echo "=> Installing binary, config, and systemd unit on $VPS_IP..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << REMOTE_SSH
  if ! id "lfr-tunnel" &>/dev/null; then
    echo "❌ Error: system user 'lfr-tunnel' doesn't exist -- run setup-central-vps.sh first."
    exit 1
  fi

  sudo mv /home/$SSH_USER/lfr-tunnel-edge-provisioner /usr/local/bin/lfr-tunnel-edge-provisioner
  sudo chmod 755 /usr/local/bin/lfr-tunnel-edge-provisioner
  sudo chown root:root /usr/local/bin/lfr-tunnel-edge-provisioner

  if [ -f /etc/lfr-tunneld/edge-provisioner.yaml ]; then
    sudo cp /etc/lfr-tunneld/edge-provisioner.yaml /etc/lfr-tunneld/edge-provisioner.yaml.backup-\$(date +%Y-%m-%d_%H-%M-%S)
  fi
  sudo mv /home/$SSH_USER/edge-provisioner.yaml /etc/lfr-tunneld/edge-provisioner.yaml
  sudo chown lfr-tunnel:lfr-tunnel /etc/lfr-tunneld/edge-provisioner.yaml
  sudo chmod 600 /etc/lfr-tunneld/edge-provisioner.yaml

  echo "Creating systemd configuration for lfr-tunnel-edge-provisioner..."
  sudo tee /etc/systemd/system/lfr-tunnel-edge-provisioner.service > /dev/null << EOF
[Unit]
Description=Liferay Tunnel Edge Provisioner Sidecar (AWS-specific, optional)
After=network.target
Before=lfr-tunneld.service

[Service]
Type=simple
User=lfr-tunnel
Group=lfr-tunnel
WorkingDirectory=/etc/lfr-tunneld
ExecStart=/usr/local/bin/lfr-tunnel-edge-provisioner --config /etc/lfr-tunneld/edge-provisioner.yaml
Restart=on-failure
RestartSec=5s

# Security Hardening (systemd Sandboxing)
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
CapabilityBoundingSet=
ReadOnlyPaths=/usr/local/bin/lfr-tunnel-edge-provisioner
ReadWritePaths=/etc/lfr-tunneld

[Install]
WantedBy=multi-user.target
EOF

  sudo systemctl daemon-reload
  sudo systemctl enable --now lfr-tunnel-edge-provisioner
  sleep 2
  echo "=> Checking status of lfr-tunnel-edge-provisioner:"
  sudo systemctl status lfr-tunnel-edge-provisioner --no-pager

  echo "=> Restarting lfr-tunneld so it picks up the now-generated edge-provisioner.token..."
  sudo systemctl restart lfr-tunneld
  sleep 2
  sudo journalctl -u lfr-tunneld --no-pager -n 10
REMOTE_SSH

echo "=========================================================="
echo "🎉 Edge Provisioner Sidecar Deployed!"
echo "IAM role/instance-profile: $ROLE_NAME / $PROFILE_NAME (attached to $CENTRAL_INSTANCE_ID)"
echo "The admin portal's Network Health screen can now start/stop/restart these edge"
echo "nodes and edit their schedules, as long as server-config.yaml's edge_provisioner_url"
echo "points at http://127.0.0.1:$LISTEN_PORT and edge_provisioner_token_file points at"
echo "/etc/lfr-tunneld/edge-provisioner.token."
echo "=========================================================="
