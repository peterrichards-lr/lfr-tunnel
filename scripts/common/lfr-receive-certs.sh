#!/usr/bin/env bash
set -euo pipefail

# Receives a certificate bundle on stdin and hands it to the privileged installer (#1302).
#
# This is the entire command the distribution key may run. It is pinned as a forced command in
# the receiving account's authorized_keys, so whatever the caller asks for is ignored:
#
#   restrict,command="/usr/local/bin/lfr-receive-certs" ssh-ed25519 AAAA...central
#
# `restrict` disables pty, port, agent and X11 forwarding. Between that and the forced command,
# a key that leaks from the control plane buys an attacker exactly one thing: the ability to
# offer this node a certificate bundle, which lfr-install-certs then validates before trusting.
#
# Runs as an unprivileged account. It cannot write to the install directory or reload nginx --
# it stages, then calls the one root command it is permitted:
#
#   /etc/sudoers.d/lfr-certsync:
#     certsync ALL=(root) NOPASSWD: /usr/local/bin/lfr-install-certs

STAGING="${LFT_CERT_STAGING:-/var/lib/lfr-certs/staging}"
MAX_BYTES="${LFT_CERT_MAX_BYTES:-1048576}"

log() { echo "[receive-certs] $*"; }

# SSH_ORIGINAL_COMMAND is deliberately ignored rather than inspected. Reading it would invite
# the idea that some values are honoured, and none are.
if [ -n "${SSH_ORIGINAL_COMMAND:-}" ]; then
    log "ignoring requested command; this key may only deliver certificates"
fi

rm -rf "${STAGING:?}"
mkdir -p "$STAGING"
chmod 700 "$STAGING"

# Bounded, so a wedged or hostile sender cannot fill the disk on a node nobody is watching.
# head closes the pipe at the limit; the tar below then fails on truncated input rather than
# silently unpacking half a bundle.
if ! head -c "$MAX_BYTES" | tar -xz \
        --no-same-owner --no-same-permissions \
        --anchored --exclude='*/..*' \
        -C "$STAGING" 2>/dev/null; then
    log "ERROR: could not unpack the delivered bundle" >&2
    rm -rf "${STAGING:?}"/*
    exit 1
fi

# Paths from the archive are never trusted. The installer reads fixed filenames from one level
# of directories, so anything deeper or oddly named is dropped here rather than reasoned about
# there.
find "$STAGING" -mindepth 3 -delete 2>/dev/null || true
find "$STAGING" -mindepth 1 -maxdepth 1 ! -type d -delete 2>/dev/null || true

log "staged $(find "$STAGING" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ') bundle(s)"

sudo /usr/local/bin/lfr-install-certs
