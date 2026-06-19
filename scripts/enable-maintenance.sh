#!/usr/bin/env bash
# enable-maintenance.sh — Enable Nginx maintenance mode for lfr-tunnel
#
# Usage:
#   sudo ./scripts/enable-maintenance.sh
#

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
TRIGGER_FILE="${LFT_MAINTENANCE_TRIGGER:-/var/lib/lfr-tunneld/maintenance.enable}"
WEB_ROOT="${LFT_WEB_ROOT:-/var/www/lfr-tunnel}"

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
SRC_HTML="${SCRIPT_DIR}/../pkg/server/static/maintenance.html"

# ── Deploy Maintenance Page ──────────────────────────────────────────────────
if [[ -f "$SRC_HTML" ]]; then
    mkdir -p "$WEB_ROOT"
    cp "$SRC_HTML" "${WEB_ROOT}/maintenance.html"
    chmod 644 "${WEB_ROOT}/maintenance.html"
    info "Deployed maintenance page to ${WEB_ROOT}/maintenance.html"
else
    if [[ -f "${WEB_ROOT}/maintenance.html" ]]; then
        warn "Source HTML not found at $SRC_HTML; using existing page in $WEB_ROOT."
    else
        die "Maintenance page source not found at $SRC_HTML and no file exists in $WEB_ROOT."
    fi
fi

# ── Create Trigger File ──────────────────────────────────────────────────────
TRIGGER_DIR="$(dirname "$TRIGGER_FILE")"
mkdir -p "$TRIGGER_DIR"
touch "$TRIGGER_FILE"
chmod 644 "$TRIGGER_FILE"

success "Maintenance mode ENABLED. Nginx will now serve the maintenance page."
echo ""
