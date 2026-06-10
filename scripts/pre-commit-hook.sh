#!/bin/bash

# Gitleaks Docker Pre-Commit Hook
# Scans staged files for API keys, passwords, and private tokens.

echo "[Git Hook] Scanning staged files for secrets/tokens..."

# Run Gitleaks in Docker
# -v "$(pwd)":/app mounts the repository root
# -w /app sets the working directory
# protect --source=/app --verbose --staged tells gitleaks to scan staged changes
docker run --rm -v "$(pwd)":/app -w /app zricethezav/gitleaks:latest protect --source=/app --verbose --staged

EXIT_CODE=$?

if [ $EXIT_CODE -ne 0 ]; then
  echo ""
  echo "❌ Error: Git commit blocked because a secret or private token was detected."
  echo "If this is a false positive, add the secret value to '.gitleaksignore' to allow it."
  echo ""
  exit $EXIT_CODE
fi

echo "✅ No secrets detected. Proceeding with commit."
exit 0
