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
# WHAT THIS GATE DOES AND DOES NOT CLAIM.
#
# It checks that a tool's ADVERTISED NAME -- the snake_case string an agent sends over the wire --
# appears somewhere in the package's tests. That is a deliberately low bar, and it is important to
# be exact about how low, because the obvious reading is wrong:
#
#   pkg/mcp/mcp_extra_test.go ALREADY calls startTunnel(), stopTunnel() and replayRequest().
#
# Those three are in KNOWN_UNCOVERED anyway, because that test writes the Go IDENTIFIER, asserts
# nothing, and discards both return values. So this gate does not measure "has a test", and a
# green run does NOT mean MCP works. It measures the one thing that was actually absent when
# #2336 shipped: whether anybody has written the name a CALLER uses, in a test.
#
# A comment satisfies it. That is intentional and it is the floor, not the goal.
#
# WHY A RATCHET AND NOT A WALL.
#
# Three of the five tools are uncovered today. Failing the build on all three would either block
# unrelated work or force a rushed three-test diff written to satisfy a gate -- which is how you
# get tests that assert nothing (and mcp_extra_test.go is what that looks like).
#
# KNOWN_UNCOVERED is a RATCHET, NOT AN EXCLUSION (AGENTS.md §5b rule 5): an entry that becomes
# covered is reported STALE and fails. KNOWN_UNCOVERED_MAX stops it growing silently -- adding an
# entry means raising a number in the same diff, the idiom check-nolint-ratchet.sh uses, so a
# reviewer sees a loosening as a loosening.

readonly SRC_DIR="pkg/mcp"

# Tools known to have no mention of their advertised name, newest incident first.
# SHRINK THIS LIST. Covering one and leaving it here fails the build -- that is the point.
#
# Injectable ONLY so the hook test can plant its own list. Production runs never set it. Without
# this, every hook-test case asserting "X is STALE" would depend on the real list still naming X
# -- so the commit that legitimately pays the debt down would turn the hook suite red with a
# diagnosis pointing at a regression that did not happen. Review of this PR caught that; it is
# the same "right code, wrong reason" class the gate is meant to prevent (§5c).
KNOWN_UNCOVERED="${LFT_MCP_KNOWN_UNCOVERED-replay_request
stop_tunnel}"

readonly KNOWN_UNCOVERED_MAX="${LFT_MCP_KNOWN_UNCOVERED_MAX:-2}"   # 3 -> 2: start_tunnel covered by #2336

# The exact set of tools the server is expected to advertise, sorted.
#
# AN EXACT SET, NOT A COUNT FLOOR. The first version of this gate asserted "at least 5 names
# extracted", which review broke in two ways that both ended in a green run over the real defect:
#
#   OVER-MATCH    a non-tool "name" key satisfies the floor. pkg/mcp/server.go already has one --
#                 "name": "lfr-tunnel" in serverInfo -- which escapes only because of its hyphen.
#                 Move the tool list to another file and leave five unrelated "name" keys behind,
#                 and the floor is satisfied by names that are not tools at all.
#   UNDER-MATCH   a lower bound cannot see a tool it failed to read. Add a sixth tool, fail to
#                 extract it, and 5 is still >= 5.
#
# Set equality catches additions, removals, renames, over-matching and relocation in one place,
# and the failure names which side moved. Changing this list is a deliberate edit.
# `${VAR-default}`, NOT `${VAR:-default}`: the colon form substitutes the default for an EMPTY
# value as well as an unset one, so a fixture injecting a deliberately empty list would have
# silently run against the PRODUCTION list and reported a pass it never earned. The hook test
# caught exactly that.
#
# Injectable for the same reason as KNOWN_UNCOVERED: set equality now fires BEFORE the coverage
# check, so a fixture that plants a sixth tool would stop at "set mismatch" and never reach the
# UNCOVERED rule it exists to exercise. Production runs never set it.
EXPECTED_TOOLS="${LFT_MCP_EXPECTED_TOOLS-get_tunnel_status
list_requests
replay_request
start_tunnel
stop_tunnel}"

fail=0

if [ ! -d "$SRC_DIR" ]; then
    echo "GATE ERROR: $SRC_DIR not found -- run from the repository root."
    exit 1
fi

# Scan the WHOLE package, not just server.go. A plain refactor moving the tool list to
# pkg/mcp/tools.go must not blind this gate -- under the old single-file read it did, and worse,
# it reported every real tool as "no longer advertised", so following the gate's own repair advice
# emptied KNOWN_UNCOVERED and turned the run green over an untested tool.
SRC_FILES="$(find "$SRC_DIR" -name '*.go' -type f ! -name '*_test.go' 2>/dev/null | sort)"
if [ -z "$SRC_FILES" ]; then
    echo "GATE ERROR: no non-test *.go found under $SRC_DIR/."
    exit 1
fi

# [A-Za-z0-9_] not [a-z_], and [[:space:]]* not +. The old pattern could not see start_tunnel_v2,
# replay_request2, startTunnel or a gofmt-tightened "name":"x" -- and a versioned successor to the
# broken start_tunnel is the single most likely next tool name in this package.
ADVERTISED="$(grep -hoE '"name":[[:space:]]*"[A-Za-z0-9_]+"' $SRC_FILES 2>/dev/null \
    | grep -oE '"[A-Za-z0-9_]+"$' | tr -d '"' | sort -u || true)"

if [ "$ADVERTISED" != "$EXPECTED_TOOLS" ]; then
    echo "GATE ERROR: the advertised tool set does not match EXPECTED_TOOLS."
    echo ""
    echo "  Only in the code (advertised but not expected -- a new tool, a rename, or a non-tool"
    echo "  \"name\" key being swept in):"
    comm -23 <(echo "$ADVERTISED") <(echo "$EXPECTED_TOOLS") | sed 's/^/    /' || true
    echo "  Only in EXPECTED_TOOLS (expected but not found -- a removal, or the extraction has"
    echo "  rotted and this gate is no longer reading what it claims to):"
    comm -13 <(echo "$ADVERTISED") <(echo "$EXPECTED_TOOLS") | sed 's/^/    /' || true
    echo ""
    echo "  Update EXPECTED_TOOLS in $0 deliberately, after checking which of those it is."
    exit 1
fi

TEST_FILES="$(find "$SRC_DIR" -maxdepth 1 -name '*_test.go' -type f 2>/dev/null | sort)"
if [ -z "$TEST_FILES" ]; then
    echo "GATE ERROR: no *_test.go found directly under $SRC_DIR/."
    exit 1
fi

known_list() { grep -v '^[[:space:]]*$' <<<"$KNOWN_UNCOVERED" || true; }

is_known_uncovered() {
    grep -qxF -- "$1" <<<"$(known_list)"
}

has_test_mention() {
    local tool="$1" f
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        # Fixed-string, whole-word. BSD and GNU grep both treat '_' as word-constituent, so
        # `start_tunnel` does NOT match inside `start_tunnel_v2` -- verified, and load-bearing
        # now that a _v2 successor is the likely next tool.
        if grep -qFw -- "$tool" "$f"; then
            return 0
        fi
    done <<<"$TEST_FILES"
    return 1
}

tool_count="$(grep -c . <<<"$ADVERTISED" || true)"
test_count="$(grep -c . <<<"$TEST_FILES" || true)"
echo "Checking $tool_count advertised MCP tool(s) against $test_count test file(s)..."

# 1. FIRING: an advertised tool whose name no test mentions, and that is not already recorded.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if has_test_mention "$tool" || is_known_uncovered "$tool"; then
        continue
    fi
    echo "UNCOVERED MCP TOOL: '$tool' is advertised but no test in $SRC_DIR/ mentions that name."
    echo "  Write a test that exercises it, or add it to KNOWN_UNCOVERED and raise"
    echo "  KNOWN_UNCOVERED_MAX in the same diff, so the loosening is visible as one."
    echo "  Before you do: #2336 is what an unexamined tool looks like three months later."
    fail=1
done <<<"$ADVERTISED"

# 2. BOUNDING: the ratchet tightens. An entry that is now covered must leave the list.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if has_test_mention "$tool"; then
        echo "STALE KNOWN_UNCOVERED: '$tool' IS mentioned by a test now -- remove it from the list."
        fail=1
    fi
done <<<"$(known_list)"

# 3. BOUNDING: an entry naming a tool that is not advertised is dead weight, and would silently
#    excuse a future tool that happened to take the same name.
while IFS= read -r tool; do
    [ -n "$tool" ] || continue
    if ! grep -qxF -- "$tool" <<<"$ADVERTISED"; then
        echo "STALE KNOWN_UNCOVERED: '$tool' is not advertised -- remove it from the list."
        fail=1
    fi
done <<<"$(known_list)"

# 4. The ratchet only ratchets if growing it is loud. "Shrink this list" in a comment is not a
#    mechanism: adding a line looks exactly like removing one in a diff.
known_count="$(grep -c . <<<"$(known_list)" || true)"
if [ "$known_count" -gt "$KNOWN_UNCOVERED_MAX" ]; then
    echo "KNOWN_UNCOVERED has grown to $known_count, above the ceiling of $KNOWN_UNCOVERED_MAX."
    echo "  Lower the list, or raise KNOWN_UNCOVERED_MAX in the same diff and say why."
    fail=1
fi

if [ "$fail" -ne 0 ]; then
    echo ""
    echo "❌ MCP tool coverage check failed."
    exit 1
fi

echo "✅ $tool_count advertised tool(s): every one is covered or recorded ($known_count recorded, ceiling $KNOWN_UNCOVERED_MAX)."
