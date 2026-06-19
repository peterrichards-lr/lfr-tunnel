#!/usr/bin/env bash
# disable-maintenance.sh — Disable Nginx maintenance mode for lfr-tunnel
#
# Usage:
#   sudo ./scripts/disable-maintenance.sh
#

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
TRIGGER_FILE="${LFT_MAINTENANCE_TRIGGER:-/var/lib/lfr-tunneld/maintenance.enable}"

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$*"; exit 1; }

# ── Preflight checks ─────────────────────────────────────────────────────────
[[ $EUID -eq 0 ]] || die "This script must be run as root (or via sudo)."

# ── Disable Maintenance Mode ─────────────────────────────────────────────────
if [[ -f "$TRIGGER_FILE" ]]; then
    rm -f "$TRIGGER_FILE"
    success "Maintenance mode DISABLED. Nginx will now forward requests to the server."
else
    info "Maintenance mode was not active (trigger file not found)."
fi
echo ""
