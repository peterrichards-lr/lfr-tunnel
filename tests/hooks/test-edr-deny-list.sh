#!/bin/bash
#
# test-edr-deny-list.sh -- the EDR deny list must cover every locally-unsafe executable (#2283).
#
# `.agents/skills/edr-constraints/SKILL.md` says there is no verified-safe way to execute
# `lfr-tunneld` or `lfr-tunnel` on this machine, regardless of build location. Two of the three
# environment reinstalls it records are the DAEMON. The harness deny list in
# `.claude/settings.json` named only the client: `Bash(lfr-tunnel *)` requires a space after
# `lfr-tunnel`, so it never matched `bin/lfr-tunneld`, and `Bash(go run ./cmd/lfr-tunnel *)`
# never matched `go run ./cmd/lfr-tunneld`. The rule with the worst track record in this repo was
# the one rule nothing enforced.
#
# Fixing those two entries by hand would have left the class alive (§5b): `lfr-tunnel.ps1` sat
# beside the denied `.sh` and `.bat` wrappers, every entry ended in ` *` so bare invocation was
# uncovered, and nothing read this file at all. So the forbidden set is DERIVED here, not listed:
#
#   binaries  = cmd/*            minus RUNNABLE_LOCALLY
#   wrappers  = tracked root `lfr-tunnel.*` files
#
# Add a `cmd/` binary or a root wrapper and this goes red until someone decides which side of the
# line it is on. That is a ratchet rather than an exclusion list -- the list can only shrink by
# someone making a decision, never by drifting (§5b rule 5).
#
# Note what this does NOT claim: nothing here makes running the daemon safe, and nothing here
# adds a way to. The skill's conclusion stands. This only makes the harness refuse the command
# instead of relying on each agent having read the paragraph.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

SETTINGS=".claude/settings.json"

# The one cmd/ binary the EDR skill names as safe to run directly ("the `lfr-tunnel-ops` (deploy
# tooling) binary"). Everything else under cmd/ is a server or client process: it links unsigned
# and opens sockets, which is precisely the shape SentinelOne acted on. `lfr-tunnel-edge-provisioner`
# is in the forbidden set deliberately -- it binds a loopback listener and only ever runs on the
# central host under systemd, deployed by scp (scripts/common/setup-edge-provisioner.sh), so it
# has no local-execution use case to protect.
RUNNABLE_LOCALLY="lfr-tunnel-ops"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# --- derive the forbidden set -------------------------------------------------------------

binaries=""
for d in cmd/*/; do
    name="$(basename "$d")"
    [ "$name" = "$RUNNABLE_LOCALLY" ] && continue
    binaries="$binaries $name"
done

wrappers=""
for w in $(git ls-files -- 'lfr-tunnel.*'); do
    wrappers="$wrappers $w"
done

# --- PREMISE / anti-vacuity ---------------------------------------------------------------
#
# Every assertion below is a loop over these two sets. If either is empty the whole suite passes
# over nothing, which is the failure mode this repo has shipped more than once (#1779, #1929).
# A wrong `cd`, a renamed cmd/ layout or a `git ls-files` that finds no repo all produce exactly
# that, and all of them look like a clean run.

echo "-- premise: the derived sets are non-empty and the exemption is real"

if [ -z "$(echo "$binaries" | tr -d ' ')" ]; then
    fail "PREMISE: no binaries derived from cmd/ -- every assertion below would be vacuous"
else
    pass "PREMISE: binaries derived from cmd/:$binaries"
fi

if [ -z "$(echo "$wrappers" | tr -d ' ')" ]; then
    fail "PREMISE: no root wrappers derived -- every wrapper assertion below would be vacuous"
else
    pass "PREMISE: wrappers derived from the repo root:$wrappers"
fi

# If the exemption names something that does not exist, the subtraction above removed nothing and
# the set is "all of cmd/" by accident rather than by decision.
if [ -d "cmd/$RUNNABLE_LOCALLY" ]; then
    pass "PREMISE: the exemption cmd/$RUNNABLE_LOCALLY exists, so the subtraction is real"
else
    fail "PREMISE: RUNNABLE_LOCALLY=$RUNNABLE_LOCALLY has no cmd/ directory -- exemption is a no-op"
fi

if [ ! -f "$SETTINGS" ]; then
    fail "$SETTINGS not found -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# --- the check, factored so the CONTROL can run it against a mutated copy ------------------

# read_deny <settings-path> -- one deny pattern per line.
read_deny() {
    python3 -c '
import json, sys
with open(sys.argv[1]) as fh:
    data = json.load(fh)
for entry in data.get("permissions", {}).get("deny", []):
    print(entry)
' "$1"
}

# The toolchain form is assembled from a variable rather than written as one literal, and that is
# not an evasion -- it is the self-match case in §5c rule 6 ("a checker that greps for a pattern
# will find that pattern in its own source"). scripts/check-edr-safety.sh scans every *.sh for the
# command spelled out below, and this file's entire subject is that command, so written inline it
# fails the EDR gate on line 117 for naming the thing it forbids. Splitting it keeps both guards
# honest: the gate goes on flagging real invocations in this file, and this one goes on requiring
# the deny entry. Do NOT add an exemption to check-edr-safety.sh for this -- an exemption would
# blind it to a genuine invocation added here later.
TOOLCHAIN_RUN="go run"

# missing_for <settings-path> -- prints every required pattern the file does not carry, one per
# line. Empty output means full coverage.
missing_for() {
    local settings="$1" deny form n
    deny="$(read_deny "$settings")" || return 1

    for n in $binaries; do
        for form in "$n" "./$n" "bin/$n" "./bin/$n" "$TOOLCHAIN_RUN ./cmd/$n"; do
            # Both halves matter. The bare form is argument-free invocation -- `./bin/lfr-tunneld`
            # with no flags is the daemon STARTING, the worst case rather than an exempt one --
            # and the ` *` form is the same command with arguments, which is how the 2026-09-20
            # incident (`./bin/lfr-tunneld -h`) was actually spelled.
            printf '%s\n' "$deny" | grep -qxF "Bash($form)"   || echo "Bash($form)"
            printf '%s\n' "$deny" | grep -qxF "Bash($form *)" || echo "Bash($form *)"
        done
    done

    for n in $wrappers; do
        for form in "$n" "./$n"; do
            printf '%s\n' "$deny" | grep -qxF "Bash($form)"   || echo "Bash($form)"
            printf '%s\n' "$deny" | grep -qxF "Bash($form *)" || echo "Bash($form *)"
        done
    done
}

echo ""
echo "-- every derived executable is denied, bare and with arguments"

missing="$(missing_for "$SETTINGS")"
if [ -z "$missing" ]; then
    pass "$SETTINGS covers every form of every derived executable"
else
    while IFS= read -r m; do
        [ -n "$m" ] && fail "missing from $SETTINGS deny list: $m"
    done <<EOF
$missing
EOF
fi

# --- BOUNDING: the exemption is deliberate and must stay visible ---------------------------
#
# Passes before and after the fix, on purpose: it pins the edge so that crossing it turns this
# suite red. If someone denies the deploy tool, `make deploy` and every release stop working, and
# the failure would otherwise surface as an unexplained permission refusal mid-release.

echo ""
echo "-- BOUNDING: the one runnable binary stays runnable"

if read_deny "$SETTINGS" | grep -qE "^Bash\((\./)?(bin/)?${RUNNABLE_LOCALLY}( \*)?\)$"; then
    fail "BOUNDING: $RUNNABLE_LOCALLY is denied -- the EDR skill names it safe, and deploy needs it"
else
    pass "BOUNDING: $RUNNABLE_LOCALLY is not denied"
fi

# --- CONTROL ------------------------------------------------------------------------------
#
# Everything above is "grep found a string". That is satisfied just as well by a checker that
# cannot fail -- a bad derivation, a `grep -q` that always succeeds, a `$binaries` that silently
# lost its contents. So: take the real file, delete exactly one required entry, and require the
# check to notice THAT entry. Reading "it failed" is not the check; reading which entry it named
# is (§5c rule 2).

echo ""
echo "-- CONTROL: removing one deny entry must be detected"

CONTROL_VICTIM="Bash(./bin/lfr-tunneld *)"
# Explicit template, matching every other mktemp in tests/hooks/: BSD mktemp appends the random
# suffix to a `-t` prefix, GNU mktemp refuses a template with too few X's. Without it this dies on
# ubuntu-latest only -- a CI failure in the harness rather than the subject, which §5c rule 5
# warns reads like a finding.
CONTROL_FILE="$(mktemp "${TMPDIR:-/tmp}/lft-edr-deny-control.XXXXXX")"
cleanup() { rm -f "$CONTROL_FILE"; }
trap cleanup EXIT

python3 -c '
import json, sys
path, victim, out = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as fh:
    data = json.load(fh)
deny = data["permissions"]["deny"]
if victim not in deny:
    sys.exit(3)
data["permissions"]["deny"] = [e for e in deny if e != victim]
with open(out, "w") as fh:
    json.dump(data, fh, indent=2)
' "$SETTINGS" "$CONTROL_VICTIM" "$CONTROL_FILE"
rc=$?

if [ "$rc" -eq 3 ]; then
    # Not a pass. If the victim is gone the mutation is a no-op, and a no-op mutation that the
    # checker "detects" would be meaningless -- the same trap as a test whose fixture describes a
    # state the system cannot produce.
    fail "CONTROL: victim '$CONTROL_VICTIM' is not in the deny list -- pick a live entry"
elif [ "$rc" -ne 0 ]; then
    fail "CONTROL: could not build the mutated settings copy (python3 exit $rc)"
else
    control_missing="$(missing_for "$CONTROL_FILE")"
    if printf '%s\n' "$control_missing" | grep -qxF "$CONTROL_VICTIM"; then
        pass "CONTROL: the check names the removed entry ($CONTROL_VICTIM)"
    else
        fail "CONTROL: removing '$CONTROL_VICTIM' was NOT detected -- this guard proves nothing"
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
