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
# Fixing those two entries by hand would have left the class alive (section 5b): `lfr-tunnel.ps1`
# sat beside the denied `.sh` and `.bat` wrappers, every entry ended in ` *` so bare invocation was
# uncovered, and nothing read this file at all. So the forbidden set is DERIVED here, not listed:
#
#   binaries   = cmd/*                                 minus RUNNABLE_LOCALLY
#   artefacts  = the -o targets the build system writes, minus RUNNABLE_LOCALLY (by basename)
#   wrappers   = tracked root `lfr-tunnel.*` files
#
# The artefact row is not a refinement, it is the correction. The first version of this gate
# derived from `cmd/` alone, and `cmd/` directory names are NOT what this build system produces:
# it writes `dist/lfr-tunnel-darwin-arm64` and `bin/lfr-tunneld-central-linux`. So every release
# and deploy artefact was undenied, including a native runnable macOS client sitting in `dist/`.
# Add a binary, an artefact name or a wrapper and this goes red until someone decides which side
# of the line it is on -- a ratchet rather than an exclusion list (section 5b rule 5).
#
# Note what this does NOT claim, and read the BOUNDING case at the bottom before trusting it: a
# deny list can only enumerate SPELLINGS. It cannot match a command whose first word is not the
# binary, so `sudo`, `timeout`, `env` and `nohup` prefixes are not covered by anything here or
# anywhere else in the repo. Nothing here makes running the daemon safe. It narrows the ways to
# do it by accident.

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

# Artefacts: the paths the build system actually WRITES. This is a separate derivation from the
# one above and it is the one that matters, because the two do not agree and never have.
# `cmd/*/` gives `lfr-tunnel`, `lfr-tunneld`, `lfr-tunnel-edge-provisioner`; the `-o` targets are
# `dist/lfr-tunnel-darwin-arm64`, `bin/lfr-tunneld-central-linux` and friends. Deriving only from
# `cmd/` left every release and deploy artefact undenied -- including a native, immediately
# runnable macOS client in `dist/`, which the ops skill walks an agent into during every release
# to run `codesign --verify` against. That was the first version of this gate's blind spot, and it
# is why "a new binary now fails the build" was not true: the build system names its outputs, and
# this had never read them.
#
# `lfr-tunnel-ops` is filtered by BASENAME, not by prefix: `bin/lfr-tunnel` is a prefix of
# `bin/lfr-tunnel-ops`, so a glob would deny the deploy tool and break `make deploy`. The BOUNDING
# case below pins that.
artefacts=""
for a in $(
    { grep -hoE '"(dist|bin)/lfr-tunnel[A-Za-z0-9_.-]*"' pkg/ops/*.go 2>/dev/null | tr -d '"'
      grep -hoE -- '-o[[:space:]]+(dist|bin)/lfr-tunnel[A-Za-z0-9_.-]*' Makefile scripts/common/*.sh 2>/dev/null |
          sed -E 's/-o[[:space:]]+//'
    } | sort -u
); do
    [ "$(basename "$a")" = "$RUNNABLE_LOCALLY" ] && continue
    artefacts="$artefacts $a"
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

if [ -z "$(echo "$artefacts" | tr -d ' ')" ]; then
    fail "PREMISE: no build artefacts derived -- the -o scan found nothing, so dist/ and the -linux builds are unchecked"
else
    pass "PREMISE: artefacts derived from the build system's -o targets:$artefacts"
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
# fails the EDR gate for naming the thing it forbids. Splitting it keeps both guards honest: a
# LITERAL invocation added to this file later still fires the gate, and this one goes on requiring
# the deny entry. Do NOT add a file-level exemption to check-edr-safety.sh instead -- that is what
# test-edr-guard.sh accidentally rides on today (a `GOTMPDIR=` on one line exempts the whole file),
# and it is strictly broader than this.
#
# The residual blind spot, stated rather than glossed: a line added later spelled
# `$TOOLCHAIN_RUN ./cmd/...` -- this file's own idiom -- would NOT be flagged. The variable is an
# exemption; it is just narrower than a --exclude and visible at its use site.
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
            grep -qxF "Bash($form)"   <<<"$deny" || echo "Bash($form)"
            grep -qxF "Bash($form *)" <<<"$deny" || echo "Bash($form *)"
        done
    done

    for n in $artefacts; do
        for form in "$n" "./$n"; do
            grep -qxF "Bash($form)"   <<<"$deny" || echo "Bash($form)"
            grep -qxF "Bash($form *)" <<<"$deny" || echo "Bash($form *)"
        done
    done

    for n in $wrappers; do
        for form in "$n" "./$n"; do
            grep -qxF "Bash($form)"   <<<"$deny" || echo "Bash($form)"
            grep -qxF "Bash($form *)" <<<"$deny" || echo "Bash($form *)"
        done
    done
}

echo ""
echo "-- every derived executable is denied, bare and with arguments"

# The status matters as much as the output. If the deny list becomes unparseable -- a trailing
# comma, or someone writing JSONC comments into it, both real hazards for this file -- `read_deny`
# fails, `missing` is empty, and "empty means full coverage" would print a PASS over a file Claude
# Code may not even have loaded. That is section 5c rule 1: the headline assertion satisfied by the
# wrong cause.
if ! missing="$(missing_for "$SETTINGS")"; then
    fail "could not read the deny list from $SETTINGS -- malformed JSON? The deny list may not be loaded at all"
elif [ -z "$missing" ]; then
    pass "$SETTINGS covers every derived cmd/ binary, build artefact and wrapper"
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

# $TOOLCHAIN_RUN again, for the same reason as its use above: the literal spelled out here trips
# check-edr-safety.sh, which scans every *.sh for exactly this command. It caught this line on the
# commit that added it.
if read_deny "$SETTINGS" | grep -qE "^Bash\((${TOOLCHAIN_RUN} \./cmd/|\./|bin/|\./bin/)?${RUNNABLE_LOCALLY}( \*)?\)$"; then
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

# Four victims, not one. A single victim exercises one branch of `missing_for` and leaves the
# others unproven: drop the bare-form check at its `grep -qxF "Bash($form)"` line and the
# argument-free requirement -- the daemon STARTING, which is the whole point of this PR --
# evaporates for every binary while a single ` *` victim still reports a clean kill. One victim
# per loop, and one per form-shape.
CONTROL_VICTIMS="Bash(./bin/lfr-tunneld *)
Bash(./bin/lfr-tunneld)
Bash(./dist/lfr-tunnel-darwin-arm64)
Bash(./lfr-tunnel.ps1)"

# Explicit template, matching every other mktemp in tests/hooks/: BSD mktemp appends the random
# suffix to a `-t` prefix, GNU mktemp refuses a template with too few X's. Without it this dies on
# ubuntu-latest only -- a CI failure in the harness rather than the subject, which section 5c rule
# 5 warns reads like a finding.
CONTROL_FILE="$(mktemp "${TMPDIR:-/tmp}/lft-edr-deny-control.XXXXXX")"
cleanup() { rm -f "$CONTROL_FILE"; }
trap cleanup EXIT

while IFS= read -r victim; do
    [ -n "$victim" ] || continue

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
' "$SETTINGS" "$victim" "$CONTROL_FILE"
    rc=$?

    if [ "$rc" -eq 3 ]; then
        # Not a pass. If the victim is already gone the mutation is a no-op, and a no-op mutation
        # the checker "detects" proves nothing -- the same trap as a fixture describing a state
        # the system cannot produce.
        fail "CONTROL: victim '$victim' is not in the deny list -- pick a live entry"
    elif [ "$rc" -ne 0 ]; then
        fail "CONTROL: could not build the mutated copy for '$victim' (python3 exit $rc)"
    # Deliberately NOT `missing_for … | grep -q`. That pipeline failed for real at 79 entries:
    # grep -q exits on its first match, SIGPIPEs the still-writing producer, and `set -o pipefail`
    # turns that into a non-zero status -- reported as "was NOT detected" for an entry the check
    # had correctly found. A harness failure wearing a finding's clothes (section 5c rule 5), and
    # it only bit the two victims that sort early enough to leave output unwritten.
    elif control_missing="$(missing_for "$CONTROL_FILE")" && grep -qxF "$victim" <<<"$control_missing"; then
        pass "CONTROL: removing '$victim' is detected, and named"
    else
        fail "CONTROL: removing '$victim' was NOT detected -- this guard proves nothing"
    fi
done <<CONTROLEOF
$CONTROL_VICTIMS
CONTROLEOF

# --- BOUNDING: what this mechanism CANNOT do, asserted rather than commented ---------------
#
# A deny list enumerates spellings. It cannot match a command whose FIRST WORD is not the binary,
# so `timeout 5 ./bin/lfr-tunneld`, `sudo ./bin/lfr-tunneld`, `env FOO=1 ./bin/lfr-tunneld` and
# `nohup ./bin/lfr-tunneld &` all walk straight through however many entries are listed. Nothing
# else in the repo covers that either: the go PATH shim refuses `go run`/`go test` only, and
# check-edr-safety.sh is a static scan of tracked source, not of an agent's ad-hoc command.
#
# That limit is not fixable here, so the requirement is that the skill SAYS so. A rule claiming
# more enforcement than it has is worse than the original gap, because it stops the next agent
# looking -- section 5b rule 6: state a gate's blind spot as a test, not a comment. If someone
# closes this class properly (a PreToolUse-style hook matching the whole command string), this
# assertion goes red and the skill's wording has to be revisited deliberately.

echo ""
echo "-- BOUNDING: the skill states this mechanism's blind spot"

SKILL=".agents/skills/edr-constraints/SKILL.md"
if grep -q 'prefix word' "$SKILL" && grep -qE 'sudo|timeout' "$SKILL"; then
    pass "BOUNDING: $SKILL names the prefix-word blind spot"
else
    fail "BOUNDING: $SKILL no longer names the prefix-word blind spot -- either it was closed (update this assertion) or the caveat was dropped (restore it)"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
