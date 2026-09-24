#!/usr/bin/env bash
# confirm-tap-version.sh -- prove the Homebrew tap (or Scoop bucket) really advertises the
# version that was just pushed, and say WHICH of the two possible causes a mismatch has (#2204).
#
# The step this replaces (.github/workflows/tap-bucket.yml, "Confirm the tap now advertises this
# version") read the formula back exactly once, immediately after the push, and failed with:
#
#   ::error::The tap still does not advertise $want after a push that reported success.
#
# One message, two causes -- the same defect #2203 fixed for the release-asset listing:
#
#   A. the push genuinely did not land, or landed something else
#   B. the push landed and GitHub's CONTENTS API has not caught up
#
# GitHub's contents API is served from a cache keyed on the ref, so a read taken seconds after a
# push to the default branch can legitimately return the previous blob. B is a correct release
# failing its own confirmation, and the message sent whoever read it looking for A.
#
# Two independent observations are therefore used here, in order:
#
#   1. POLL the default-branch read with exponential backoff until it agrees or the budget
#      expires. Not a fixed `sleep`: a constant only moves the race.
#   2. If it never agrees, re-read the file PINNED TO AN EXPLICIT COMMIT SHA, resolved through
#      the Git Data API (`git/ref/heads/<default branch>`), which reads the git backend rather
#      than the contents cache. A sha-pinned contents read is immutable, so it cannot be stale
#      in the way a branch-pinned one can. That answers definitively:
#        head commit carries the wanted version -> the push LANDED; the cache is behind (pass)
#        head commit carries a different version -> the push DID NOT LAND (fail)
#        head commit carries no version at all   -> the formula rendered broken (fail)
#        the head could not be resolved          -> undetermined, so nothing was verified (fail)
#
# Deliberately NOT a second read of the same contents endpoint: two samples of one cached view
# are one observation taken twice, which is exactly the thing that cannot tell A from B
# (github-workflow §5c -- "if this assertion fails, is there exactly one thing that could have
# caused it?").
#
# Deliberately NOT the commits LIST endpoint for the head sha either. If that listing lagged it
# would hand back the PREVIOUS head, the pinned read would show the previous version, and B
# would be reported as A -- the original bug wearing the fix's clothes.
#
# Exit codes:
#   0  the tap advertises the wanted version (possibly with a lagging cache)
#   1  it does not, or which of the two causes applies could not be determined
#
# bash 3.2 clean -- no associative arrays, no mapfile, no case modification. It is reachable
# from `make confirm-tap`, so tests/hooks/test-shell-portability.sh derives it into the portable
# set, and tests/hooks/test-tap-version-confirmation.sh executes it on the developer's own
# /bin/bash.

set -uo pipefail

REPO="${1:-${LFT_TAP_REPO:-}}"
FILE_PATH="${2:-${LFT_TAP_PATH:-}}"
WANT="${3:-${LFT_TAP_VERSION:-}}"

# Seams. The tests stub `gh` on PATH; LFT_GH exists so a caller can point at a specific binary.
GH_BIN="${LFT_GH:-gh}"
SLEEP_CMD="${LFT_TAP_SLEEP:-sleep}"

# How long the cached read is allowed to lag before the sha-pinned read is asked instead. 30s
# covers the ordinary case at zero cost on the happy path, where the first poll already agrees.
SETTLE_SECONDS="${LFT_TAP_SETTLE_SECONDS:-30}"
POLL_INITIAL="${LFT_TAP_POLL_INITIAL:-1}"
POLL_MAX="${LFT_TAP_POLL_MAX:-8}"

notice() { printf '::notice::%s\n' "$*"; }
warn() { printf '::warning::%s\n' "$*"; }
err() { printf '::error::%s\n' "$*"; }

usage() {
    err "usage: $0 <owner/repo> <path/in/repo> <version>"
    err "   or: set LFT_TAP_REPO, LFT_TAP_PATH and LFT_TAP_VERSION"
}

# A leading v is accepted so the workflow can pass its own tag input straight through.
WANT="${WANT#v}"

if [ -z "$REPO" ] || [ -z "$FILE_PATH" ]; then
    usage
    exit 1
fi

# Anti-vacuity. An empty expected version would compare equal to an unparseable read and report
# a confirmed tap having established nothing.
if [ -z "$WANT" ]; then
    err "No expected version was given, so there is nothing to confirm $REPO/$FILE_PATH against."
    err "Refusing to report the tap confirmed against an empty expectation."
    exit 1
fi

now() { date +%s; }
one_line() { printf '%s\n' "$1" | grep -v '^[[:space:]]*$' | head -2 | tr '\n' ' ' | sed 's/[[:space:]]*$//'; }

# Matches the Homebrew formula's `version "1.2.3"` and the Scoop manifest's `"version": "1.2.3"`
# with one expression, so the same confirmation reads either file.
extract_version() {
    printf '%s\n' "$1" \
        | grep -oE 'version"?[[:space:]]*:?[[:space:]]*"[0-9]+(\.[0-9]+)*"' \
        | head -1 \
        | sed -E 's/.*"([0-9]+(\.[0-9]+)*)"$/\1/'
}

# The raw media type rather than `--jq .content | base64 -d`: it removes a base64 flag that is
# spelt differently on macOS and GNU, and it is the same single request either way.
read_contents() {
    _ref="${1:-}"
    if [ -n "$_ref" ]; then
        "$GH_BIN" api -H "Accept: application/vnd.github.raw" \
            "repos/${REPO}/contents/${FILE_PATH}?ref=${_ref}" 2>&1
    else
        "$GH_BIN" api -H "Accept: application/vnd.github.raw" \
            "repos/${REPO}/contents/${FILE_PATH}" 2>/dev/null
    fi
}

echo "Confirming ${REPO}/${FILE_PATH} advertises ${WANT}"

# ---------------------------------------------------------------------------------------------
# 1. Let the cached default-branch read settle.
# ---------------------------------------------------------------------------------------------
SETTLE_GOT=""
SETTLE_POLLS=0
SETTLE_ELAPSED=0
settle() {
    _started="$(now)"
    _deadline=$((_started + SETTLE_SECONDS))
    _delay="$POLL_INITIAL"
    SETTLE_POLLS=0
    while :; do
        SETTLE_POLLS=$((SETTLE_POLLS + 1))
        SETTLE_GOT="$(extract_version "$(read_contents)")"
        [ "$SETTLE_GOT" = "$WANT" ] && break
        [ "$(now)" -ge "$_deadline" ] && break
        "$SLEEP_CMD" "$_delay"
        _delay=$((_delay * 2))
        [ "$_delay" -gt "$POLL_MAX" ] && _delay="$POLL_MAX"
    done
    SETTLE_ELAPSED=$(( $(now) - _started ))
}

settle

if [ "$SETTLE_GOT" = "$WANT" ]; then
    echo "The tap agreed after ${SETTLE_POLLS} poll(s) / ${SETTLE_ELAPSED}s."
    notice "${REPO}/${FILE_PATH} advertises ${WANT}. Confirmed."
    exit 0
fi

# ---------------------------------------------------------------------------------------------
# 2. Ask the commit the branch actually points at. This is the step that separates "the push did
#    not land" from "the contents API has not caught up", and it is the whole point of the fix.
# ---------------------------------------------------------------------------------------------
warn "${REPO}/${FILE_PATH} still advertises ${SETTLE_GOT:-<no version>} rather than ${WANT} after ${SETTLE_POLLS} poll(s) / ${SETTLE_ELAPSED}s. Reading the file pinned to the branch head instead, to tell a failed push apart from a stale cache."

# The error text lands in BRANCH on failure, which is why the assignment is the condition: the
# reason has to reach the log, and a bare `||` would discard it.
if ! BRANCH="$("$GH_BIN" api "repos/${REPO}" --jq '.default_branch' 2>&1)" || [ -z "$BRANCH" ]; then
    err "Could not resolve ${REPO}'s default branch, so it is UNDETERMINED whether the push landed: $(one_line "$BRANCH")"
    err "Refusing to report the tap confirmed, and refusing to blame the push, on no evidence."
    exit 1
fi

HEAD_SHA="$("$GH_BIN" api "repos/${REPO}/git/ref/heads/${BRANCH}" --jq '.object.sha' 2>&1)"
if ! printf '%s' "$HEAD_SHA" | grep -qE '^[0-9a-f]{7,40}$'; then
    err "Could not resolve the head of ${REPO}@${BRANCH}, so it is UNDETERMINED whether the push landed: $(one_line "$HEAD_SHA")"
    err "Refusing to report the tap confirmed, and refusing to blame the push, on no evidence."
    exit 1
fi

PINNED_RAW="$(read_contents "$HEAD_SHA")"
PINNED_RC=$?
PINNED_GOT="$(extract_version "$PINNED_RAW")"

# A failed read is undetermined even if its error text happens to contain something version-shaped
# -- the verdicts below must only ever be reached from a read that actually succeeded.
if [ "$PINNED_RC" -ne 0 ]; then
    err "Could not read ${FILE_PATH} at ${REPO}@${HEAD_SHA}, so it is UNDETERMINED whether the push landed: $(one_line "$PINNED_RAW")"
    err "Refusing to report the tap confirmed, and refusing to blame the push, on no evidence."
    exit 1
fi

# ---------------------------------------------------------------------------------------------
# 3. Verdict. Exactly one of these can be reached, and each names its own cause.
# ---------------------------------------------------------------------------------------------
if [ "$PINNED_GOT" = "$WANT" ]; then
    warn "THE PUSH LANDED. ${FILE_PATH} at ${REPO}@${HEAD_SHA} -- the current head of ${BRANCH} -- already advertises ${WANT}. The default-branch read is serving a cached view that has not caught up: that is read-after-write lag in GitHub's contents API, NOT a failed push. No action is needed."
    notice "${REPO}/${FILE_PATH} advertises ${WANT}. Confirmed."
    exit 0
fi

if [ -z "$PINNED_GOT" ]; then
    err "THE PUSH LANDED SOMETHING BROKEN. ${FILE_PATH} at ${REPO}@${HEAD_SHA} -- the current head of ${BRANCH} -- carries no version string at all, so the file was rendered or written wrongly rather than left stale."
    err "Homebrew and Scoop users will not get ${WANT}. Fix the rendering and re-run this workflow."
    exit 1
fi

err "THE PUSH DID NOT LAND. ${FILE_PATH} at ${REPO}@${HEAD_SHA} -- the current head of ${BRANCH} -- still advertises ${PINNED_GOT}, not ${WANT}. This is not a stale cache: the commit the branch points at does not carry the new version."
err "Homebrew and Scoop users will stay on ${PINNED_GOT}. Re-run this workflow once the cause is fixed; it is dispatchable precisely so this costs a re-run rather than a release."
exit 1
