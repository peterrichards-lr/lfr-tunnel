#!/usr/bin/env bash
# scripts/provision-aws-ec2.sh
# Provisions an AWS EC2 instance (key pair, security group, instance, Elastic IP) ready
# for scripts/setup-edge-vps.sh or lfr-tunnel-ops's -i flag.
# See docs/server/aws_setup_guide.md for the manual step-by-step equivalent.
set -e

# Defaults
REGION="us-east-1"
INSTANCE_TYPE="t3.micro"
NAME_TAG="lfr-tunnel-gateway"
KEY_NAME="lfr-tunnel-gateway"
KEY_PATH="$HOME/.ssh/${KEY_NAME}.pem"
AMI_ID=""
ROLE=""
PROFILE=""
IPV6="false"

usage() {
  echo "Usage: $0 --profile <aws-cli-profile> [--region <aws-region>] [--instance-type <type>] [--name-tag <name>] [--key-name <name>] [--ami-id <ami-id>] [--role central|edge] [--ipv6]"
  echo "  --profile:       AWS CLI named profile to use (required — see 'aws configure --profile <name>')."
  echo "                   This script never falls back to the ambient [default] profile, so it can't"
  echo "                   accidentally run against the wrong AWS account."
  echo "  --region:        AWS region (default: us-east-1)"
  echo "  --instance-type: EC2 instance type (default: t3.micro)"
  echo "  --name-tag:      Name tag applied to the instance/security group (default: lfr-tunnel-gateway)"
  echo "  --key-name:      EC2 key pair name to create/reuse (default: lfr-tunnel-gateway)"
  echo "  --ami-id:        Ubuntu AMI ID to launch (default: auto-lookup latest 22.04 LTS in the region)"
  echo "  --role:          Optional 'central' or 'edge' tag, so a single Project-wide Resource Group"
  echo "                   (tag:Project=lfr-tunnel) can still be filtered by role. See §7 of"
  echo "                   docs/server/aws_setup_guide.md."
  echo "  --ipv6:          Opt-in dual-stack IPv6 setup (off by default, since it modifies the VPC/subnet's"
  echo "                   networking, not just this instance). Associates an Amazon-provided IPv6 CIDR"
  echo "                   block with the VPC/subnet if not already present, assigns the instance an IPv6"
  echo "                   address, adds a ::/0 route, and opens 80/443 (not 22) to ::/0 on the security"
  echo "                   group. Matches the existing VPS's IPv6 support used for AAAA/SPF records by"
  echo "                   scripts/cloudflare-ddns.sh. See §9 of docs/server/aws_setup_guide.md."
  echo "See docs/server/aws_setup_guide.md for the manual step-by-step equivalent."
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) PROFILE="$2"; shift 2 ;;
    --region) REGION="$2"; shift 2 ;;
    --instance-type) INSTANCE_TYPE="$2"; shift 2 ;;
    --name-tag) NAME_TAG="$2"; shift 2 ;;
    --key-name) KEY_NAME="$2"; KEY_PATH="$HOME/.ssh/${KEY_NAME}.pem"; shift 2 ;;
    --ami-id) AMI_ID="$2"; shift 2 ;;
    --role) ROLE="$2"; shift 2 ;;
    --ipv6) IPV6="true"; shift ;;
    -h|--help) usage ;;
    *) echo "❌ Unknown argument: $1"; usage ;;
  esac
done

command -v aws >/dev/null 2>&1 || { echo "❌ Error: AWS CLI not found. Install it and run 'aws configure --profile <name>' first."; exit 1; }
if [ -z "$PROFILE" ]; then
  echo "❌ Error: --profile is required. This script intentionally never uses the ambient [default] AWS profile."
  usage
fi
export AWS_PROFILE="$PROFILE"
echo "=> Using AWS CLI profile: $AWS_PROFILE"

echo "=== Provisioning AWS EC2 gateway '$NAME_TAG' in $REGION ==="

# 1. Optional Liferay-internal tag overlay (git-ignored; see scripts/aws/liferay-tags.env.example).
#    Community users can ignore this entirely — the script works fine without it.
TAG_ENTRIES=("Key=Name,Value=$NAME_TAG")
[ -n "$ROLE" ] && TAG_ENTRIES+=("Key=Role,Value=$ROLE")
LIFERAY_TAGS_ENV="$(dirname "$0")/aws/liferay-tags.env"
if [ -f "$LIFERAY_TAGS_ENV" ]; then
  echo "=> Sourcing optional tag overlay: $LIFERAY_TAGS_ENV"
  # shellcheck disable=SC1090
  source "$LIFERAY_TAGS_ENV"
  [ -n "${LFR_TAG_PROJECT:-}" ] && TAG_ENTRIES+=("Key=Project,Value=$LFR_TAG_PROJECT")
  [ -n "${LFR_TAG_OWNER:-}" ] && TAG_ENTRIES+=("Key=Owner,Value=$LFR_TAG_OWNER")
  [ -n "${LFR_TAG_TEAM:-}" ] && TAG_ENTRIES+=("Key=Team,Value=$LFR_TAG_TEAM")
  [ -n "${LFR_TAG_COST_CENTER:-}" ] && TAG_ENTRIES+=("Key=CostCenter,Value=$LFR_TAG_COST_CENTER")
fi

build_tag_json() {
  local json="[" first=1
  for kv in "${TAG_ENTRIES[@]}"; do
    [ $first -eq 0 ] && json+=","
    json+="{$kv}"
    first=0
  done
  echo "${json}]"
}

# 2. Create or reuse the EC2 key pair.
#    Key pairs are region-scoped in AWS — the same --key-name in a different --region
#    produces entirely different key material. If we're about to create a NEW key pair
#    (i.e. AWS doesn't already have one by this name in THIS region) but a local file
#    already sits at $KEY_PATH, creating it would silently overwrite that file with a
#    key for a different region/instance, orphaning SSH access to whatever used it
#    before. Refuse instead, and ask for a distinct --key-name.
if aws ec2 describe-key-pairs --region "$REGION" --key-names "$KEY_NAME" >/dev/null 2>&1; then
  echo "=> Key pair '$KEY_NAME' already exists in AWS ($REGION); reusing it. Ensure $KEY_PATH matches it locally."
else
  if [ -e "$KEY_PATH" ]; then
    echo "❌ Error: AWS has no key pair named '$KEY_NAME' in $REGION, but $KEY_PATH already exists locally."
    echo "   Creating a new key pair here would overwrite that file with different region's key material,"
    echo "   breaking SSH access to whatever instance the existing file belongs to."
    echo "   Pass a distinct --key-name (e.g. --key-name ${KEY_NAME}-${REGION}) for this instance."
    exit 1
  fi
  echo "=> Creating key pair '$KEY_NAME' -> $KEY_PATH"
  aws ec2 create-key-pair --region "$REGION" --key-name "$KEY_NAME" \
    --query 'KeyMaterial' --output text > "$KEY_PATH"
  chmod 400 "$KEY_PATH"
fi

# 3. Create or reuse the security group (22/80/443 inbound, matching setup_guide.md §2.4's UFW rules)
if aws ec2 describe-security-groups --region "$REGION" --group-names "$NAME_TAG" >/dev/null 2>&1; then
  echo "=> Security group '$NAME_TAG' already exists; reusing it."
  SG_ID="$(aws ec2 describe-security-groups --region "$REGION" --group-names "$NAME_TAG" \
    --query 'SecurityGroups[0].GroupId' --output text)"
else
  echo "=> Creating security group '$NAME_TAG' (22/80/443 inbound)..."
  SG_ID="$(aws ec2 create-security-group --region "$REGION" --group-name "$NAME_TAG" \
    --description "lfr-tunneld gateway (SSH/HTTP/HTTPS)" --query 'GroupId' --output text)"
  MY_IP="$(curl -s https://ifconfig.me)/32"
  aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
    --protocol tcp --port 22 --cidr "$MY_IP" >/dev/null
  aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
    --protocol tcp --port 80 --cidr 0.0.0.0/0 >/dev/null
  aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
    --protocol tcp --port 443 --cidr 0.0.0.0/0 >/dev/null
fi
# Tag the security group with the same Name/Role/(optional overlay) tags as the
# instance, so it's included in any tag-based Resource Group query too.
aws ec2 create-tags --region "$REGION" --resources "$SG_ID" --tags "${TAG_ENTRIES[@]}" >/dev/null

# 4. Look up the latest Ubuntu 22.04 LTS AMI if one wasn't given
if [ -z "$AMI_ID" ]; then
  echo "=> Looking up latest Ubuntu 22.04 LTS AMI in $REGION..."
  AMI_ID="$(aws ec2 describe-images --region "$REGION" --owners 099720109477 \
    --filters "Name=name,Values=ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*" \
               "Name=state,Values=available" \
    --query 'sort_by(Images, &CreationDate)[-1].ImageId' --output text)"
fi

# 5. Launch the instance
echo "=> Launching $INSTANCE_TYPE instance from AMI $AMI_ID..."
INSTANCE_ID="$(aws ec2 run-instances --region "$REGION" \
  --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" \
  --key-name "$KEY_NAME" --security-group-ids "$SG_ID" \
  --tag-specifications "ResourceType=instance,Tags=$(build_tag_json)" \
  --query 'Instances[0].InstanceId' --output text)"

echo "=> Waiting for instance $INSTANCE_ID to enter running state..."
aws ec2 wait instance-running --region "$REGION" --instance-ids "$INSTANCE_ID"

# 6. Allocate and associate an Elastic IP — required so Cloudflare's DNS A records
#    (grey-clouded, per setup_guide.md §1.1) stay valid across instance stop/start.
echo "=> Allocating Elastic IP..."
ALLOC_ID="$(aws ec2 allocate-address --region "$REGION" --domain vpc \
  --query 'AllocationId' --output text)"
aws ec2 associate-address --region "$REGION" --instance-id "$INSTANCE_ID" \
  --allocation-id "$ALLOC_ID" >/dev/null
PUBLIC_IP="$(aws ec2 describe-addresses --region "$REGION" --allocation-ids "$ALLOC_ID" \
  --query 'Addresses[0].PublicIp' --output text)"

# 7. Optional IPv6 dual-stack setup (opt-in via --ipv6). Matches the existing production
#    VPS's IPv6 support (AAAA/SPF records via scripts/cloudflare-ddns.sh). Off by default
#    since it modifies the VPC/subnet's networking, not just this instance — see §9 of
#    docs/server/aws_setup_guide.md for the full rationale and manual equivalent.
IPV6_ADDR=""
if [ "$IPV6" = "true" ]; then
  echo "=> Setting up IPv6 dual-stack networking..."
  VPC_ID="$(aws ec2 describe-instances --region "$REGION" --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].VpcId' --output text)"
  SUBNET_ID="$(aws ec2 describe-instances --region "$REGION" --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].SubnetId' --output text)"
  ENI_ID="$(aws ec2 describe-instances --region "$REGION" --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].NetworkInterfaces[0].NetworkInterfaceId' --output text)"

  VPC_IPV6_CIDR="$(aws ec2 describe-vpcs --region "$REGION" --vpc-ids "$VPC_ID" \
    --query 'Vpcs[0].Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlock' --output text)"
  if [ "$VPC_IPV6_CIDR" = "None" ] || [ -z "$VPC_IPV6_CIDR" ]; then
    echo "   Associating an Amazon-provided IPv6 CIDR block with $VPC_ID..."
    aws ec2 associate-vpc-cidr-block --region "$REGION" --vpc-id "$VPC_ID" \
      --amazon-provided-ipv6-cidr-block >/dev/null
    # shellcheck disable=SC2034
    for i in $(seq 1 20); do
      VPC_IPV6_CIDR="$(aws ec2 describe-vpcs --region "$REGION" --vpc-ids "$VPC_ID" \
        --query 'Vpcs[0].Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlock' --output text)"
      STATE="$(aws ec2 describe-vpcs --region "$REGION" --vpc-ids "$VPC_ID" \
        --query 'Vpcs[0].Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlockState.State' --output text)"
      [ "$STATE" = "associated" ] && break
      sleep 3
    done
  else
    echo "   VPC $VPC_ID already has an IPv6 CIDR block ($VPC_IPV6_CIDR); reusing it."
  fi

  SUBNET_IPV6_CIDR="$(aws ec2 describe-subnets --region "$REGION" --subnet-ids "$SUBNET_ID" \
    --query 'Subnets[0].Ipv6CidrBlockAssociationSet[0].Ipv6CidrBlock' --output text)"
  if [ "$SUBNET_IPV6_CIDR" = "None" ] || [ -z "$SUBNET_IPV6_CIDR" ]; then
    # Take the first /64 out of the VPC's /56 — narrowing the prefix length is enough;
    # the address bits are already correctly zero-padded for that first subnet.
    SUBNET_IPV6_CIDR="$(echo "$VPC_IPV6_CIDR" | sed -E 's#/56#/64#')"
    echo "   Associating $SUBNET_IPV6_CIDR with subnet $SUBNET_ID..."
    aws ec2 associate-subnet-cidr-block --region "$REGION" --subnet-id "$SUBNET_ID" \
      --ipv6-cidr-block "$SUBNET_IPV6_CIDR" >/dev/null
    aws ec2 modify-subnet-attribute --region "$REGION" --subnet-id "$SUBNET_ID" \
      --assign-ipv6-address-on-creation >/dev/null
  else
    echo "   Subnet $SUBNET_ID already has an IPv6 CIDR block ($SUBNET_IPV6_CIDR); reusing it."
  fi

  IPV6_ADDR="$(aws ec2 describe-network-interfaces --region "$REGION" --network-interface-ids "$ENI_ID" \
    --query 'NetworkInterfaces[0].Ipv6Addresses[0].Ipv6Address' --output text)"
  if [ "$IPV6_ADDR" = "None" ] || [ -z "$IPV6_ADDR" ]; then
    echo "   Assigning an IPv6 address to $ENI_ID..."
    aws ec2 assign-ipv6-addresses --region "$REGION" --network-interface-id "$ENI_ID" \
      --ipv6-address-count 1 >/dev/null
    IPV6_ADDR="$(aws ec2 describe-network-interfaces --region "$REGION" --network-interface-ids "$ENI_ID" \
      --query 'NetworkInterfaces[0].Ipv6Addresses[0].Ipv6Address' --output text)"
  fi

  RTB_ID="$(aws ec2 describe-route-tables --region "$REGION" \
    --filters "Name=association.subnet-id,Values=$SUBNET_ID" \
    --query 'RouteTables[0].RouteTableId' --output text)"
  if [ "$RTB_ID" = "None" ] || [ -z "$RTB_ID" ]; then
    RTB_ID="$(aws ec2 describe-route-tables --region "$REGION" \
      --filters "Name=vpc-id,Values=$VPC_ID" "Name=association.main,Values=true" \
      --query 'RouteTables[0].RouteTableId' --output text)"
  fi
  HAS_IPV6_ROUTE="$(aws ec2 describe-route-tables --region "$REGION" --route-table-ids "$RTB_ID" \
    --query "length(RouteTables[0].Routes[?DestinationIpv6CidrBlock=='::/0'])" --output text)"
  if [ "$HAS_IPV6_ROUTE" = "0" ]; then
    IGW_ID="$(aws ec2 describe-internet-gateways --region "$REGION" \
      --filters "Name=attachment.vpc-id,Values=$VPC_ID" \
      --query 'InternetGateways[0].InternetGatewayId' --output text)"
    echo "   Adding ::/0 route via $IGW_ID to route table $RTB_ID..."
    aws ec2 create-route --region "$REGION" --route-table-id "$RTB_ID" \
      --destination-ipv6-cidr-block ::/0 --gateway-id "$IGW_ID" >/dev/null
  fi

  # Only 80/443 over IPv6 — SSH stays IPv4-only via the existing IP-restricted rule,
  # to avoid opening port 22 to the entire IPv6 internet.
  for PORT in 80 443; do
    RULE_COUNT="$(aws ec2 describe-security-group-rules --region "$REGION" \
      --filters "Name=group-id,Values=$SG_ID" \
      --query "length(SecurityGroupRules[?IsEgress==\`false\` && FromPort==\`$PORT\` && CidrIpv6=='::/0'])" \
      --output text)"
    if [ "$RULE_COUNT" = "0" ]; then
      aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$SG_ID" \
        --ip-permissions "IpProtocol=tcp,FromPort=$PORT,ToPort=$PORT,Ipv6Ranges=[{CidrIpv6=::/0}]" >/dev/null
    fi
  done

  echo "   IPv6 address: $IPV6_ADDR"
fi

echo ""
echo "=== Done ==="
echo "Instance ID: $INSTANCE_ID"
echo "Public IP:   $PUBLIC_IP (Elastic — stable across restarts)"
[ -n "$IPV6_ADDR" ] && [ "$IPV6_ADDR" != "None" ] && echo "IPv6:        $IPV6_ADDR"
echo "SSH key:     $KEY_PATH"
echo ""
echo "Next steps (see docs/server/aws_setup_guide.md §6):"
echo "  Edge node:    ./scripts/setup-edge-vps.sh -s $PUBLIC_IP -i $KEY_PATH -t <edge_token> ..."
echo "  Central node: continue manually from docs/server/setup_guide.md §2.1, using $PUBLIC_IP as YOUR_VPS_PUBLIC_IP"
