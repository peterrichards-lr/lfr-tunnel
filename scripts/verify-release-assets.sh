#!/usr/bin/env bash
# verify-release-assets.sh -- prove a published GitHub release carries every artefact that was
# built, and say WHICH of the two possible causes a shortfall has (#2203).
#
# The gate this replaces read the asset listing once per artefact and treated "not in the
# listing" as "not published". Those are different facts. Measured on v1.48.52:
#
#   09:09:51  softprops/action-gh-release uploaded all nine assets, all ✅
#   09:09:53  gh release view --json assets returned FOUR of them
#   09:10:18  gh release upload  -> HTTP 422: ReleaseAsset.name already exists
#   09:11:39  ##[error]5 built artefact(s) are missing ... INCOMPLETE and must not be shipped
#
# The release was complete. GitHub's release-asset LISTING is eventually consistent; the upload
# endpoint is not -- it enforces name uniqueness against the primary. So the run already held
# proof that the assets existed, printed it in its own log, and failed anyway, because the
# assertion could only say "missing" (§5c: one message, two causes).
#
# Two independent observations are therefore used here, in order:
#
#   1. POLL the listing with exponential backoff until it agrees or the budget expires. This is
#      not a fixed `sleep`: a constant only moves the race, and v1.48.52's listing was still
#      stale 105 seconds later, so no constant would have covered it.
#   2. PROBE anything still unlisted through the upload endpoint, which answers definitively:
#        rc 0                -> the asset really was absent; it is now published (recovered)
#        "already exists"    -> the asset IS attached; the LISTING is lagging (not missing)
#        any other failure   -> genuinely missing and could not be published (FAIL the job)
#
# Deliberately NOT `gh release upload --clobber`, which the inlined step used. Clobber returns 0
# whether the asset was absent or present, which is exactly what makes the two causes
# indistinguishable. Dropping it is what makes the 422 readable. It loses nothing: clobber's
# stated purpose was unblocking a retry over a leftover asset, and a leftover asset by definition
# carries the name, so the plain upload's 422 reports it either way.
#
# Exit codes:
#   0  the release is complete (possibly after re-uploading, possibly with a lagging listing)
#   1  the release is genuinely incomplete, or nothing was verified at all
#
# bash 3.2 clean -- no associative arrays, no mapfile, no case modification. It is reachable from
# `make verify-release`, so tests/hooks/test-shell-portability.sh derives it into the portable
# set, and tests/hooks/test-release-asset-verification.sh executes it on the developer's own
# /bin/bash.

set -uo pipefail

TAG="${1:-${LFT_RELEASE_TAG:-}}"
DIST="${2:-${LFT_RELEASE_DIST:-dist}}"

# Seams. The tests stub `gh` on PATH; LFT_GH exists so a caller can point at a specific binary.
GH_BIN="${LFT_GH:-gh}"
SLEEP_CMD="${LFT_RELEASE_SLEEP:-sleep}"
# The clock is a seam for the same reason the sleep is, and its absence made this suite flaky on a
# developer's machine while staying green in CI (#2238, the sibling of #2220 which fixed exactly
# this in confirm-tap-version.sh). The settle loop is bounded on real time, so a case expecting
# agreement on the third poll silently meant "the third poll, if three fit in the budget on this
# box" -- a property of how fast a stubbed `gh` spawns, not of the polling logic. A test that can
# go red without the subject changing is worse than no test, and this one guards releases.
NOW_CMD="${LFT_RELEASE_NOW:-}"

# How long the listing is allowed to lag before the upload endpoint is asked instead. 30s covers
# the ordinary case at zero cost on the happy path, where the first poll already agrees.
SETTLE_SECONDS="${LFT_RELEASE_SETTLE_SECONDS:-30}"
POLL_INITIAL="${LFT_RELEASE_POLL_INITIAL:-1}"
POLL_MAX="${LFT_RELEASE_POLL_MAX:-8}"

notice() { printf '::notice::%s\n' "$*"; }
warn() { printf '::warning::%s\n' "$*"; }
err() { printf '::error::%s\n' "$*"; }

if [ -z "$TAG" ]; then
    err "usage: $0 <tag> [dist-dir]  (or set LFT_RELEASE_TAG)"
    exit 1
fi

now() {
    if [ -n "$NOW_CMD" ]; then
        "$NOW_CMD"
    else
        date +%s
    fi
}
count_lines() { printf '%s' "$1" | grep -c . ; }

# ---------------------------------------------------------------------------------------------
# The expected set: what was actually built. An empty one must FAIL rather than report that all
# zero artefacts are published -- a scan of nothing reporting success is the shape this repo has
# been bitten by repeatedly (tests/hooks/test-gate-anti-vacuity.sh).
# ---------------------------------------------------------------------------------------------
EXPECTED=""
EXPECTED_COUNT=0
for f in "$DIST"/*; do
    [ -f "$f" ] || continue
    EXPECTED="${EXPECTED}$(basename "$f")
"
    EXPECTED_COUNT=$((EXPECTED_COUNT + 1))
done

if [ "$EXPECTED_COUNT" -eq 0 ]; then
    err "No built artefacts found in '$DIST', so there is nothing to verify $TAG against."
    err "Refusing to report a release complete against an empty expected set."
    exit 1
fi

echo "Verifying $EXPECTED_COUNT built artefact(s) from '$DIST' against release $TAG"

list_assets() {
    "$GH_BIN" release view "$TAG" --json assets --jq '.assets[].name' 2>/dev/null
}

# Echoes the names from $1 that the listing does not currently carry.
absent_from_listing() {
    _listing="$(list_assets)"
    while IFS= read -r _name; do
        [ -n "$_name" ] || continue
        printf '%s\n' "$_listing" | grep -qxF "$_name" || printf '%s\n' "$_name"
    done <<ABSENT_INPUT
$1
ABSENT_INPUT
}

# Poll until the listing carries every name in $1, or the budget expires. Results in globals
# rather than stdout so the poll count survives -- a command substitution would run this in a
# subshell and lose it.
SETTLE_REMAINING=""
SETTLE_POLLS=0
SETTLE_ELAPSED=0
settle() {
    _want="$1"
    _started="$(now)"
    _deadline=$((_started + SETTLE_SECONDS))
    _delay="$POLL_INITIAL"
    SETTLE_POLLS=0
    while :; do
        SETTLE_POLLS=$((SETTLE_POLLS + 1))
        SETTLE_REMAINING="$(absent_from_listing "$_want")"
        [ -z "$SETTLE_REMAINING" ] && break
        [ "$(now)" -ge "$_deadline" ] && break
        "$SLEEP_CMD" "$_delay"
        _delay=$((_delay * 2))
        [ "$_delay" -gt "$POLL_MAX" ] && _delay="$POLL_MAX"
    done
    SETTLE_ELAPSED=$(( $(now) - _started ))
}

# ---------------------------------------------------------------------------------------------
# 1. Let the listing settle.
# ---------------------------------------------------------------------------------------------
settle "$EXPECTED"

if [ -z "$SETTLE_REMAINING" ]; then
    echo "Asset listing agreed after ${SETTLE_POLLS} poll(s) / ${SETTLE_ELAPSED}s."
    notice "$TAG is COMPLETE: all $EXPECTED_COUNT built artefact(s) are published."
    exit 0
fi

# ---------------------------------------------------------------------------------------------
# 2. Ask the upload endpoint about whatever is still unlisted. This is the step that separates
#    "absent" from "unlisted", and it is the whole point of the fix.
# ---------------------------------------------------------------------------------------------
warn "$(count_lines "$SETTLE_REMAINING") artefact(s) were still not in ${TAG}'s asset listing after ${SETTLE_POLLS} poll(s) / ${SETTLE_ELAPSED}s. Asking the upload endpoint whether they are absent or merely unlisted."

RECOVERED=""
LAGGING=""
UNPUBLISHED=""
UNPUBLISHED_DETAIL=""

while IFS= read -r name; do
    [ -n "$name" ] || continue
    if [ ! -f "$DIST/$name" ]; then
        UNPUBLISHED="${UNPUBLISHED}${name}
"
        UNPUBLISHED_DETAIL="${UNPUBLISHED_DETAIL}  - ${name}: vanished from '$DIST' before it could be re-uploaded
"
        continue
    fi

    out="$("$GH_BIN" release upload "$TAG" "$DIST/$name" 2>&1)"
    rc=$?

    if [ "$rc" -eq 0 ]; then
        RECOVERED="${RECOVERED}${name}
"
        warn "$name was GENUINELY ABSENT from $TAG and has now been re-uploaded."
    elif printf '%s\n' "$out" | grep -qi 'already exist'; then
        evidence="$(printf '%s\n' "$out" | grep -i 'already exist' | head -1 | sed 's/^[[:space:]]*//')"
        LAGGING="${LAGGING}${name}
"
        warn "$name IS PRESENT on $TAG -- only the listing had not caught up. The upload endpoint refused it with \"${evidence}\", which GitHub returns only when an asset of that name is already attached to the release."
    else
        reason="$(printf '%s\n' "$out" | grep -v '^[[:space:]]*$' | head -2 | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
        [ -n "$reason" ] || reason="upload exited $rc with no output"
        UNPUBLISHED="${UNPUBLISHED}${name}
"
        UNPUBLISHED_DETAIL="${UNPUBLISHED_DETAIL}  - ${name}: ${reason}
"
    fi
done <<REMAINING
$SETTLE_REMAINING
REMAINING

# A recovered asset is published the moment its upload returns 0. Re-polling is only so the log
# says whether the listing has caught up -- it must not turn a successful upload back into a
# failure, or the fix reintroduces the bug it exists to remove.
if [ -n "$RECOVERED" ]; then
    settle "$RECOVERED"
    if [ -n "$SETTLE_REMAINING" ]; then
        warn "$(count_lines "$SETTLE_REMAINING") re-uploaded artefact(s) are not in the listing yet after ${SETTLE_ELAPSED}s. The uploads returned success, so they are published; the listing is behind."
    fi
fi

# ---------------------------------------------------------------------------------------------
# 3. Verdict. Exactly one of these can be reached, and each names its own cause.
# ---------------------------------------------------------------------------------------------
if [ -n "$UNPUBLISHED" ]; then
    err "$(count_lines "$UNPUBLISHED") built artefact(s) are GENUINELY MISSING from $TAG: absent from the asset listing AND rejected by the upload endpoint."
    printf '%s' "$UNPUBLISHED_DETAIL" | while IFS= read -r line; do
        [ -n "$line" ] && err "$line"
    done
    err "The release is INCOMPLETE and must not be treated as shipped."
    exit 1
fi

if [ -n "$LAGGING" ]; then
    warn "$(count_lines "$LAGGING") artefact(s) are published but absent from ${TAG}'s asset listing. That is read-after-write lag in GitHub's listing API, NOT a missing artefact: each was confirmed attached by the upload endpoint. No action is needed and the release IS complete."
fi

if [ -n "$RECOVERED" ]; then
    warn "$(count_lines "$RECOVERED") artefact(s) were genuinely absent and have been re-uploaded. Check the run above for why the original upload did not land."
fi

notice "$TAG is COMPLETE: all $EXPECTED_COUNT built artefact(s) are published."
exit 0
