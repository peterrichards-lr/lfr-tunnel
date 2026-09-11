#!/usr/bin/env bash
# test-status-vocabulary.sh -- assert check-status-vocabulary.cjs actually fires (#1851).
#
# The gate compares pkg/db/user_status.go's UserStatuses against the portal's two enumerations.
# A comparison that always passes is the failure this repo keeps finding (#1779), so every case
# below plants a fixture pair and requires a specific verdict -- including the vacuity case, where
# both sides parse to nothing and a naive implementation would report that they agree.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-status-vocabulary.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/status-vocab.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-status-vocabulary.cjs is missing -- if it moved, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# run_case <label> <expected-exit> <go-statuses csv> <ui-statuses csv> <badge-statuses csv>
run_case() {
    local label="$1" want="$2" go_list="$3" ui_list="$4" badge_list="$5"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/pkg/db" "$dir/ui/src/pages" "$dir/scripts"
    cp "$GATE" "$dir/scripts/"

    {
        echo "package db"
        echo ""
        echo "const ("
        local IFS=,
        for s in $go_list; do
            [ -n "$s" ] || continue
            printf '\tUserStatus%s = "%s"\n' "$(printf '%s' "$s" | awk '{print toupper(substr($0,1,1)) substr($0,2)}')" "$s"
        done
        echo ")"
        echo ""
        echo "var UserStatuses = []string{"
        for s in $go_list; do
            [ -n "$s" ] || continue
            printf '\tUserStatus%s,\n' "$(printf '%s' "$s" | awk '{print toupper(substr($0,1,1)) substr($0,2)}')"
        done
        echo "}"
    } > "$dir/pkg/db/user_status.go"

    {
        echo "const STATUS_BADGE: Record<string, string> = {"
        local IFS=,
        for s in $badge_list; do
            [ -n "$s" ] || continue
            printf "  %s: 'badge-success',\n" "$s"
        done
        echo "};"
        echo ""
        echo "export default function AdminUsers() {"
        echo "  const statusOptions = useMemo("
        echo "    () => ["
        for s in $ui_list; do
            [ -n "$s" ] || continue
            printf "      { value: '%s', label: t('status_%s', '%s') },\n" "$s" "$s" "$s"
        done
        echo "    ],"
        echo "    [t],"
        echo "  );"
        echo "}"
    } > "$dir/ui/src/pages/AdminUsers.tsx"

    ( cd "$dir" && node scripts/check-status-vocabulary.cjs >/dev/null 2>&1 )
    local got=$?

    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-status-vocabulary.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Status vocabulary gate cases:"
echo ""

# PREMISE. Without this, every failing case below could be passing because the gate is broken for
# everything rather than because it detects the specific defect.
run_case "all three agree" 0 "approved,pending" "approved,pending" "approved,pending"

# FIRING. The #1847 defect exactly: the server grew a status and the portal did not learn it, so
# it could not be filtered for or changed to.
run_case "statusOptions missing a server status" 1 "approved,pending,rejected" "approved,pending" "approved,pending,rejected"

# FIRING. The badge half of the same defect -- what made 'unverified' render red in the table and
# amber in the detail panel.
run_case "STATUS_BADGE missing a server status" 1 "approved,pending,rejected" "approved,pending,rejected" "approved,pending"

# FIRING. The other direction: a portal status the server does not define, which is either a typo
# or a status that was removed server-side and left behind in the UI.
run_case "statusOptions has one the server does not define" 1 "approved,pending" "approved,pending,ghost" "approved,pending"

# BOUNDING. Order is not the contract. The portal lists in dropdown order and the server in
# lifecycle order, and requiring them to match would fail for a difference nobody cares about.
run_case "a different order still agrees" 0 "approved,pending,rejected" "rejected,approved,pending" "pending,rejected,approved"

# CONTROL. Both sides empty. A comparison with no anti-vacuity floor reports that they agree, and
# the gate then passes forever on a tree it cannot parse.
run_case "empty on both sides must NOT be reported as agreement" 1 "" "" ""

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
