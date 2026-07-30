# AWS EC2 Provisioning Guide for `lfr-tunnel`

This guide covers the AWS-specific steps needed to provision a bare Ubuntu instance
suitable for running `lfr-tunneld` — as either the central control plane or a regional
edge node. It is a **supplement**, not a replacement, for
[Control Plane Setup](setup_guide.md) and [Edge Node Scaling](edge_setup_guide.md): once
your instance exists, is reachable over SSH, and has a stable public IP, every step from
that point on (DNS, Nginx, Let's Encrypt, systemd, `lfr-tunneld` configuration) is
identical regardless of hosting provider.

`lfr-tunnel` does not require AWS — DigitalOcean, Hetzner, and Linode remain equally
supported, per the provider list in [`setup_guide.md` §2](setup_guide.md#2-vps-server-setup--security-hardening).
This guide exists because AWS has a few provisioning steps that other providers handle
differently or don't require at all, most importantly the Elastic IP step below.

> [!IMPORTANT]
> **Use a named AWS CLI profile, not the ambient `[default]` one.** Configure a
> profile dedicated to this project rather than relying on whatever `aws configure`
> writes to `[default]` on your machine:
> ```bash
> aws configure --profile lfr-tunnel
> ```
> `scripts/common/provision-aws-ec2.sh` requires `--profile <name>` explicitly and refuses to
> run without it — it will never silently fall back to `[default]`, so it can't
> accidentally provision resources against the wrong AWS account.
>
> The manual `aws ec2 ...` commands shown below assume you've exported
> `AWS_PROFILE=lfr-tunnel` for the session (or add `--profile lfr-tunnel` to each one).

---

## 1. Choosing an AMI, Instance Type & Region

> [!NOTE]
> **Region choice isn't just about latency.** The central control plane's SQLite
> database stores real user registration/auth data (see
> [`infosec.md` §4](../infosec.md#4-gateway-governance--data-plane-risk-controls)), so if
> your deployment is subject to GDPR or similar data-residency requirements, provision
> the central control plane in a region within that jurisdiction (e.g. an EU-based
> organization would use an EU AWS region). Stateless edge nodes hold no persistent data,
> so this concern applies to the central control plane specifically, not edge nodes.

Launch an instance using the standard **Canonical Ubuntu Server 22.04 LTS** or
**24.04 LTS** AMI — the same OS versions required by
[`setup_guide.md` §2](setup_guide.md#2-vps-server-setup--security-hardening). Using the
official Canonical AMI keeps the default SSH user as `ubuntu` — pass `-u ubuntu` to
`scripts/common/setup-edge-vps.sh` (it has no default of its own; every flag is required)
if you use `scripts/common/provision-aws-ec2.sh` (§6) or that script directly.

For instance sizing, `t3.micro` (2 vCPU burstable, 1GB RAM) is a safe default for either
role. `t3.nano` can run a lightweight edge node (stateless, no SQLite DB, no Postfix) but
is undersized for a central control plane running Nginx, `lfr-tunneld`, and a mail relay
together.

---

## 2. Key Pair

Create a key pair (or reuse an existing one) and download the private key:

```bash
aws ec2 create-key-pair \
  --key-name lfr-tunnel-gateway \
  --query 'KeyMaterial' \
  --output text > ~/.ssh/lfr-tunnel-gateway.pem
chmod 400 ~/.ssh/lfr-tunnel-gateway.pem
```

This file is used directly as the identity file for the existing SSH-based tooling — no
code changes are needed on the `lfr-tunnel` side:
- `lfr-tunnel-ops deploy -i ~/.ssh/lfr-tunnel-gateway.pem`
- `scripts/common/setup-edge-vps.sh -i ~/.ssh/lfr-tunnel-gateway.pem ...`

> [!WARNING]
> **Key pairs are region-scoped.** The same `--key-name` in two different regions is two
> different keys with different material — reusing the default name across a central
> gateway and multiple edge nodes in other regions means each region's key would
> overwrite the same local `~/.ssh/lfr-tunnel-gateway.pem` file. Use a distinct
> `--key-name` (and therefore a distinct local file) per region, e.g.
> `--key-name lfr-tunnel-gateway-us-east-2`. `scripts/common/provision-aws-ec2.sh` refuses to
> overwrite an existing local key file for this reason — pass a distinct `--key-name`
> per region/instance.

---

## 3. Security Group

Mirror the UFW rules already documented in
[`setup_guide.md` §2.4](setup_guide.md#24-configure-the-firewall-ufw) and configured
automatically by `scripts/common/setup-edge-vps.sh`:

| Port | Protocol | Source    | Purpose                          |
|------|----------|-----------|-----------------------------------|
| 22   | TCP      | Your IP(s) | SSH administration               |
| 80   | TCP      | 0.0.0.0/0  | HTTP (Let's Encrypt ACME, redirects) |
| 443  | TCP      | 0.0.0.0/0  | HTTPS (tunnel traffic, dashboard) |

```bash
aws ec2 create-security-group \
  --group-name lfr-tunnel-gateway \
  --description "lfr-tunneld gateway (SSH/HTTP/HTTPS)"

aws ec2 authorize-security-group-ingress --group-name lfr-tunnel-gateway \
  --protocol tcp --port 22 --cidr "$(curl -s https://ifconfig.me)/32"
aws ec2 authorize-security-group-ingress --group-name lfr-tunnel-gateway \
  --protocol tcp --port 80 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-name lfr-tunnel-gateway \
  --protocol tcp --port 443 --cidr 0.0.0.0/0
```

> [!NOTE]
> Restricting port 22 to your own IP here is in addition to, not instead of, the
> SSH-hardening steps in `setup_guide.md` §2.3 (key-only auth, no root login) — keep both.

---

## 4. Launch the Instance

```bash
aws ec2 run-instances \
  --image-id <ubuntu-22.04-or-24.04-ami-id-for-your-region> \
  --instance-type t3.micro \
  --key-name lfr-tunnel-gateway \
  --security-groups lfr-tunnel-gateway \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=lfr-tunnel-gateway}]'
```

Look up the current Ubuntu AMI ID for your region via the
[Canonical AMI locator](https://cloud-images.ubuntu.com/locator/ec2/) or
`aws ec2 describe-images --owners 099720109477 ...`.

---

## 5. Allocate an Elastic IP

> [!IMPORTANT]
> **This step is required, not optional.** A plain EC2 instance's public IP changes
> whenever the instance stops/starts. `lfr-tunnel`'s DNS setup
> ([`setup_guide.md` §1.1](setup_guide.md#11-required-dns-records)) points A records
> (root, `tunnel`, and the wildcard `*`) directly at a fixed IP with Cloudflare's proxy
> disabled (grey-clouded) — there is no dynamic-DNS layer in front of those records by
> default. Without an Elastic IP, any instance stop/start would silently break every
> active tunnel and the wildcard cert's DNS-01 validation.

```bash
aws ec2 allocate-address --domain vpc
aws ec2 associate-address --instance-id <instance-id> --allocation-id <allocation-id>
```

Use the resulting Elastic IP as `YOUR_VPS_PUBLIC_IP` throughout
[`setup_guide.md` §1.1](setup_guide.md#11-required-dns-records) and
[`edge_setup_guide.md` §3](edge_setup_guide.md#3-dns-configuration).

---

## 6. Continue with the Standard Setup

Once the instance is running, has an associated Elastic IP, and you can SSH into it as
`ubuntu` with the key pair from §2:

- **Central control plane**: continue from
  [`setup_guide.md` §2.1](setup_guide.md#21-basic-os--package-updates) onward — nothing
  else in that guide is AWS-specific.
- **Regional edge node**: continue with [`edge_setup_guide.md`](edge_setup_guide.md), or
  run `scripts/common/setup-edge-vps.sh -s <elastic-ip> -i ~/.ssh/lfr-tunnel-gateway.pem -u ubuntu ...`
  directly (the Canonical AMI's default SSH user).

`scripts/common/provision-aws-ec2.sh` automates steps 2–5 above (key pair, security group,
instance launch, Elastic IP) and prints the resulting IP and key path in a form ready to
pass straight to `scripts/common/setup-edge-vps.sh` or `lfr-tunnel-ops`'s `-i` flag:

```bash
./scripts/common/provision-aws-ec2.sh --profile lfr-tunnel --region us-east-1 --instance-type t3.micro --name-tag my-gateway --key-name my-gateway --role central
```

---

## 7. Liferay Internal Notes (Optional)

The following applies to Liferay's own AWS account and cost-management practices. It is
**not required** to use `lfr-tunnel` on AWS — community deployments can skip this section
entirely.

- **The central control plane MUST be provisioned in a UK or EU AWS region** — e.g.
  `eu-west-2` (London) or `eu-west-1` (Ireland) — for GDPR compliance and data
  sovereignty. This is a hard requirement for Liferay's deployment, not just a
  recommendation, since the central node's database holds real developer registration
  and auth data. It also matches the UK-control-plane / US-edge-node architecture already
  used as the example throughout
  [`edge_setup_guide.md` §1](edge_setup_guide.md#1-architectural-overview). Regional edge
  nodes hold no persistent data and can be provisioned wherever latency dictates (e.g.
  `us-east-1` for a US edge node) — `--region` is always required by
  `scripts/common/provision-aws-ec2.sh` (it has no default), so pass the right region explicitly
  for each node you provision.
- Prefer `t3.micro` for the central control plane; `t3.nano` is acceptable for
  low-traffic edge regions where cost matters more than headroom.
- Tag every resource (instance, security group, Elastic IP) with `Project=lfr-tunnel`,
  `Owner`, and a cost-center tag for AWS Cost Explorer/Cost Allocation Reports.
  `scripts/common/provision-aws-ec2.sh` will source `scripts/liferay/aws/liferay-tags.env` if present
  (see `scripts/liferay/aws/liferay-tags.env.example`) and apply those tags automatically — this
  file is git-ignored, so Liferay's actual account-specific values never need to be
  committed to this OSS repo.
- **Viewing the whole fleet across regions.** Every instance (central control plane and
  every regional edge node) shares the same `Project=lfr-tunnel` tag, but
  [AWS Resource Groups](https://console.aws.amazon.com/resource-groups/) are
  **region-scoped** — a group created in one region only lists resources in that same
  region, even with an `AWS::AllSupported` tag-based query (confirmed empirically:
  `list-group-resources` against a different region returns "group does not exist").
  There are two practical options:
  - **For a genuine single cross-region view**, use
    [**Tag Editor**](https://console.aws.amazon.com/resource-groups/tag-editor) with
    Region set to "All regions" and search `Project = lfr-tunnel` — this is a
    console-side aggregation across every region's endpoint, not a single API-level
    group.
  - **For a named Resource Group per region** (useful if you're already working within
    one region's console), create a **region-suffixed** group name (e.g.
    `lfr-tunnel-eu-west-1`, `lfr-tunnel-us-east-2`) in each region you provision into —
    do **not** reuse the identical name `lfr-tunnel` across regions: a Resource Group's
    ARN embeds its region (`arn:aws:resource-groups:<region>:...:group/<name>`), and
    same-named groups in different regions are otherwise indistinguishable when
    switching `--region` context, which surfaces as a confusing "Region in ARN not
    valid" error the moment a group's ARN from one region is used against another.
    `scripts/common/provision-aws-ec2.sh` does not create these automatically, so run
    `aws resource-groups create-group --region <region> --name lfr-tunnel-<region> ...`
    once per region if you want it.
  Pass `--role central` or `--role edge` to `provision-aws-ec2.sh` to additionally tag
  each instance's role, so either view can still be filtered (e.g. "edge nodes only").
- Set up an [AWS Budget](https://console.aws.amazon.com/billing/home#/budgets) alert per
  environment so unexpected usage (e.g. a forgotten test instance) surfaces quickly.

---

## 8. Cost Dashboard

`scripts/common/setup-aws-cost-dashboard.sh` automates the two API-reachable pieces of
the cost-visibility setup mentioned in §7: activating the `Project`/`Role`/`Owner`/
`CostCenter` tags as **Cost Allocation Tags**, and creating an **AWS Budget** scoped to
`tag:Project=<project-tag>` with email alerts at a configurable percentage of actual
spend and 100% of forecasted spend.

```bash
./scripts/common/setup-aws-cost-dashboard.sh --profile lfr-tunnel \
  --monthly-budget-usd 50 --alert-emails you@example.com,other@example.com
```

> [!NOTE]
> **Cost Explorer must already be enabled for the account** — a one-time manual toggle
> under [Billing Preferences](https://console.aws.amazon.com/costmanagement/home#/cost-explorer)
> with no API equivalent — before any of the above will work. Newly-applied tags also
> take up to 24h to become discoverable/activatable after their first billed use.

**Including SES sending costs.** Amazon SES's billing line item isn't resource-tagged
the way EC2 instances/Elastic IPs are, so it can't share the `tag:Project` budget above.
Pass `--include-ses --ses-monthly-budget-usd <amount>` to also create a second budget
scoped to `Service=Amazon Simple Email Service`:

```bash
./scripts/common/setup-aws-cost-dashboard.sh --profile lfr-tunnel \
  --monthly-budget-usd 50 --alert-emails you@example.com \
  --include-ses --ses-monthly-budget-usd 10
```

The one part this script can't automate: Cost Explorer's grouped/filtered **reports**
have no public creation API, so saving one as a reusable "dashboard" is a manual,
one-time console step:

1. Open [Cost Explorer](https://console.aws.amazon.com/costmanagement/home#/cost-explorer).
2. Group by **Tag → Project**; filter **Tag → Project → `<project-tag>`**.
3. Click **Save as** to bookmark it — this is the persistent dashboard view going
   forward, reusable across regions since billing data itself isn't region-scoped (unlike
   the Resource Groups caveat in §7).
4. If `--include-ses` was used, save a **second** report filtered on
   **Service → Amazon Simple Email Service** — Cost Explorer ANDs filters across
   different dimensions, so a single report can't show "tag:Project OR Service:SES"
   together; two saved reports is the practical equivalent of one combined dashboard.

---

## 9. Tearing Down / Retesting

`scripts/common/deprovision-aws-ec2.sh` is the companion to `provision-aws-ec2.sh`: it releases
the Elastic IP, terminates the instance, and removes the security group for a given
`--name-tag`, so you can cleanly retry a provisioning run without hunting through the AWS
Console for leftover (billable) resources.

```bash
./scripts/common/deprovision-aws-ec2.sh --profile lfr-tunnel --region us-east-1 --name-tag my-gateway
```

> [!IMPORTANT]
> **Releasing the Elastic IP changes the public IP.** If DNS records already point at it
> (per [`setup_guide.md` §1.1](setup_guide.md#11-required-dns-records) or
> [`edge_setup_guide.md` §3](edge_setup_guide.md#3-dns-configuration)), tunnels and
> wildcard cert renewal will break until you re-provision and update those DNS records to
> the new Elastic IP. This is the right tool for cleaning up a test/retest cycle before
> DNS is pointed at the instance — treat it as destructive once an environment is live.

The EC2 key pair is **not** deleted by default, since the same key is often reused across
both the central gateway and edge nodes (see `--key-name` in §2). Pass
`--delete-key-pair` explicitly if you also want the AWS-side key pair removed (the local
`.pem` file on disk is never touched).

---

## 10. IPv6 Dual-Stack Support (Optional)

The existing production VPS is dual-stack — `scripts/liferay/vm6/cloudflare-ddns.sh` actively
maintains AAAA records and folds the IPv6 address into the SPF record whenever one is
present. To preserve that on AWS, pass `--ipv6` to `provision-aws-ec2.sh`:

```bash
./scripts/common/provision-aws-ec2.sh --profile lfr-tunnel --region eu-west-1 --instance-type t3.micro --name-tag my-gateway --key-name my-gateway --role central --ipv6
```

This is **opt-in, not the default** — unlike the rest of what the script does, it
modifies the VPC/subnet's networking (not just adding a new resource), so it only runs
when explicitly requested. When passed, it:

1. Associates an Amazon-provided IPv6 `/56` CIDR block with the instance's VPC, if one
   isn't already there.
2. Associates a `/64` subnet out of that block with the instance's subnet, and enables
   auto-assign-IPv6 for future instances launched into it.
3. Assigns the instance's network interface an IPv6 address. Unlike the IPv4 Elastic IP,
   **no "Elastic IPv6" is needed** — an IPv6 address assigned this way is already stable
   for the life of the network interface across instance stop/start.
4. Adds a `::/0` route to the subnet's route table via the VPC's internet gateway (this
   doesn't happen automatically just from associating the CIDR block).
5. Opens **80/443 only** to `::/0` on the security group — deliberately **not port 22**,
   so SSH stays reachable only via the existing IP-restricted IPv4 rule rather than being
   exposed to the entire IPv6 internet.

All five steps are idempotent (safe to run again, e.g. when provisioning a second
instance into a VPC/subnet that already has IPv6 set up from a previous run) and covered
by companion checks in the script rather than a single irreversible action.

`scripts/common/deprovision-aws-ec2.sh` does not need any IPv6-specific cleanup: the VPC/subnet
CIDR association and route are free and harmless to leave in place for future instances,
and the instance's own IPv6 address is released automatically when the instance
terminates (no billable "floating" IPv6 concept the way Elastic IPs work for IPv4).

If you add the equivalent AAAA DNS records afterward (same pattern as
[`setup_guide.md` §1.1](setup_guide.md#11-required-dns-records)), you get the same
dual-stack behavior the current production VPS already has.


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-30* | *Last Reviewed: 2026-07-30*
