#!/bin/bash
# Shared Docker Compose invocation for the E2E suites.
#
# Sourced rather than duplicated because four scripts needed this and one of the four had
# drifted (#1355): run-edge.sh gated its fallback on whether `docker-compose` *exists*, while
# the other three tested whether `docker compose` *works*. Docker Desktop leaves a shim on
# PATH for every WSL distro whether integration is enabled or not, so the existence check
# passed and the command then failed at runtime -- `make e2e-edge` was unrunnable on WSL2
# while `make e2e` was fine, with an error naming Docker rather than the script.
#
# The rule this encodes, from .agent-state.md: prefer a check that proves the thing *works*
# over one that proves it is *configured*.
#
# Defines a `docker-compose` shell function that shadows the v1 binary, so call sites keep
# using `docker-compose ...` unchanged and pick up v2 automatically.

# shellcheck shell=bash
docker-compose() {
    # -p only when the caller set a project name. run.sh, run-sso.sh and run-e2e-ui.sh each
    # generate one to keep concurrent agents' containers apart; run-edge.sh does not use one
    # and passes -f docker-compose-edge.yml instead, so it must not receive an empty -p.
    local -a _project=()
    if [ -n "${E2E_PROJECT_NAME:-}" ]; then
        _project=(-p "$E2E_PROJECT_NAME")
    fi

    if docker compose version >/dev/null 2>&1; then
        docker compose "${_project[@]}" "$@"
    else
        # `command` so this does not recurse into the function shadowing the same name.
        command docker-compose "${_project[@]}" "$@"
    fi
}
