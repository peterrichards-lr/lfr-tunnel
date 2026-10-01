#!/usr/bin/env bash
#
# test-mcp-tool-coverage.sh -- scripts/check-mcp-tool-coverage.sh can actually fire (#2337).
#
# The gate exists because #2336 sat unnoticed for ~36 releases: `start_tunnel` could never report
# success, and no test in pkg/mcp ever mentioned it. A gate that would not have caught that is
# worth nothing, so each detection is exercised against a fixture:
#
#   FIRING        a newly advertised tool that no test mentions is reported
#   BOUNDING      a KNOWN_UNCOVERED entry that gains a test is reported STALE -- the ratchet
#                 tightens, so the list can only shrink
#   BOUNDING      a KNOWN_UNCOVERED entry naming a tool that is no longer advertised is reported
#   ANTI-VACUITY  an extraction that matches nothing reports WHY, rather than dying silently
#
# That last case is the reason this file asserts on OUTPUT and not on exit codes. During
# development the anti-vacuity control "passed" on exit status alone while the diagnostic never
# ran -- the script was dying at the `grep` under `set -e`, one line before the check whose job
# was to explain it. Right code, wrong reason (AGENTS.md §5c). Asserting the message is what
# distinguishes the two.
#
# Fixtures live in a temp tree: the gate resolves pkg/mcp/ from its working directory, so anything
# planted in the real tree would be read by the real run.
#
# bash 3.2 compatible (macOS /bin/bash). See AGENTS.md.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

GATE="$REPO_ROOT/scripts/check-mcp-tool-coverage.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -x "$GATE" ]; then
    fail "PREMISE: $GATE is missing or not executable -- if it moved, move this test with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

echo "-- PREMISE: the gate passes against the real tree"
if "$GATE" >/dev/null 2>&1; then
    pass "PREMISE: the real tree is clean, so a failure below is about the fixture"
else
    fail "PREMISE: the real tree already fails -- fix that first"
fi

FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/lft-mcp-coverage.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT

mkdir -p "$FIXTURE/pkg/mcp"

# Plants a fixture tree. $1 = extra tool name advertised (may be empty).
# $2 = extra tool name mentioned by the test file (may be empty).
plant() {
    local extra_tool="$1" extra_mention="$2" f="$FIXTURE/pkg/mcp/server.go"
    {
        echo 'package mcp'
        echo 'var tools = []map[string]interface{}{'
        for t in get_tunnel_status start_tunnel stop_tunnel list_requests replay_request; do
            printf '\t{"name":        "%s", "description": "x"},\n' "$t"
        done
        if [ -n "$extra_tool" ]; then
            printf '\t{"name":        "%s", "description": "x"},\n' "$extra_tool"
        fi
        echo '}'
    } > "$f"

    # The real test file mentions exactly the two tools that are covered today.
    {
        echo 'package mcp'
        echo '// mentions: get_tunnel_status list_requests'
        if [ -n "$extra_mention" ]; then
            echo "// mentions: $extra_mention"
        fi
    } > "$FIXTURE/pkg/mcp/server_test.go"
}

# Runs the gate inside the fixture and captures combined output.
run_gate() {
    ( cd "$FIXTURE" && "$GATE" 2>&1 )
}

echo ""
echo "-- FIRING: a newly advertised tool that nothing tests"
plant "totally_new_tool" ""
OUT="$(run_gate)"
if grep -q "UNCOVERED MCP TOOL: 'totally_new_tool'" <<<"$OUT"; then
    pass "FIRING: an unexamined new tool is named"
else
    fail "FIRING: a new tool with no test went unreported -- the gate would not have caught #2336"
    printf '%s\n' "$OUT" | sed 's/^/        /'
fi

echo ""
echo "-- BOUNDING: the ratchet tightens when a recorded tool gains a test"
plant "" "start_tunnel"
OUT="$(run_gate)"
if grep -q "STALE KNOWN_UNCOVERED: 'start_tunnel'" <<<"$OUT"; then
    pass "BOUNDING: a covered entry must leave the list, so it can only shrink"
else
    fail "BOUNDING: a now-covered entry was not reported stale -- the list could grow forever"
    printf '%s\n' "$OUT" | sed 's/^/        /'
fi

echo ""
echo "-- BOUNDING: an entry naming a tool that is no longer advertised"
# Drop replay_request from the advertised set while it is still in KNOWN_UNCOVERED.
#
# A substitute tool is planted first so the count stays at five. Deleting one outright drops below
# MIN_TOOLS, and the anti-vacuity floor fires before the check this case is about -- masking it
# with a different, correct-looking failure. The substitute is mentioned by the test file so it
# does not trip the FIRING rule either; the only thing left for the gate to report is the stale
# entry.
plant "substitute_tool" "substitute_tool"
sed -i.bak '/replay_request/d' "$FIXTURE/pkg/mcp/server.go" && rm -f "$FIXTURE/pkg/mcp/server.go.bak"
OUT="$(run_gate)"
if grep -q "STALE KNOWN_UNCOVERED: 'replay_request' is not advertised" <<<"$OUT"; then
    pass "BOUNDING: a dead entry is reported rather than silently excusing a future namesake"
else
    fail "BOUNDING: an entry for a removed tool was not reported"
    printf '%s\n' "$OUT" | sed 's/^/        /'
fi

echo ""
echo "-- ANTI-VACUITY: an extraction that reads nothing says so"
plant "" ""
echo 'package mcp' > "$FIXTURE/pkg/mcp/server.go"   # no tool names at all
OUT="$(run_gate)"
if grep -q 'GATE ERROR: extracted only 0 tool name' <<<"$OUT"; then
    pass "ANTI-VACUITY: the floor reports the cause, not just a non-zero exit"
else
    fail "ANTI-VACUITY: a gate reading zero tools did not explain itself -- a silent pass is next"
    printf '%s\n' "$OUT" | sed 's/^/        /'
fi

echo ""
echo "-- CONTROL: the untouched fixture is green, so the failures above are the mutations"
plant "" ""
OUT="$(run_gate)"
if grep -q '✅' <<<"$OUT"; then
    pass "CONTROL: an unmutated fixture passes"
else
    fail "CONTROL: the baseline fixture fails, so every result above is suspect"
    printf '%s\n' "$OUT" | sed 's/^/        /'
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
