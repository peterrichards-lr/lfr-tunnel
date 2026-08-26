#!/usr/bin/env bash
# scripts/common/setup-certsync-edge.sh
# Provisions the receiving half of certificate distribution on one edge node (#1302).
set -e

# The scripts that carry a renewed certificate outward already existed; nothing created the
# accounts and permissions they need, so an edge would keep serving its previous certificate
# until it expired. This installs that missing half, and is safe to re-run.
#
# What it puts on the node:
#
#   certsync                         unprivileged account, the only thing the control-plane
#                                    key may log in as
#   /usr/local/bin/lfr-receive-certs forced command for that key: stdin -> staging directory
#   /usr/local/bin/lfr-install-certs the one root command certsync may run; validates and
#                                    installs, then reloads nginx
#   /etc/sudoers.d/lfr-certsync      that single grant, and nothing else
#   /var/lib/lfr-certs/staging       where a delivered bundle lands before it is trusted
#
# This is a generic, reusable script -- it carries no default values of its own. Every
# parameter must be supplied explicitly by the caller, which is the only place that knows the
# right values for a given deployment (#1015/#1016).

SSH_USER=""
KEY_PATH=""
SSH_KEY_ARG=""
VPS_HOST=""
PUBKEY=""
INSTALL_ROOT="/etc/lfr-tunneld/certs"
KNOWN_HOSTS_OUT=""

usage() {
  echo "Usage: $0 -s <edge_host> -i <identity_file> -u <ssh_user> -k <control_plane_public_key>"
  echo "  -s: Edge hostname or IP to provision (required)"
  echo "  -i: Path to the SSH private key used to administer that node (required)"
  echo "  -u: SSH username to administer with, e.g. ubuntu (required)"
  echo "  -k: The control plane's certsync PUBLIC key, in full, quoted (required)."
  echo "      setup-certsync-central.sh prints this. It is pinned to a forced command, so"
  echo "      the only thing it can ever do on this node is offer a certificate bundle."
  echo "  -K: Read that public key from a file instead of the command line (optional)."
  echo "  -r: Directory nginx reads certificates from (optional, default $INSTALL_ROOT)."
  echo "      Must match the ssl_certificate paths in this node's nginx config."
  echo "  -H: Append this node's SSH host key, in known_hosts form, to the named file"
  echo "      (optional). Feed that file to setup-certsync-central.sh -H so the control"
  echo "      plane knows this node's key before it ever ships a private key to it."
  exit 1
}

while getopts "s:i:u:k:K:r:H:" opt; do
  case $opt in
    s) VPS_HOST="$OPTARG" ;;
    i)
      KEY_PATH="$OPTARG"
      # Deliberate literal tilde (#1366): this matches a user-supplied path that BEGINS with
      # "~/" so the next line can expand it against $HOME. Using $HOME in the pattern would
      # match the already-expanded form instead, which is not what arrives here.
      # shellcheck disable=SC2088
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY_ARG="-i $KEY_PATH"
      ;;
    u) SSH_USER="$OPTARG" ;;
    k) PUBKEY="$OPTARG" ;;
    K) PUBKEY="$(cat "$OPTARG")" ;;
    r) INSTALL_ROOT="$OPTARG" ;;
    H) KNOWN_HOSTS_OUT="$OPTARG" ;;
    *) usage ;;
  esac
done

if [ -z "$VPS_HOST" ] || [ -z "$KEY_PATH" ] || [ -z "$SSH_USER" ] || [ -z "$PUBKEY" ]; then
  echo "❌ Error: -s, -i, -u and one of -k/-K are all required."
  usage
fi

# A private key here would be an operator error worth stopping for: it would be copied to the
# node and left in an authorized_keys file, readable by the certsync account.
case "$PUBKEY" in
  ssh-ed25519\ *|ssh-rsa\ *|ecdsa-sha2-*\ *) ;;
  *)
    echo "❌ Error: -k does not look like an SSH public key (expected 'ssh-ed25519 AAAA...')."
    echo "   Pass the PUBLIC key that setup-certsync-central.sh printed, not a private key."
    exit 1
    ;;
esac

echo "=== Provisioning certsync on $VPS_HOST ==="

for f in lfr-receive-certs.sh lfr-install-certs.sh; do
  [ -f "scripts/common/$f" ] || { echo "❌ Error: scripts/common/$f not found; run this from the repository root."; exit 1; }
done

echo "=> Uploading the receiving scripts..."
scp $SSH_KEY_ARG scripts/common/lfr-receive-certs.sh "$SSH_USER@$VPS_HOST:/home/$SSH_USER/lfr-receive-certs"
scp $SSH_KEY_ARG scripts/common/lfr-install-certs.sh "$SSH_USER@$VPS_HOST:/home/$SSH_USER/lfr-install-certs"

# Deliberate mixed heredoc (#1366): local values such as $SSH_USER are interpolated
# here on purpose, and every remote-side variable is escaped as \$var so it expands on
# the server. Quoting the delimiter would stop the local half the script depends on.
# shellcheck disable=SC2087
ssh $SSH_KEY_ARG "$SSH_USER@$VPS_HOST" << REMOTE_SSH
set -e

# A real shell, not /bin/false: the account never logs in interactively, but sshd still needs a
# shell to run the forced command with, and a nologin shell makes every delivery fail.
if ! id certsync >/dev/null 2>&1; then
  echo "Creating the certsync account..."
  sudo useradd --system --create-home --home-dir /var/lib/lfr-certs --shell /bin/bash certsync
else
  echo "certsync already exists; leaving it alone."
fi

sudo install -d -m 750 -o certsync -g certsync /var/lib/lfr-certs
sudo install -d -m 700 -o certsync -g certsync /var/lib/lfr-certs/staging

# Root-owned and not writable by certsync. The receiving account must not be able to edit the
# privileged half it is allowed to invoke.
sudo install -m 755 -o root -g root /home/$SSH_USER/lfr-receive-certs /usr/local/bin/lfr-receive-certs
sudo install -m 755 -o root -g root /home/$SSH_USER/lfr-install-certs /usr/local/bin/lfr-install-certs
rm -f /home/$SSH_USER/lfr-receive-certs /home/$SSH_USER/lfr-install-certs

# Written to a temporary file and validated before being put in place: a malformed file in
# sudoers.d breaks sudo for every user on the node, including the operator's own recovery path.
sudo tee /tmp/lfr-certsync.sudoers > /dev/null << 'SUDOERS'
certsync ALL=(root) NOPASSWD: /usr/local/bin/lfr-install-certs
SUDOERS
if sudo visudo -c -f /tmp/lfr-certsync.sudoers >/dev/null 2>&1; then
  sudo install -m 440 -o root -g root /tmp/lfr-certsync.sudoers /etc/sudoers.d/lfr-certsync
  sudo rm -f /tmp/lfr-certsync.sudoers
  echo "Installed the sudoers grant."
else
  sudo rm -f /tmp/lfr-certsync.sudoers
  echo "ERROR: refusing to install an invalid sudoers file." >&2
  exit 1
fi

# restrict disables pty, port, agent and X11 forwarding; the forced command means whatever the
# caller asks for is ignored. Rewritten wholesale rather than appended to, so re-running cannot
# accumulate stale keys -- an old control-plane key left behind here would still be honoured.
sudo install -d -m 700 -o certsync -g certsync /var/lib/lfr-certs/.ssh
sudo tee /var/lib/lfr-certs/.ssh/authorized_keys > /dev/null << 'AUTHKEYS'
restrict,command="/usr/local/bin/lfr-receive-certs" $PUBKEY
AUTHKEYS
sudo chown certsync:certsync /var/lib/lfr-certs/.ssh/authorized_keys
sudo chmod 600 /var/lib/lfr-certs/.ssh/authorized_keys

sudo install -d -m 700 -o root -g root $INSTALL_ROOT

# Prove it works rather than prove it is configured. Running the privileged half through the
# sudoers grant, as certsync, with an empty staging directory exercises the whole local path --
# the grant, the script, and reading this node's own domains out of its config -- and installs
# nothing, because there is nothing staged. A node that cannot do this would have failed at
# renewal instead, months from now, with an expired certificate.
echo "Self-test: invoking the privileged installer as certsync..."
if sudo -u certsync sudo -n /usr/local/bin/lfr-install-certs; then
  echo "Self-test passed."
else
  echo "ERROR: certsync could not run the installer; distribution would fail at renewal." >&2
  exit 1
fi
REMOTE_SSH

# Read from the node over the session just used to administer it, rather than scanned off the
# network. What is about to be shipped here is a private key, so trusting whoever answers on
# port 22 the first time is the one moment an impostor would aim for; this closes it, and it
# does not depend on the control plane being able to reach the node yet.
if [ -n "$KNOWN_HOSTS_OUT" ]; then
  HOSTKEY="$(ssh $SSH_KEY_ARG -o BatchMode=yes "$SSH_USER@$VPS_HOST" \
      'cat /etc/ssh/ssh_host_ed25519_key.pub 2>/dev/null' | awk '{print $1" "$2}')"
  if [ -n "$HOSTKEY" ]; then
    touch "$KNOWN_HOSTS_OUT"
    # Keyed on the name the control plane will actually connect to.
    grep -qF "$VPS_HOST $HOSTKEY" "$KNOWN_HOSTS_OUT" 2>/dev/null || \
      echo "$VPS_HOST $HOSTKEY" >> "$KNOWN_HOSTS_OUT"
    echo "=> Recorded this node's host key in $KNOWN_HOSTS_OUT"
  else
    echo "⚠️  Could not read this node's host key; the control plane will refuse to deliver to it." >&2
  fi
fi

echo "=== certsync provisioned on $VPS_HOST ==="
