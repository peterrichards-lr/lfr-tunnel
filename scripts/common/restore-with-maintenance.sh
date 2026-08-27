#!/usr/bin/env bash
# restore-with-maintenance.sh — Safely coordinate lfr-tunneld restore with Nginx maintenance mode
#
# Usage:
#   sudo ./scripts/common/restore-with-maintenance.sh [backup_file]
#

set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$*"; exit 1; }

# ── Preflight checks ─────────────────────────────────────────────────────────
[[ $EUID -eq 0 ]] || die "This script must be run as root (or via sudo)."

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Coordinate Maintenance Mode ──────────────────────────────────────────────
info "Entering maintenance window..."
"${SCRIPT_DIR}/enable-maintenance.sh" "Database Restore" "database restoration/optimization" "180"


# Ensure we always clean up and disable maintenance mode on script exit
cleanup() {
    info "Exiting maintenance window..."
    # Withdraw the drain first, or clients keep migrating away from a gateway that is staying
    # up. Ordered before disable-maintenance.sh so the announcement is gone by the time new
    # connections are let back in.
    if [ -x "${SCRIPT_DIR}/drain-and-wait.sh" ]; then
        "${SCRIPT_DIR}/drain-and-wait.sh" clear || true
    fi
    "${SCRIPT_DIR}/disable-maintenance.sh"
}
trap cleanup EXIT INT TERM

# ── Drain attached tunnels ───────────────────────────────────────────────────
# Maintenance mode above stops NEW connections arriving. It does nothing about the ones already
# attached, and a restore stops the gateway underneath them -- so without this they are dropped
# outright, which measured 24m36s of downtime for a client against none at all for one that
# moved on a warning (#1246).
#
# This script predates the drain concept by about a month (it exists since 2026-07-29; drain
# landed 2026-08-24 in #1305), so it never had anything to call. Not a judgement call that
# restores may drop tunnels -- just the older of the two (#1455).
#
# Guarded on existence and never fatal, matching how deploy treats it: a box provisioned before
# this script shipped must still be able to restore.
if [ -x "${SCRIPT_DIR}/drain-and-wait.sh" ]; then
    info "Announcing the restart to attached tunnels..."
    "${SCRIPT_DIR}/drain-and-wait.sh" announce 45 90 "Gateway is restarting for a database restore" || true
else
    warn "drain-and-wait.sh not found; attached tunnels will be dropped rather than moved."
fi

# ── Run database restore ──────────────────────────────────────────────────────
info "Initiating database restore..."
# Forward all arguments (like backup file path) to the main restore script
# Since restore-backup.sh is interactive if no arguments are provided, it will prompt correctly.
"${SCRIPT_DIR}/restore-backup.sh" "$@"

success "Database restore coordinate sequence completed successfully!"
echo ""
