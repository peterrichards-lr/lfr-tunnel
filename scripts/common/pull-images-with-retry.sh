#!/usr/bin/env bash
# pull-images-with-retry.sh — pre-pull an e2e stack's images, retrying transient registry errors
#
# Docker Hub returned a 504 pulling node:20-alpine and failed a required check on a PR that
# touched only a shell script and two docs files (#1530). That was the fourth infrastructure
# flake of the day: three were proxy.golang.org dropping an HTTP/2 stream mid-fetch, fixed in
# #1506 by retrying `go mod download`. The base image pull happens earlier, inside BuildKit's
# resolve step, so that fix does not reach it.
#
# Retrying is safe by construction here for the same reason it was there: these are immutable,
# digest-addressed artefacts. A corrupted or substituted response fails the digest check rather
# than being retried into acceptance.
#
# TWO SOURCES, which is the part worth stating -- a loop over only one of them looks like a fix
# and leaves half the pulls unprotected:
#
#   * compose `image:` services      nginx:alpine, axllent/mailpit, keycloak
#   * `FROM` lines in the Dockerfiles those compose files build   golang, node, alpine
#
# Derived from the compose file rather than listed, so an image added later is covered without
# anyone remembering this script exists -- the same reasoning as check-theme-contrast.cjs
# discovering theme files and test-shell-portability.sh deriving its script set.
#
# NEVER FATAL. This is an optimisation that absorbs a transient error, not a new gate: if a pull
# genuinely fails, the build that follows fails with its own, more accurate message. A mis-parsed
# image name must not be able to fail the suite on its own.
set -uo pipefail

ATTEMPTS="${LFT_PULL_ATTEMPTS:-3}"
SLEEP="${LFT_PULL_SLEEP:-5}"

if [ "$#" -eq 0 ]; then
    echo "Usage: $0 <docker-compose.yml> [more-compose-files...]" >&2
    exit 2
fi

# images_from_compose prints the `image:` values of a compose file. A service that builds has no
# image: line, so nothing here needs to exclude it.
images_from_compose() {
    sed -nE 's/^[[:space:]]*image:[[:space:]]*"?([^"[:space:]]+)"?.*/\1/p' "$1"
}

# base_images_from_dockerfile prints the images a Dockerfile pulls.
#
# Skips two things that are not pullable: the `--platform=$BUILDPLATFORM` prefix, and any FROM
# that names an earlier stage rather than an image (`FROM builder`). Getting the second wrong
# would send docker looking for an image called "builder" on every run.
base_images_from_dockerfile() {
    local df="$1" stages="" line img
    [ -f "$df" ] || return 0
    while IFS= read -r line; do
        # Strip the FROM keyword and any --flag arguments before the image name.
        img=$(printf '%s' "$line" | sed -E 's/^FROM[[:space:]]+//; s/^(--[^[:space:]]+[[:space:]]+)*//')
        # The image is the first token; an "AS <stage>" suffix follows it.
        local name stage
        name=$(printf '%s' "$img" | awk '{print $1}')
        stage=$(printf '%s' "$img" | awk 'tolower($2)=="as"{print $3}')
        case " $stages " in
            *" $name "*) ;;            # references an earlier stage, not a registry image
            *) printf '%s\n' "$name" ;;
        esac
        [ -n "$stage" ] && stages="$stages $stage"
    done < <(grep -E '^FROM[[:space:]]' "$df")
}

# dockerfiles_from_compose resolves each `dockerfile:` entry against the repo root, which is
# where the compose files' build contexts point.
dockerfiles_from_compose() {
    local compose="$1" root
    root=$(cd "$(dirname "$compose")/../.." && pwd)
    sed -nE 's/^[[:space:]]*dockerfile:[[:space:]]*"?([^"[:space:]]+)"?.*/\1/p' "$compose" \
        | while IFS= read -r df; do
            [ -f "$root/$df" ] && printf '%s\n' "$root/$df"
        done
}

IMAGES=""
for compose in "$@"; do
    if [ ! -f "$compose" ]; then
        echo "[pull] $compose not found; skipping."
        continue
    fi
    IMAGES="$IMAGES
$(images_from_compose "$compose")"
    while IFS= read -r df; do
        [ -n "$df" ] || continue
        IMAGES="$IMAGES
$(base_images_from_dockerfile "$df")"
    done < <(dockerfiles_from_compose "$compose")
done

# Deduplicate while dropping blanks.
IMAGES=$(printf '%s\n' "$IMAGES" | grep -v '^[[:space:]]*$' | sort -u)

if [ -z "$IMAGES" ]; then
    echo "[pull] No images derived from $* -- continuing; the build will pull them itself."
    exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
    echo "[pull] docker not found; skipping the pre-pull."
    exit 0
fi

echo "[pull] Pre-pulling $(printf '%s\n' "$IMAGES" | wc -l | tr -d ' ') image(s), up to $ATTEMPTS attempts each..."
FAILED=""
for img in $IMAGES; do
    n=1
    while [ "$n" -le "$ATTEMPTS" ]; do
        if docker pull -q "$img" >/dev/null 2>&1; then
            break
        fi
        if [ "$n" -eq "$ATTEMPTS" ]; then
            FAILED="$FAILED $img"
            break
        fi
        echo "[pull]   $img failed (attempt $n/$ATTEMPTS), retrying in ${SLEEP}s..."
        sleep "$SLEEP"
        n=$((n + 1))
    done
done

if [ -n "$FAILED" ]; then
    # Deliberately exit 0. The build is the authority: if these really cannot be fetched it will
    # say so, with a better message than this script can give.
    echo "[pull] Could not pre-pull:$FAILED"
    echo "[pull] Continuing anyway -- the build will report the real error if they are needed."
else
    echo "[pull] All images present."
fi
exit 0
