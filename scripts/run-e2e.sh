#!/bin/bash
set -e

# Change directory to repo root
cd "$(dirname "$0")/.."

# Signal file configuration
SIGNAL_FILE=".progress-signal"

# Make sure we clean up properly on exit/interrupt/error
cleanup() {
    EXIT_CODE=$?
    if [ $EXIT_CODE -eq 0 ]; then
        echo "SUCCESS" > "$SIGNAL_FILE"
    else
        echo "FAILED" > "$SIGNAL_FILE"
    fi
    exit $EXIT_CODE
}
trap cleanup EXIT INT TERM ERR

TEST_TYPE=${1:-standard}

if [ "$TEST_TYPE" = "sso" ]; then
    echo "=== Running Keycloak SSO E2E Integration Test Suite ==="
    ./tests/e2e/run-sso.sh
elif [ "$TEST_TYPE" = "edge" ]; then
    # run-edge.sh existed but nothing invoked it -- not this dispatcher, not the Makefile,
    # not CI. It had been failing on a node-id mismatch that stopped the edge's control
    # channel authenticating at all, and nobody found out because nobody ran it (#1254).
    echo "=== Running Multi-Region Edge E2E Integration Test Suite ==="
    ./tests/e2e/run-edge.sh
else
    echo "=== Running Standard E2E Integration Test Suite ==="
    ./tests/e2e/run.sh
fi
