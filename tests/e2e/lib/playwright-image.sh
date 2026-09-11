#!/bin/bash
# Shared derivation of the Playwright runner image for the E2E suites (#1863).
#
# Sourced rather than duplicated for the same reason as compose.sh (#1355): two scripts needed
# this and the two had drifted. scripts/run-e2e-ui.sh derived the tag from pnpm-lock.yaml, while
# tests/e2e/run-ui.sh hardcoded v1.60.0-jammy and installed with npm against a package-lock.json
# pinning 1.60.0. Each was internally consistent, which is why neither failed -- they simply ran
# different versions of the thing the whole suite is.
#
# The image ships browser builds for exactly ONE library version, so the tag and the installed
# @playwright/test have to agree. Pinning a literal taken from package.json's range (^1.60.0) was
# tried and Playwright refused to launch, naming both tags itself:
#
#   Executable doesn't exist at /ms-playwright/chromium_headless_shell-1228/...
#   current: mcr.microsoft.com/playwright:v1.60.0-jammy / required: v1.61.1-jammy
#
# A caret range cannot decide this; only the lockfile can. Derived rather than listed, for the
# same reason test-shell-portability.sh derives its file set: a listed value is a second source of
# truth that drifts the moment the first one moves.
#
# Sets PLAYWRIGHT_VERSION and PLAYWRIGHT_IMAGE. Callers must have E2E_UI_DIR set, or pass the
# directory as $1.

lft_playwright_image() {
    local ui_dir="${1:-${E2E_UI_DIR:-}}"

    if [ -z "$ui_dir" ]; then
        echo "playwright-image.sh: no UI directory given (pass \$1 or set E2E_UI_DIR)." >&2
        return 1
    fi

    local lockfile="$ui_dir/pnpm-lock.yaml"
    if [ ! -f "$lockfile" ]; then
        echo "playwright-image.sh: $lockfile not found." >&2
        echo "  Refusing to guess an image tag: a mismatch means Playwright cannot launch." >&2
        return 1
    fi

    PLAYWRIGHT_VERSION="$(sed -n "s/^  '@playwright\/test@\([0-9][0-9.]*\)':.*/\1/p" "$lockfile" | head -1)"
    if [ -z "$PLAYWRIGHT_VERSION" ]; then
        echo "playwright-image.sh: could not read @playwright/test from $lockfile." >&2
        echo "  pnpm's lockfile format may have changed. Refusing to guess an image tag." >&2
        return 1
    fi

    PLAYWRIGHT_IMAGE="lfr-tunnel-e2e-playwright:v${PLAYWRIGHT_VERSION}"
    export PLAYWRIGHT_VERSION PLAYWRIGHT_IMAGE
}

# Builds the runner image. The docker CLI is installed because analytics.spec.ts drives the client
# with `docker exec ${E2E_PROJECT_NAME}-lfr-tunnel-1`; the daemon is the host's, reached through
# the socket the caller mounts. Built rather than `docker run mcr.microsoft.com/playwright` with an
# inline apt-get: installing it on every run re-fetches from the apt mirrors each time, the same
# transient flakiness #1530 added a retry for one layer up. Build context is stdin, so nothing is
# uploaded, and the tag makes it a no-op on every run after the first.
lft_playwright_build() {
    docker build -t "$PLAYWRIGHT_IMAGE" --build-arg "PW_VERSION=$PLAYWRIGHT_VERSION" - <<'DOCKERFILE'
ARG PW_VERSION
FROM mcr.microsoft.com/playwright:v${PW_VERSION}-jammy

RUN apt-get update \
    && apt-get install -y --no-install-recommends docker.io \
    && rm -rf /var/lib/apt/lists/*

# pnpm, not npm: tests/e2e/ui is a pnpm project (#1863).
RUN corepack enable
DOCKERFILE
}
