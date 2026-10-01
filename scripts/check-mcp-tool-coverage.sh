#!/usr/bin/env bash
# Every tool the MCP server ADVERTISES must be mentioned by a test (#2337).
set -euo pipefail

# WHY THIS EXISTS.
#
# On 2026-10-01 the v1.51.1 announcement led with the MCP server. Within the hour, reconciling
# docs/mcp.md against the code turned up #2336: `start_tunnel` has NEVER been able to report
# success. It matches the state file against the PID of the process it spawned, but `-background`
# spawns a further process and THAT one writes the state file. The PIDs never match, so the
# success branch -- the one carrying the public URLs -- is unreachable.
#
# It shipped that way in the original commit, v1.15.0, and survived ~36 releases.
#
# Nothing was red. Every release gate answers "is master green", and a tool with no tests is green
# by default. The greener the board, the more confident the announcement. That is the blind spot,
# stated here as a check (AGENTS.md §5b rule 6).
#
# `grep -rn start_tunnel pkg/mcp/*_test.go` returned NOTHING. That is the signal this gate makes
# impossible to miss: an unreachable branch nobody ever asked about.
#
# WHAT THIS GATE DOES AND DOES NOT CLAIM.
#
# It checks that a tool NAME appears somewhere in the package's tests. That is a deliberately low
# bar -- a mention is not an assertion, and passing here does NOT mean the tool works. It means
# somebody has at least pointed a test at it. The bar is low because the failure it prevents was
# lower still: a tool nobody had ever written the name of in a test file.
#
# Do not read a green run as "MCP is tested". Read it as "no tool is completely unexamined".
#
# WHY A RATCHET AND NOT A WALL.
#
# Three of the five tools are uncovered today. Failing the build on all three would either block
# unrelated work or force a rushed three-test diff written to satisfy a gate -- which is how you
# get tests that assert nothing. So: record the backlog, stop it growing, and let it shrink.
#
# KNOWN_UNCOVERED is a RATCHET, NOT AN EXCLUSION (AGENTS.md §5b rule 5). An entry that becomes
# covered is reported as STALE and fails the build, so the list can only get shorter. Removing an
# entry is the deliberate act of saying "this one is covered now".

readonly SERVER_SRC="pkg/mcp/server.go"
readonly TEST_GLOB="pkg/mcp"

# Tools known to have no test mention, newest incident first. SHRINK THIS LIST, never grow it.
# Covering one and leaving it here fails the build -- that is the point.
KNOWN_UNCOVERED="
replay_request
start_tunnel
stop_tunnel
"

# ANTI-VACUITY FLOOR (#1779). If the extraction regex stops matching -- a gofmt change, a
# refactor that builds the tool list differently -- the loop below runs zero times and every
# check passes. A gate that cannot fire is worse than no gate, because it reports success.
#
# Five tools are advertised today. Adding one raises the real count and this floor still holds;
# only a deliberate REMOVAL needs this number changed, and that should be a conscious edit.
readonly MIN_TOOLS=5

fail=0

if [ ! -f "$SERVER_SRC" ]; then
    echo "GATE ERROR: $SERVER_SRC not found -- run from the repository root."
    exit 1
fi

# The tool names as the server advertises them in its tools/list response.
#
# `|| true` is load-bearing, and the control proved it. Without it, a rotted pattern makes grep
# exit 1, `set -e` kills the script AT THIS LINE, and the MIN_TOOLS floor below -- the check whose
# entire job is to report that rot -- never runs. The script still failed, so the control looked
# like it passed, while the diagnostic that names the cause was unreachable. That is §5c: an
# assertion satisfied by the wrong failure. Let the emptiness through and let the floor speak.
TOOLS="$(grep -oE '"name":[[:space:]]+"[a-z_]+"' "$SERVER_SRC" | grep -oE '"[a-z_]+"$' | tr -d '"' | sort -u || true)"

tool_count="$(grep -c . <<<"$TOOLS" || true)"
if [ "$tool_count" -lt "$MIN_TOOLS" ]; then
    echo "GATE ERROR: extracted only $tool_count tool name(s) from $SERVER_SRC, expected at least $MIN_TOOLS."
    echo "  The extraction has rotted, or tools were removed. Either way this gate is not checking"
    echo "  what it claims to -- fix the pattern, or lower MIN_TOOLS deliberately and say why."
    exit 1
fi

# A test file set, resolved once. Not a glob in the loop: an empty glob would silently make
# every tool look uncovered, which is a different failure wearing this one's clothes.
TEST_FILES="$(find "$TEST_GLOB" -name '*_test.go' -type f 2>/dev/null | sort)"
if [ -z "$TEST_FILES" ]; then
    echo "GATE ERROR: no *_test.go found under $TEST_GLOB/."
    exit 1
fi

is_known_uncovered() {
    grep -qx -- "$1" <<<"$(echo "$KNOWN_UNCOVERED" | grep -v '^[[:space:]]*$' || true)"
}

has_test_mention() {
    local tool="$1" f
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        # Fixed-string, whole-word: `list_requests` must not be satisfied by `list_requests_v2`.
        if grep -qFw -- "$tool" "$f"; then
            return 0
        fi
    done <<<"$TEST_FILES"
    return 1
}

echo "Checking $tool_count advertised MCP tool(s) against $(grep -c . <<<"$TEST_FILES") test file(s)..."

# 1. FIRING: an advertised tool that no test mentions and that is not already recorded.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if has_test_mention "$tool"; then
        continue
    fi
    if is_known_uncovered "$tool"; then
        continue
    fi
    echo "UNCOVERED MCP TOOL: '$tool' is advertised in tools/list but no test in $TEST_GLOB/ mentions it."
    echo "  Write a test that exercises it, or add it to KNOWN_UNCOVERED with the reason."
    echo "  Before you add it: #2336 is what an unexamined tool looks like three months later."
    fail=1
done <<<"$TOOLS"

# 2. BOUNDING: the ratchet tightens. An entry that is now covered must leave the list.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if has_test_mention "$tool"; then
        echo "STALE KNOWN_UNCOVERED: '$tool' IS mentioned by a test now -- remove it from the list."
        fail=1
    fi
done <<<"$(echo "$KNOWN_UNCOVERED" | grep -v '^[[:space:]]*$' || true)"

# 3. BOUNDING: an entry naming a tool that no longer exists is dead weight that would silently
#    excuse a future tool of the same name.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if ! grep -qx -- "$tool" <<<"$TOOLS"; then
        echo "STALE KNOWN_UNCOVERED: '$tool' is not advertised in tools/list -- remove it from the list."
        fail=1
    fi
done <<<"$(echo "$KNOWN_UNCOVERED" | grep -v '^[[:space:]]*$' || true)"

if [ "$fail" -ne 0 ]; then
    echo ""
    echo "❌ MCP tool coverage check failed."
    exit 1
fi

known_count="$(echo "$KNOWN_UNCOVERED" | grep -c '[^[:space:]]' || true)"
echo "✅ $tool_count advertised tool(s): every one is either covered or recorded ($known_count recorded, and the list can only shrink)."
