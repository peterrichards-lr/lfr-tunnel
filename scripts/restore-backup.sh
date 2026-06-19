#!/usr/bin/env bash
# restore-backup.sh — Restore an lfr-tunneld SQLite backup on the VPS
#
# Usage:
#   ./scripts/restore-backup.sh                          # Interactive: lists and prompts
#   ./scripts/restore-backup.sh /path/to/backup.db       # Non-interactive: restore specific file
#
# The script will:
#   1. Validate the chosen backup is a healthy SQLite3 database
#   2. Stop lfr-tunneld
#   3. Preserve the current DB as a pre-restore safety snapshot
#   4. Atomically swap the backup into place
#   5. Restart lfr-tunneld and verify it came up cleanly
#   6. Auto-rollback on startup failure
#
# Requirements: sqlite3 must be installed on the VPS (apt install sqlite3)

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
SERVICE_NAME="lfr-tunneld"
DB_PATH="${LFT_DB_PATH:-/var/lib/lfr-tunneld/lfr-tunnel.db}"
BACKUPS_DIR="${LFT_BACKUPS_DIR:-$(dirname "$DB_PATH")/backups}"
ROLLBACK_COPY="${DB_PATH}.pre-restore"

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()     { error "$*"; exit 1; }

# ── Preflight checks ─────────────────────────────────────────────────────────
[[ $EUID -eq 0 ]] || die "This script must be run as root (or via sudo)."
command -v sqlite3 &>/dev/null || die "sqlite3 is not installed. Run: apt install sqlite3"
[[ -f "$DB_PATH" ]] || die "Active database not found at: $DB_PATH"
[[ -d "$BACKUPS_DIR" ]] || die "Backups directory not found at: $BACKUPS_DIR"

# ── Select backup file ────────────────────────────────────────────────────────
BACKUP_FILE="${1:-}"

if [[ -z "$BACKUP_FILE" ]]; then
    echo ""
    echo -e "${CYAN}Available backups (newest first):${NC}"
    echo "────────────────────────────────────────────────────────────────"
    mapfile -t backups < <(ls -t "$BACKUPS_DIR"/*.db 2>/dev/null || true)

    if [[ ${#backups[@]} -eq 0 ]]; then
        die "No backup files found in $BACKUPS_DIR"
    fi

    for i in "${!backups[@]}"; do
        size=$(du -sh "${backups[$i]}" 2>/dev/null | cut -f1)
        mtime=$(date -r "${backups[$i]}" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || stat -c "%y" "${backups[$i]}" 2>/dev/null | cut -d'.' -f1)
        printf "  [%2d] %-55s %6s  %s\n" "$((i+1))" "$(basename "${backups[$i]}")" "$size" "$mtime"
    done

    echo "────────────────────────────────────────────────────────────────"
    echo ""
    read -rp "Enter the number of the backup to restore (or q to quit): " selection

    [[ "$selection" == "q" || "$selection" == "Q" ]] && { info "Cancelled."; exit 0; }

    if ! [[ "$selection" =~ ^[0-9]+$ ]] || (( selection < 1 || selection > ${#backups[@]} )); then
        die "Invalid selection: $selection"
    fi

    BACKUP_FILE="${backups[$((selection-1))]}"
fi

[[ -f "$BACKUP_FILE" ]] || die "Backup file not found: $BACKUP_FILE"

# ── Confirm ───────────────────────────────────────────────────────────────────
echo ""
warn "You are about to restore from:"
echo "  Backup : $BACKUP_FILE"
echo "  Into   : $DB_PATH"
echo ""
warn "This will overwrite the active database. The current DB will be preserved"
warn "at ${ROLLBACK_COPY} as a safety snapshot."
echo ""
read -rp "Type 'yes' to confirm: " confirm
[[ "$confirm" == "yes" ]] || { info "Cancelled."; exit 0; }

# ── Validate backup integrity ─────────────────────────────────────────────────
info "Validating backup integrity..."
result=$(sqlite3 "$BACKUP_FILE" "PRAGMA integrity_check;" 2>&1)
if [[ "$result" != "ok" ]]; then
    die "Backup integrity check FAILED:\n$result"
fi
success "Backup integrity check passed."

# ── Stop service ──────────────────────────────────────────────────────────────
info "Stopping $SERVICE_NAME..."
systemctl stop "$SERVICE_NAME"
success "$SERVICE_NAME stopped."

# ── Safety snapshot of current DB ────────────────────────────────────────────
info "Creating pre-restore safety snapshot..."
cp "$DB_PATH" "$ROLLBACK_COPY"
success "Current DB preserved at: $ROLLBACK_COPY"

# ── Atomic swap ───────────────────────────────────────────────────────────────
info "Restoring backup..."
cp "$BACKUP_FILE" "${DB_PATH}.incoming"
mv "${DB_PATH}.incoming" "$DB_PATH"
success "Backup restored to: $DB_PATH"

# ── Restart service ───────────────────────────────────────────────────────────
info "Starting $SERVICE_NAME..."
systemctl start "$SERVICE_NAME"

# Give the service a moment to initialise
sleep 3

if systemctl is-active --quiet "$SERVICE_NAME"; then
    success "$SERVICE_NAME is running. Restore complete!"
    echo ""
    echo -e "${GREEN}────────────────────────────────────────────────────────────────${NC}"
    echo -e "${GREEN}  Restore completed successfully.${NC}"
    echo -e "${GREEN}  Pre-restore snapshot: $ROLLBACK_COPY${NC}"
    echo -e "${GREEN}────────────────────────────────────────────────────────────────${NC}"
    echo ""
else
    error "$SERVICE_NAME failed to start after restore. Rolling back automatically..."
    cp "$ROLLBACK_COPY" "$DB_PATH"
    systemctl start "$SERVICE_NAME"

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        warn "Rollback successful. $SERVICE_NAME is running on the original database."
    else
        die "CRITICAL: $SERVICE_NAME failed to restart even after rollback. Manual intervention required.\nCheck: journalctl -u $SERVICE_NAME -n 50"
    fi
    exit 1
fi
