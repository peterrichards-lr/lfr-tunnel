#!/usr/bin/env bash
#
# test-mcp-tool-coverage.sh -- scripts/check-mcp-tool-coverage.sh can actually fire (#2337).
#
# The gate exists because #2336 sat unnoticed for ~36 releases: `start_tunnel` could never report
# success, and no test in pkg/mcp ever mentioned that name. A gate that would not have caught that
# is worth nothing, so each detection is exercised against a fixture.
#
# TWO CASES ARE REGRESSION GUARDS FOR THE GATE'S OWN FIRST DRAFT, which review broke:
#
#   RELOCATION  the first version read only server.go. Moving the tool list to tools.go -- a
#               plain refactor -- made it report every real tool as "no longer advertised", and
#               following its own repair advice emptied the ratchet and turned it GREEN over an
#               untested tool. Now the whole package is scanned.
#   NAME SHAPE  the first version matched [a-z_]+ only, so start_tunnel_v2, replay_request2 and
#               startTunnel were invisible -- and a MIN_TOOLS lower bound cannot see a tool it
#               failed to read. A versioned successor to the broken start_tunnel is the single
#               most likely next tool name here. Now [A-Za-z0-9_]+, checked as an exact SET.
#
# The gate's lists are injected per case. Without that, every "X is STALE" assertion would depend
# on the real KNOWN_UNCOVERED still naming X -- so the commit that legitimately pays the debt down
# would turn this suite red while reporting a regression that did not happen. That is the "right
# code, wrong reason" class (AGENTS.md §5c), and it is why these cases assert on OUTPUT rather
# than on exit status: during development an anti-vacuity control "passed" on exit status alone
# while its diagnostic never ran.
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
show() { printf '%s\n' "$1" | sed 's/^/        /'; }

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

# plant <src-basename> <tools...> -- writes a source file advertising exactly those tool names,
# plus a non-tool "name" key of the shape pkg/mcp/server.go really carries.
plant() {
    local base="$1"; shift
    rm -f "$FIXTURE"/pkg/mcp/*.go
    {
        echo 'package mcp'
        echo '"name":    "lfr-tunnel",'
        for t in "$@"; do
            printf '\t{"name":        "%s", "description": "x"},\n' "$t"
        done
    } > "$FIXTURE/pkg/mcp/$base"
}

# mentions <tool...> -- the test file names exactly these tools.
mentions() {
    {
        echo 'package mcp'
        for t in "$@"; do echo "// mentions: $t"; done
    } > "$FIXTURE/pkg/mcp/server_test.go"
}

# run_gate <expected-set> <known-uncovered> -- newline-separated, may be empty.
run_gate() {
    ( cd "$FIXTURE" \
      && LFT_MCP_EXPECTED_TOOLS="$1" LFT_MCP_KNOWN_UNCOVERED="$2" LFT_MCP_KNOWN_UNCOVERED_MAX=9 \
         "$GATE" 2>&1 )
}

FIVE='get_tunnel_status
list_requests
replay_request
start_tunnel
stop_tunnel'

echo ""
echo "-- CONTROL: the untouched fixture is green, so every failure below is its mutation"
plant server.go get_tunnel_status list_requests replay_request start_tunnel stop_tunnel
mentions get_tunnel_status list_requests
OUT="$(run_gate "$FIVE" 'replay_request
start_tunnel
stop_tunnel')"
if grep -q '✅' <<<"$OUT"; then
    pass "CONTROL: an unmutated fixture passes"
else
    fail "CONTROL: the baseline fixture fails, so every result below is suspect"; show "$OUT"
fi

echo ""
echo "-- FIRING: an advertised tool whose name nothing mentions"
OUT="$(run_gate "$FIVE" '')"
if grep -q "UNCOVERED MCP TOOL: 'start_tunnel'" <<<"$OUT"; then
    pass "FIRING: an unexamined tool is named -- the #2336 shape"
else
    fail "FIRING: a tool with no mention went unreported"; show "$OUT"
fi

echo ""
echo "-- FIRING: a tool name with a digit or a capital is SEEN (the [a-z_] blind spot)"
plant server.go get_tunnel_status list_requests replay_request start_tunnel start_tunnel_v2
mentions get_tunnel_status list_requests
OUT="$(run_gate 'get_tunnel_status
list_requests
replay_request
start_tunnel
start_tunnel_v2' '')"
if grep -q "UNCOVERED MCP TOOL: 'start_tunnel_v2'" <<<"$OUT"; then
    pass "FIRING: start_tunnel_v2 is visible -- the old regex could not see it at all"
else
    fail "FIRING: a _v2 tool was invisible, which is how a successor to #2336 would ship"; show "$OUT"
fi

echo ""
echo "-- BOUNDING: whole-word matching, so start_tunnel does not cover start_tunnel_v2"
mentions get_tunnel_status list_requests start_tunnel
OUT="$(run_gate 'get_tunnel_status
list_requests
replay_request
start_tunnel
start_tunnel_v2' '')"
if grep -q "UNCOVERED MCP TOOL: 'start_tunnel_v2'" <<<"$OUT"; then
    pass "BOUNDING: a prefix does not satisfy a longer name"
else
    fail "BOUNDING: mentioning start_tunnel wrongly covered start_tunnel_v2"; show "$OUT"
fi

echo ""
echo "-- RELOCATION: the tool list moved to another file is still read"
plant tools.go get_tunnel_status list_requests replay_request start_tunnel stop_tunnel
mentions get_tunnel_status list_requests
OUT="$(run_gate "$FIVE" 'replay_request
start_tunnel
stop_tunnel')"
if grep -q '✅' <<<"$OUT"; then
    pass "RELOCATION: a refactor to tools.go does not blind the gate"
else
    fail "RELOCATION: moving the tool list broke the gate -- the exact fault review found"; show "$OUT"
fi

echo ""
echo "-- SET: a tool the code advertises but the expected set does not know about"
plant server.go get_tunnel_status list_requests replay_request start_tunnel stop_tunnel brand_new_tool
mentions get_tunnel_status list_requests
OUT="$(run_gate "$FIVE" '')"
if grep -q 'does not match EXPECTED_TOOLS' <<<"$OUT" && grep -q 'brand_new_tool' <<<"$OUT"; then
    pass "SET: an unexpected tool is refused and named, not silently counted"
else
    fail "SET: a new tool slipped past the set check"; show "$OUT"
fi

echo ""
echo "-- SET: an expected tool the code no longer advertises (removal, or extraction rot)"
plant server.go get_tunnel_status list_requests replay_request start_tunnel
mentions get_tunnel_status list_requests
OUT="$(run_gate "$FIVE" '')"
if grep -q 'does not match EXPECTED_TOOLS' <<<"$OUT" && grep -q 'stop_tunnel' <<<"$OUT"; then
    pass "SET: a vanished tool is refused -- a lower bound could not have seen this"
else
    fail "SET: a missing tool did not fail the set check"; show "$OUT"
fi

echo ""
echo "-- BOUNDING: the ratchet tightens when a recorded tool gains a mention"
plant server.go get_tunnel_status list_requests replay_request start_tunnel stop_tunnel
mentions get_tunnel_status list_requests start_tunnel
OUT="$(run_gate "$FIVE" 'start_tunnel')"
if grep -q "STALE KNOWN_UNCOVERED: 'start_tunnel'" <<<"$OUT"; then
    pass "BOUNDING: a covered entry must leave the list, so it can only shrink"
else
    fail "BOUNDING: a now-covered entry was not reported stale"; show "$OUT"
fi

echo ""
echo "-- BOUNDING: an entry naming a tool that is not advertised"
mentions get_tunnel_status list_requests
OUT="$(run_gate "$FIVE" 'ghost_tool')"
if grep -q "STALE KNOWN_UNCOVERED: 'ghost_tool' is not advertised" <<<"$OUT"; then
    pass "BOUNDING: a dead entry cannot sit there excusing a future namesake"
else
    fail "BOUNDING: an entry for a non-existent tool was not reported"; show "$OUT"
fi

echo ""
echo "-- CEILING: the list cannot grow silently"
OUT="$( cd "$FIXTURE" \
        && LFT_MCP_EXPECTED_TOOLS="$FIVE" \
           LFT_MCP_KNOWN_UNCOVERED='replay_request
start_tunnel
stop_tunnel' \
           LFT_MCP_KNOWN_UNCOVERED_MAX=2 "$GATE" 2>&1 )"
if grep -q 'KNOWN_UNCOVERED has grown to 3, above the ceiling of 2' <<<"$OUT"; then
    pass "CEILING: adding an entry means raising a number in the same diff"
else
    fail "CEILING: the list grew past its ceiling without a word"; show "$OUT"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
