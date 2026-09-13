#!/usr/bin/env bash
# test-alert-vocabulary.sh -- assert check-alert-vocabulary.cjs actually fires (#1882).
#
# The defect it guards: an alert raised by sendAdminAlert that nothing declares, so it cannot be
# switched off from System Settings. Three of six keys were in that state, and #1875 added a
# fourth following the same broken pattern -- which is why the rule needs enforcing rather than
# documenting.
#
# Every case plants a fixture and demands a specific verdict, including the vacuity case where
# both sides parse to nothing and a naive implementation reports agreement.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-alert-vocabulary.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/alert-vocab.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-alert-vocabulary.cjs is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# Portal fixtures. The arms render from the server-declared table, so there is nothing
# per-key to plant -- what the gate checks is that each arm asks for the table and reads it.
# <arms> selects which of those two properties to break.
#
# write_arms <dir> <arms>
#   ok         both arms wired correctly
#   v1-nofetch V1 never calls the endpoint
#   v2-inline  V2 names an alert key inline instead of rendering the table
#   v2-missing the V2 file does not exist at all
write_arms() {
    local dir="$1" arms="$2"
    mkdir -p "$dir/pkg/server/static" "$dir/ui/src/pages"
    local v1="fetch('/api/admin/settings'); data.alert_settings.map(x => x.key);"
    local v2="axios.get('/api/admin/settings'); res.data.alert_settings.forEach(a => a.key);"
    case "$arms" in
        v1-nofetch) v1="renderAlertSettings(data.alert_settings);" ;;
        v2-inline)  v2="$v2 if (k === 'alert_notify_registration') show();" ;;
    esac
    printf '%s\n' "$v1" > "$dir/pkg/server/static/dashboard.js"
    if [ "$arms" != "v2-missing" ]; then
        printf '%s\n' "$v2" > "$dir/ui/src/pages/AdminSettings.tsx"
    fi
}

# run_case <label> <expected-exit> <declared csv> <raised csv> <labelled csv> [arms]
run_case() {
    local label="$1" want="$2" declared="$3" raised="$4" labelled="$5" arms="${6:-ok}"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/pkg/server/i18n" "$dir/scripts"
    cp "$GATE" "$dir/scripts/"
    write_arms "$dir" "$arms"

    {
        echo "package server"
        echo ""
        echo "var AlertSettings = []AlertSetting{"
        local IFS=,
        for k in $declared; do
            [ -n "$k" ] || continue
            printf '\t{Key: "alert_notify_%s", LabelKey: "alert_%s", DefaultOn: true},\n' "$k" "$k"
        done
        echo "}"
    } > "$dir/pkg/server/alert_settings.go"

    {
        echo "package server"
        echo ""
        echo "func raise() {"
        local IFS=,
        for k in $raised; do
            [ -n "$k" ] || continue
            printf '\ts.sendAdminAlert("alert_notify_%s", "s", "b")\n' "$k"
        done
        echo "}"
    } > "$dir/pkg/server/callsites.go"

    {
        local IFS=,
        for k in $labelled; do
            [ -n "$k" ] || continue
            printf 'alert_%s=Label for %s\n' "$k" "$k"
        done
    } > "$dir/pkg/server/i18n/Language.properties"

    ( cd "$dir" && node scripts/check-alert-vocabulary.cjs >/dev/null 2>&1 )
    local got=$?
    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-alert-vocabulary.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Alert vocabulary gate cases:"
echo ""

# PREMISE. Without this every failing case below could be passing because the gate refuses
# everything rather than because it detects the specific defect.
run_case "declared, raised and labelled agree" 0 "registration,blacklist" "registration,blacklist" "registration,blacklist"

# FIRING. The #1882 defect exactly: an alert the code raises that no toggle exists for.
run_case "raised but not declared has no toggle" 1 "registration" "registration,watchdog_restart" "registration"

# FIRING. The other direction -- a toggle for an alert nothing sends is a control that does
# nothing, and an owner switching it off would believe they had changed something.
run_case "declared but never raised" 1 "registration,ghost" "registration" "registration,ghost"

# FIRING. A declared alert with no label renders a raw settings key to the operator.
run_case "declared without a label" 1 "registration,blacklist" "registration,blacklist" "registration"

# BOUNDING. Order is not the contract; the portals render in declaration order and the call
# sites appear wherever the feature lives.
run_case "a different order still agrees" 0 "registration,blacklist" "blacklist,registration" "blacklist,registration"

# FIRING. An arm that never asks the server for the table cannot show a toggle, whatever the
# server declares -- the state both arms were actually in before #1882.
run_case "an arm that never fetches the settings" 1 "registration,blacklist" "registration,blacklist" "registration,blacklist" "v1-nofetch"

# FIRING. An arm naming a key inline is the drift itself: it renders whatever the portal
# happens to list, so a seventh alert stays invisible until someone remembers this file.
run_case "an arm naming an alert key inline" 1 "registration,blacklist" "registration,blacklist" "registration,blacklist" "v2-inline"

# FIRING. A missing arm must fail rather than be quietly skipped -- a parity check that
# silently ignores the arm it cannot find is the #1866 defect with extra steps.
run_case "a missing portal arm is not a pass" 1 "registration,blacklist" "registration,blacklist" "registration,blacklist" "v2-missing"

# CONTROL. Both sides empty. Without an anti-vacuity floor this reports agreement and the gate
# passes forever on a tree it cannot parse.
run_case "empty on both sides must NOT be reported as agreement" 1 "" "" ""

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
