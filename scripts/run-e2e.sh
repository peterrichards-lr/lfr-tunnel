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
else
    echo "=== Running Standard E2E Integration Test Suite ==="
    ./tests/e2e/run.sh
fi
