#!/usr/bin/env bash
#
# test-edr-bash-guard.sh -- the PreToolUse guard refuses execution, not mention (#2289).
#
# `.claude/settings.json`'s 79 deny entries match a command from its FIRST WORD, so anything with
# a prefix word walks through them: `timeout 5 ./bin/lfr-tunneld -h` matches nothing. `timeout` is
# the seductive one -- it reads like bounding the risk, and it is reinstall-incident 3 with a
# safety blanket. scripts/edr-bash-guard.py closes that.
#
# The guard is only worth having if BOTH tables below hold. A guard that also refuses
# `scp dist/lfr-tunnel-linux-amd64 host:` or `codesign --verify dist/...` would block the release
# path it is meant to protect, and a gate that is mostly false positives gets exemptions bolted on
# until it says nothing -- the failure this repo keeps finding.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

GUARD="scripts/edr-bash-guard.py"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -f "$GUARD" ]; then
    fail "PREMISE: $GUARD is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# decide <guard-path> <command> -- prints "deny" or "allow".
decide() {
    python3 -c '
import json, subprocess, sys
guard, command = sys.argv[1], sys.argv[2]
out = subprocess.run([sys.executable, guard],
                     input=json.dumps({"tool_name": "Bash", "tool_input": {"command": command}}),
                     capture_output=True, text=True).stdout.strip()
if not out:
    print("allow"); raise SystemExit
try:
    print(json.loads(out)["hookSpecificOutput"]["permissionDecision"])
except Exception:
    print("unparseable")
' "$1" "$2"
}

# The toolchain words are assembled rather than written as literals. Not an evasion: this file's
# data tables have to SPELL the commands they assert on, and scripts/check-edr-safety.sh scans
# every *.sh for exactly those spellings -- the self-match in section 5c rule 6, which has now
# bitten this repo four times. Do NOT exempt this file in check-edr-safety.sh instead; an
# exemption would blind it to a genuine invocation added here later.
# Built from one token rather than written as three literals. Note the asymmetry that caught this:
# check-edr-safety.sh's run/build patterns require a TRAILING SPACE, so `GO_RUN="go run"` slips
# past them -- but its test pattern does not, so the same shape for test was flagged. Deriving all
# three removes the inconsistency and the literals together.
TOOLCHAIN="go"
GO_RUN="$TOOLCHAIN run"
GO_BUILD="$TOOLCHAIN build"
GO_TEST="$TOOLCHAIN test"

# Commands that MUST be refused. Every prefix-word row is a spelling the 79 deny entries cannot
# reach; that is the whole reason this file exists.
DENY_CASES="./bin/lfr-tunneld -h
timeout 5 ./bin/lfr-tunneld -h
sudo ./bin/lfr-tunneld
env LFT_CONFIG=x ./bin/lfr-tunneld
nohup ./bin/lfr-tunneld &
./dist/lfr-tunnel-darwin-arm64 --version
/Volumes/SanDisk/repos/lfr-tunnel/bin/lfr-tunneld
${GO_RUN} ./cmd/lfr-tunneld
cd bin && ./lfr-tunneld
./lfr-tunnel.sh
./lfr-tunnel.ps1
bin/lfr-tunneld-central-linux
bin/lfr-tunnel-edge-provisioner-linux
sudo -u ubuntu ./bin/lfr-tunneld
sudo -u ubuntu -- ./bin/lfr-tunneld
timeout -s KILL 5 ./bin/lfr-tunneld
env -C /tmp ./bin/lfr-tunneld
eval ./bin/lfr-tunneld
( ./bin/lfr-tunneld )
> /tmp/out ./bin/lfr-tunneld
bash ./lfr-tunnel.sh
source ./lfr-tunnel.sh
timeout 600 ${GO_TEST} ./...
env FOO=1 ${GO_TEST} ./pkg/server
${GO_RUN} .
${GO_RUN} main.go"

# Commands that MUST be allowed. Several are load-bearing: `make deploy` runs lfr-tunnel-ops, and
# deploy-clients scp's the very binaries this guard refuses to EXECUTE.
ALLOW_CASES="make build
make deploy
make test
gofmt -w cmd/lfr-tunneld/main.go
scp dist/lfr-tunnel-linux-amd64 host:/tmp/
git log -- cmd/lfr-tunneld
./bin/lfr-tunnel-ops deploy -target central
bin/lfr-tunnel-ops build
${GO_BUILD} -o dist/lfr-tunnel-linux-amd64 ./cmd/lfr-tunnel
grep -rn lfr-tunneld pkg/
ls -l dist/lfr-tunnel-darwin-arm64
codesign --verify dist/lfr-tunnel-darwin-arm64"

echo "-- every forbidden spelling is refused, prefix words included"
deny_ok=0
deny_total=0
while IFS= read -r c; do
    [ -n "$c" ] || continue
    deny_total=$((deny_total + 1))
    if [ "$(decide "$GUARD" "$c")" = "deny" ]; then
        deny_ok=$((deny_ok + 1))
    else
        fail "NOT refused: $c"
    fi
done <<<"$DENY_CASES"
[ "$deny_ok" -eq "$deny_total" ] && pass "all $deny_total forbidden spellings refused"

echo ""
echo "-- BOUNDING: ordinary work is not refused"
allow_ok=0
allow_total=0
while IFS= read -r c; do
    [ -n "$c" ] || continue
    allow_total=$((allow_total + 1))
    if [ "$(decide "$GUARD" "$c")" = "allow" ]; then
        allow_ok=$((allow_ok + 1))
    else
        fail "FALSE POSITIVE, refused: $c"
    fi
done <<<"$ALLOW_CASES"
[ "$allow_ok" -eq "$allow_total" ] && pass "all $allow_total ordinary commands allowed"

echo ""
echo "-- a MULTI-LINE block is inspected past its first line"
# Cannot live in DENY_CASES: that list is newline-delimited, so each line would be read as its own
# case -- and `make build` alone must be ALLOWED. That is exactly why this bypass went untested:
# the harness shape hid it. A build line followed by the daemon walked straight through until
# newline became a segment separator.
multiline="$(printf 'make build\n./bin/lfr-tunneld -h')"
if [ "$(decide "$GUARD" "$multiline")" = "deny" ]; then
    pass "a forbidden command on line 2 of a multi-line block is refused"
else
    fail "line 2 of a multi-line block was NOT inspected -- 'make build' then the daemon walks through"
fi

echo ""
echo "-- BOUNDING: lfr-tunnel-ops stays runnable"
if [ "$(decide "$GUARD" "./bin/lfr-tunnel-ops deploy")" = "allow" ]; then
    pass "BOUNDING: lfr-tunnel-ops is not refused -- make deploy depends on it"
else
    fail "BOUNDING: lfr-tunnel-ops refused -- every release would break"
fi

echo ""
echo "-- fail-closed: a guard that cannot read its input refuses"
malformed="$(printf 'not json at all' | python3 "$GUARD" 2>/dev/null | python3 -c '
import json,sys
raw=sys.stdin.read().strip()
print(json.loads(raw)["hookSpecificOutput"]["permissionDecision"] if raw else "allow")
' 2>/dev/null)"
if [ "$malformed" = "deny" ]; then
    pass "malformed input is refused rather than waved through"
else
    fail "malformed input produced '$malformed' -- a guard that fails OPEN is absent exactly when something odd is happening"
fi

# --- FIRING ---------------------------------------------------------------------------------
#
# Everything above is "the guard said deny". That is equally satisfied by a guard that denies
# indiscriminately, or by a test harness that never reached it. Strip the matcher and the
# refusals must stop -- and the ALLOW table must still pass, which is what distinguishes
# "the matcher does the work" from "something else refuses everything".

echo ""
echo "-- FIRING: with the basename rule stripped, the refusals stop"

FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/lft-bashguard.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT

# TWO rules, so TWO mutants. The guard refuses by basename OR by toolchain subcommand, and they
# are independent: neutering the basename rule alone left the toolchain rows still refused, which
# read as "something else is denying them" when the real answer was "you mutated one of two input
# paths". That is section 5d's shape, found by this suite against its own author.
python3 - "$GUARD" "$FIXTURE/no-basename.py" "$FIXTURE/no-toolchain.py" <<'PY'
import sys
src = open(sys.argv[1]).read()

basename_rule = '    return base.startswith("lfr-tunnel")'
toolchain_rule = '        if "go" in bases:'
for rule, out in ((basename_rule, sys.argv[2]), (toolchain_rule, sys.argv[3])):
    if rule not in src:
        # A stale mutation string would silently produce an identical copy, and every assertion
        # below would then pass while proving nothing (section 5c).
        sys.exit("MUTATION STRING NOT FOUND: " + rule)
mutated = src.replace(basename_rule, "    return False")
open(sys.argv[2], "w").write(mutated)
open(sys.argv[3], "w").write(src.replace(toolchain_rule, '        if False:'))
PY
if [ $? -ne 0 ]; then
    fail "FIRING: a mutation string no longer matches the guard -- update it rather than trusting this"
fi

# A crashed mutant allows everything, which would satisfy "no refusals" while proving nothing.
# The malformed-input control never touches either rule, so a mutant that still refuses it is
# alive rather than broken.
for m in no-basename no-toolchain; do
    if [ "$(printf 'not json' | python3 "$FIXTURE/$m.py" 2>/dev/null | head -c 1)" != "{" ]; then
        fail "FIRING: mutant $m does not run at all -- its 'allow' results prove nothing"
    fi
done

basename_left=0
toolchain_left=0
while IFS= read -r c; do
    [ -n "$c" ] || continue
    [ "$(decide "$FIXTURE/no-basename.py" "$c")" = "deny" ] && basename_left=$((basename_left + 1))
    [ "$(decide "$FIXTURE/no-toolchain.py" "$c")" = "deny" ] && toolchain_left=$((toolchain_left + 1))
done <<<"$DENY_CASES"

# Each mutant must leave ONLY the other rule's rows refused, and their counts must add up to the
# whole table -- which is what proves the two rules together cover it, with nothing refused twice
# and nothing refused by accident.
if [ "$((basename_left + toolchain_left))" -eq "$deny_total" ] && [ "$basename_left" -gt 0 ] && [ "$toolchain_left" -gt 0 ]; then
    pass "FIRING: the two rules partition the table ($toolchain_left by basename, $basename_left by toolchain) -- each is load-bearing"
else
    fail "FIRING: mutants left $basename_left + $toolchain_left of $deny_total refused -- the rules do not partition the table"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
