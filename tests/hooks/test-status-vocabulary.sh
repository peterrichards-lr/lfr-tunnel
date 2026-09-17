#!/usr/bin/env bash
# test-status-vocabulary.sh -- assert check-status-vocabulary.cjs actually fires (#1851).
#
# The gate compares pkg/db/user_status.go's UserStatuses against the portal's three enumerations
# -- V2's statusOptions and STATUS_BADGE, and V1's STATUS_BADGE (#1866).
# A comparison that always passes is the failure this repo keeps finding (#1779), so every case
# below plants a fixture pair and requires a specific verdict -- including the vacuity cases, where
# a side parses to nothing and a naive implementation would report that the sets agree.
#
# A verdict here is an exit code AND the line the gate printed. The exit code alone is not a
# verdict: every failure mode this gate has exits 1, so a case asserting only that is satisfied by
# whichever failure fires first. That is not hypothetical -- it is how the vacuity case below spent
# its life green while never once putting an empty list on the V1 side (#1967).
#
# Cases are labelled PREMISE / FIRING / BOUNDING / CONTROL. A BOUNDING case pins a deliberate limit
# so that crossing it turns this suite red rather than silently widening the gate.
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

# run_case <label> <expected-exit> <expected-output regex> <go csv> <statusOptions csv>
#          <V2 badge csv> [V1 badge csv]
#
# The regex is the load-bearing half, not the exit code (#1967). Exit 1 is shared by every failure
# mode this gate has -- an unparseable source, a mismatched list and the anti-vacuity floor all
# produce it -- so an exit-code-only assertion is satisfied by whichever failure happens to fire
# first rather than by the one the case names. Each case therefore names a line only its own
# defect produces.
#
# The V1 list defaults to the V2 one, so every case below exercises V1 too and the cases that name
# it are the ones where the two arms deliberately disagree.
run_case() {
    local label="$1" want="$2" want_re="$3" go_list="$4" ui_list="$5" badge_list="$6"
    local v1_list="${7-$badge_list}"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/pkg/db" "$dir/ui/src/pages" "$dir/pkg/server/static" "$dir/scripts"
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

    if [ "$v1_list" = "__inline__" ]; then
        # V1 having gone back to deciding the colour in a ternary -- the defect itself, not a
        # mismatched list. The gate must refuse to parse rather than skip the arm it cannot find.
        printf '%s\n' \
            "function renderUser(u) {" \
            "  return \`<span class=\"badge \${u.status === 'approved' ? 'success' : 'warning'}\">\`;" \
            "}" > "$dir/pkg/server/static/dashboard.js"
    else
        {
            echo "const STATUS_BADGE = {"
            local IFS=,
            for s in $v1_list; do
                [ -n "$s" ] || continue
                printf "  %s: 'success',\n" "$s"
            done
            echo "};"
        } > "$dir/pkg/server/static/dashboard.js"
    fi

    ( cd "$dir" && node scripts/check-status-vocabulary.cjs >"$dir/output.txt" 2>&1 )
    local got=$?

    if [ "$got" -ne "$want" ]; then
        fail "$label (expected exit $want, got $got)"
        sed 's/^/        /' "$dir/output.txt"
        return
    fi

    if grep -Eq -e "$want_re" "$dir/output.txt"; then
        pass "$label (exit $got)"
    else
        fail "$label (exit $got as expected, but for the wrong reason -- nothing matched: $want_re)"
        sed 's/^/        /' "$dir/output.txt"
    fi
}

echo "Status vocabulary gate cases:"
echo ""

# PREMISE. Without this, every failing case below could be passing because the gate is broken for
# everything rather than because it detects the specific defect.
run_case "all three agree" 0 \
    "^OK -- statusOptions and both portals' STATUS_BADGE match: approved, pending$" \
    "approved,pending" "approved,pending" "approved,pending"

# FIRING. The #1847 defect exactly: the server grew a status and the portal did not learn it, so
# it could not be filtered for or changed to.
run_case "statusOptions missing a server status" 1 \
    "^  statusOptions is missing: rejected$" \
    "approved,pending,rejected" "approved,pending" "approved,pending,rejected"

# FIRING. The badge half of the same defect -- what made 'unverified' render red in the table and
# amber in the detail panel.
run_case "STATUS_BADGE missing a server status" 1 \
    "^  STATUS_BADGE \(V2\) is missing: rejected$" \
    "approved,pending,rejected" "approved,pending,rejected" "approved,pending"

# FIRING. The other direction: a portal status the server does not define, which is either a typo
# or a status that was removed server-side and left behind in the UI.
run_case "statusOptions has one the server does not define" 1 \
    "^  statusOptions has statuses the server does not define: ghost$" \
    "approved,pending" "approved,pending,ghost" "approved,pending"

# BOUNDING. Order is not the contract. The portal lists in dropdown order and the server in
# lifecycle order, and requiring them to match would fail for a difference nobody cares about.
run_case "a different order still agrees" 0 \
    "^OK -- .*: approved, pending, rejected$" \
    "approved,pending,rejected" "rejected,approved,pending" "pending,rejected,approved"

# FIRING. The #1866 defect: V2 learned the status and V1 did not, so the same user renders a
# different colour depending on which arm of the A/B test they land in.
run_case "V1 STATUS_BADGE missing a server status" 1 \
    "^  STATUS_BADGE \(V1\) is missing: rejected$" \
    "approved,pending,rejected" "approved,pending,rejected" "approved,pending,rejected" "approved,pending"

# FIRING. V1 in the other direction, so the arm is genuinely compared and not merely required to
# be non-empty.
run_case "V1 STATUS_BADGE has one the server does not define" 1 \
    "^  STATUS_BADGE \(V1\) has statuses the server does not define: ghost$" \
    "approved,pending" "approved,pending" "approved,pending" "approved,pending,ghost"

# FIRING. V1 reverting to an inline ternary. The map is what the gate can read; if it disappears,
# the gate must fail rather than quietly stop checking that arm.
run_case "V1 back to an inline ternary is not a silent skip" 1 \
    "could not find STATUS_BADGE in pkg/server/static/dashboard\.js" \
    "approved,pending" "approved,pending" "approved,pending" "__inline__"

# CONTROL. All four lists empty. A comparison with no anti-vacuity floor reports that they agree,
# and the gate then passes forever on a tree it cannot parse.
#
# This case used to pass a stray `""ile` as the V1 list, so the V1 arm held one entry named `ile`
# and "empty on both sides" was never exercised (#1967). It stayed green with the ENTIRE floor
# deleted, because `ile` is a status the server does not define and the comparison failed on that
# instead -- measured, not assumed. Hence the counts below rather than the exit code: only the
# floor prints them, and only with all four at zero.
run_case "empty on all four sides must NOT be reported as agreement" 1 \
    "^  server=0 statusOptions=0 STATUS_BADGE=0 V1 STATUS_BADGE=0$" \
    "" "" "" ""

# CONTROL, per arm. The floor is four `.length === 0` clauses, and the all-empty case above fires
# on the first of them -- so it cannot tell whether the other three still exist. Each case below
# empties exactly one side, and each dies if its own clause is removed: without it the gate reaches
# the comparison and fails there instead, with a different message, which is what these regexes
# distinguish.
run_case "only the server list empty must NOT be reported as agreement" 1 \
    "^  server=0 statusOptions=2 STATUS_BADGE=2 V1 STATUS_BADGE=2$" \
    "" "approved,pending" "approved,pending" "approved,pending"

run_case "only statusOptions empty must NOT be reported as agreement" 1 \
    "^  server=2 statusOptions=0 STATUS_BADGE=2 V1 STATUS_BADGE=2$" \
    "approved,pending" "" "approved,pending" "approved,pending"

run_case "only the V2 STATUS_BADGE empty must NOT be reported as agreement" 1 \
    "^  server=2 statusOptions=2 STATUS_BADGE=0 V1 STATUS_BADGE=2$" \
    "approved,pending" "approved,pending" "" "approved,pending"

run_case "only the V1 STATUS_BADGE empty must NOT be reported as agreement" 1 \
    "^  server=2 statusOptions=2 STATUS_BADGE=2 V1 STATUS_BADGE=0$" \
    "approved,pending" "approved,pending" "approved,pending" ""

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
