#!/usr/bin/env bash
# restore-with-maintenance.sh — Safely coordinate lfr-tunneld restore with Nginx maintenance mode
#
# Usage:
#   sudo ./scripts/restore-with-maintenance.sh [backup_file]
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
    "${SCRIPT_DIR}/disable-maintenance.sh"
}
trap cleanup EXIT INT TERM

# ── Run database restore ──────────────────────────────────────────────────────
info "Initiating database restore..."
# Forward all arguments (like backup file path) to the main restore script
# Since restore-backup.sh is interactive if no arguments are provided, it will prompt correctly.
"${SCRIPT_DIR}/restore-backup.sh" "$@"

success "Database restore coordinate sequence completed successfully!"
echo ""
