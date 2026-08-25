#!/usr/bin/env bash
# scripts/common/setup-certsync-central.sh
# Provisions the sending half of certificate distribution on the control plane (#1302).
set -e

# The control plane holds the DNS-write credential and renews the wildcards; the edges renew
# nothing. This installs the piece that carries a renewal outward, as a Certbot deploy hook --
# which fires only when a certificate actually renewed, rather than on every timer tick.
#
# What it puts on the node:
#
#   /etc/lfr-certsync/certsync.key      the distribution key, root-owned, generated here and
#                                       never leaving the node; its public half is what each
#                                       edge pins to a forced command
#   /usr/local/bin/lfr-distribute-certs bundles the live certificates and sends them
#   /usr/local/bin/lfr-power-hook-aws.sh  optional; lets a sleeping node be woken to receive
#   /etc/letsencrypt/renewal-hooks/deploy/50-lfr-distribute-certs   the trigger
#
# Run this FIRST: it prints the public key that setup-certsync-edge.sh needs on each edge.
#
# This is a generic, reusable script -- it carries no default values of its own. Every
# parameter must be supplied explicitly by the caller, which is the only place that knows the
# right values for a given deployment (#1015/#1016).

SSH_USER=""
KEY_PATH=""
SSH_KEY_ARG=""
CENTRAL_HOST=""
TARGETS=""
REGIONS=""
INSTANCE_TAG=""
DOMAINS=""
KNOWN_HOSTS_IN=""
KEY_DIR="/etc/lfr-certsync"

usage() {
  echo "Usage: $0 -s <central_host> -i <identity_file> -u <ssh_user> -t <targets> [-R <regions>] [-T <tag>] [-d <domains>]"
  echo "  -s: Control plane hostname or IP (required)"
  echo "  -i: Path to the SSH private key used to administer it (required)"
  echo "  -u: SSH username to administer with, e.g. ubuntu (required)"
  echo "  -t: Comma-separated receiving accounts, e.g."
  echo "      \"certsync@in.example.com,certsync@us.example.com\" (required)"
  echo "  -R: Comma-separated regions the EDGES live in, e.g. \"us-east-2,ap-south-1\"."
  echo "      Supplying this installs the AWS power hook, so a node that is powered off when a"
  echo "      renewal lands is started, delivered to, and put back as it was found. Without it"
  echo "      a sleeping node is skipped loudly and the hook exits non-zero."
  echo "      These are the regions the edges are in, not the one the control plane runs in."
  echo "  -T: Instance tag Key=Value to narrow the power hook's lookup (optional)"
  echo "  -d: Comma-separated certificates to distribute (optional; default is every"
  echo "      certificate in /etc/letsencrypt/live)"
  echo "  -H: File of known_hosts entries for the targets, as written by"
  echo "      setup-certsync-edge.sh -H (optional but strongly preferred). Those keys were"
  echo "      read from each node over an authenticated session; without this file the"
  echo "      control plane falls back to scanning the network for them, which trusts"
  echo "      whoever answers -- on the one connection that carries a private key."
  exit 1
}

while getopts "s:i:u:t:R:T:d:H:" opt; do
  case $opt in
    s) CENTRAL_HOST="$OPTARG" ;;
    i)
      KEY_PATH="$OPTARG"
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY_ARG="-i $KEY_PATH"
      ;;
    u) SSH_USER="$OPTARG" ;;
    t) TARGETS="$OPTARG" ;;
    R) REGIONS="$OPTARG" ;;
    T) INSTANCE_TAG="$OPTARG" ;;
    d) DOMAINS="$OPTARG" ;;
    H) KNOWN_HOSTS_IN="$OPTARG" ;;
    *) usage ;;
  esac
done

if [ -z "$CENTRAL_HOST" ] || [ -z "$KEY_PATH" ] || [ -z "$SSH_USER" ] || [ -z "$TARGETS" ]; then
  echo "❌ Error: -s, -i, -u and -t are all required."
  usage
fi

# Spaces here would not survive being passed to the power hook as one word of environment, and
# the failure would only show up months later when a sleeping node missed a renewal.
case "$REGIONS" in
  *\ *)
    echo "❌ Error: -R must be comma-separated with no spaces (got '$REGIONS')."
    exit 1
    ;;
esac

echo "=== Provisioning certificate distribution on $CENTRAL_HOST ==="

for f in lfr-distribute-certs.sh lfr-power-hook-aws.sh; do
  [ -f "scripts/common/$f" ] || { echo "❌ Error: scripts/common/$f not found; run this from the repository root."; exit 1; }
done

UPLOADED_KH=0
if [ -n "$KNOWN_HOSTS_IN" ]; then
  [ -r "$KNOWN_HOSTS_IN" ] || { echo "❌ Error: cannot read $KNOWN_HOSTS_IN"; exit 1; }
  scp $SSH_KEY_ARG "$KNOWN_HOSTS_IN" "$SSH_USER@$CENTRAL_HOST:/home/$SSH_USER/certsync-known-hosts"
  UPLOADED_KH=1
fi

echo "=> Uploading the distribution scripts..."
scp $SSH_KEY_ARG scripts/common/lfr-distribute-certs.sh "$SSH_USER@$CENTRAL_HOST:/home/$SSH_USER/lfr-distribute-certs"
scp $SSH_KEY_ARG scripts/common/lfr-power-hook-aws.sh "$SSH_USER@$CENTRAL_HOST:/home/$SSH_USER/lfr-power-hook-aws.sh"

# Normalised to spaces for the remote loops; the deploy hook keeps whatever form each consumer
# wants, which for the region list means commas.
TARGET_LIST="$(echo "$TARGETS" | tr ',' ' ')"

ssh $SSH_KEY_ARG "$SSH_USER@$CENTRAL_HOST" << REMOTE_SSH
set -e

sudo install -m 700 -o root -g root /home/$SSH_USER/lfr-distribute-certs /usr/local/bin/lfr-distribute-certs
sudo install -m 700 -o root -g root /home/$SSH_USER/lfr-power-hook-aws.sh /usr/local/bin/lfr-power-hook-aws.sh
rm -f /home/$SSH_USER/lfr-distribute-certs /home/$SSH_USER/lfr-power-hook-aws.sh

# Its own root-owned directory rather than alongside the server's configuration: that
# directory belongs to the service account, which could then replace the key that reaches
# every edge. The key itself never leaves this node -- only its public half does.
sudo install -d -m 700 -o root -g root $KEY_DIR

if sudo test -f $KEY_DIR/certsync.key; then
  echo "Distribution key already exists; keeping it."
else
  echo "Generating the distribution key..."
  # Regenerating would orphan every edge's authorized_keys at once, so this only ever runs
  # when there is no key at all.
  sudo ssh-keygen -t ed25519 -N '' -C "certsync@$CENTRAL_HOST" -f $KEY_DIR/certsync.key >/dev/null
  sudo chmod 600 $KEY_DIR/certsync.key
fi

# Host keys are pinned ahead of time, because the bundle being shipped contains a private key.
# With strict checking and no entry, delivery fails loudly; the alternative -- accepting an
# unknown host key on first contact -- would hand that private key to whoever answered.
sudo install -d -m 700 -o root -g root /root/.ssh
sudo touch /root/.ssh/known_hosts

# Keys collected from the nodes themselves take precedence over anything scanned: they were
# read over an authenticated session, so they establish identity rather than assume it.
if [ "$UPLOADED_KH" = "1" ]; then
  while IFS= read -r line; do
    [ -n "\$line" ] || continue
    sudo grep -qF "\$line" /root/.ssh/known_hosts 2>/dev/null || \
      echo "\$line" | sudo tee -a /root/.ssh/known_hosts >/dev/null
  done < /home/$SSH_USER/certsync-known-hosts
  rm -f /home/$SSH_USER/certsync-known-hosts
  echo "Installed host keys collected from the nodes."
fi

UNPINNED=""
for target in $TARGET_LIST; do
  host="\${target#*@}"
  if ! sudo ssh-keygen -F "\$host" -f /root/.ssh/known_hosts >/dev/null 2>&1; then
    echo "Pinning host key for \$host..."
    # Captured rather than piped straight to tee: a keyscan that reaches nothing exits
    # quietly with no output, and appending nothing would leave this node unpinned while the
    # provisioning run still reported success. That is the failure mode this whole mechanism
    # exists to avoid -- it would surface months later, as a missed renewal.
    # The || true is load-bearing under set -e: ssh-keyscan exits non-zero when it cannot
    # reach a host, and an unreachable node must be reported at the end rather than abort
    # provisioning of the ones that are reachable.
    SCANNED="\$(ssh-keyscan -T 10 "\$host" 2>/dev/null || true)"
    if [ -n "\$SCANNED" ]; then
      echo "\$SCANNED" | sudo tee -a /root/.ssh/known_hosts >/dev/null
    else
      UNPINNED="\$UNPINNED \$host"
    fi
  fi
done
sudo chmod 600 /root/.ssh/known_hosts

if [ -n "\$UNPINNED" ]; then
  echo "WARNING: could not reach these nodes to pin a host key:\$UNPINNED" >&2
  echo "         Delivery to them will fail until this node can open port 22 to them." >&2
  echo "         Check the receiving nodes' firewall allows SSH from this control plane." >&2
fi
REMOTE_SSH

# Built locally and uploaded rather than assembled in a remote heredoc: the values below are
# the deployment's own, and quoting them through two levels of shell is how a target list
# silently loses an entry.
HOOK_TMP="$(mktemp -t lfr-deploy-hook-XXXXXX)"
{
  echo "#!/bin/sh"
  echo "# Certbot deploy hook -- runs only when a certificate actually renewed (#1302)."
  echo "# Installed by scripts/common/setup-certsync-central.sh. Edit that, not this."
  echo "#"
  echo "# Every certificate is offered on each run, not just the renewed lineage: the installer"
  echo "# on the far side refuses anything not newer than what it already has, so a second"
  echo "# offer costs one comparison and means a node that missed an earlier renewal is caught"
  echo "# up rather than left behind."
  echo "set -e"
  echo "export LFT_EDGE_TARGETS=\"$TARGETS\""
  echo "export LFT_CERT_KEY=\"$KEY_DIR/certsync.key\""
  [ -n "$DOMAINS" ] && echo "export LFT_CERT_DOMAINS=\"$(echo "$DOMAINS" | tr ',' ' ')\""
  if [ -n "$REGIONS" ]; then
    echo "export LFT_POWER_HOOK=\"/usr/local/bin/lfr-power-hook-aws.sh\""
    if [ -n "$INSTANCE_TAG" ]; then
      echo "export LFT_POWER_HOOK_ENV=\"AWS_REGION=$REGIONS LFT_INSTANCE_TAG=$INSTANCE_TAG\""
    else
      echo "export LFT_POWER_HOOK_ENV=\"AWS_REGION=$REGIONS\""
    fi
  fi
  echo "exec /usr/local/bin/lfr-distribute-certs"
} > "$HOOK_TMP"

scp $SSH_KEY_ARG "$HOOK_TMP" "$SSH_USER@$CENTRAL_HOST:/home/$SSH_USER/50-lfr-distribute-certs"
rm -f "$HOOK_TMP"

# Numbered to run before distribution: the node that renewed should be serving the new
# certificate before it starts handing it to anyone else.
RELOAD_TMP="$(mktemp -t lfr-reload-hook-XXXXXX)"
cat > "$RELOAD_TMP" << 'RELOAD_HOOK'
#!/bin/sh
# Certbot deploy hook -- reload nginx so a renewed certificate is actually served (#1302).
# Installed by scripts/common/setup-certsync-central.sh. Edit that, not this.
#
# nginx reads its certificates once and holds them in memory. A renewal writes new files and
# changes nothing that is being served until a reload, so without this the control plane keeps
# presenting the previous certificate right up to its expiry -- the same cliff the edges had,
# on the node that does the renewing.
#
# Tested before reloading, because this fires unattended and a bad config plus a reload takes
# the node's public traffic down.
set -e
command -v nginx >/dev/null 2>&1 || exit 0
nginx -t
systemctl reload nginx
echo "[reload-nginx] reloaded; the renewed certificate is now being served"
RELOAD_HOOK

scp $SSH_KEY_ARG "$RELOAD_TMP" "$SSH_USER@$CENTRAL_HOST:/home/$SSH_USER/10-lfr-reload-nginx"
rm -f "$RELOAD_TMP"

ssh $SSH_KEY_ARG "$SSH_USER@$CENTRAL_HOST" << REMOTE_SSH
set -e
sudo install -d -m 755 -o root -g root /etc/letsencrypt/renewal-hooks/deploy
sudo install -m 700 -o root -g root /home/$SSH_USER/50-lfr-distribute-certs /etc/letsencrypt/renewal-hooks/deploy/50-lfr-distribute-certs
sudo install -m 700 -o root -g root /home/$SSH_USER/10-lfr-reload-nginx /etc/letsencrypt/renewal-hooks/deploy/10-lfr-reload-nginx
rm -f /home/$SSH_USER/50-lfr-distribute-certs /home/$SSH_USER/10-lfr-reload-nginx
echo "Deploy hooks installed."

echo
echo "=== Give this public key to each edge, via setup-certsync-edge.sh -k ==="
sudo cat $KEY_DIR/certsync.key.pub
REMOTE_SSH

echo
echo "=== Control plane provisioned ==="
echo "Next: run setup-certsync-edge.sh against every node in -t, passing the key printed above."
echo "Then prove it end to end by running the hook by hand:"
echo "  ssh $SSH_USER@$CENTRAL_HOST 'sudo /etc/letsencrypt/renewal-hooks/deploy/50-lfr-distribute-certs'"
