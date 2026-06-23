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

echo "✅ No secrets detected."

echo "[Git Hook] Checking for unformatted files..."
UNFORMATTED=$(gofmt -l .)
if [ -n "$UNFORMATTED" ]; then
  echo "❌ Error: The following files are not formatted properly:"
  echo "$UNFORMATTED"
  echo "Formatting them now..."
  make fmt
  echo "❌ Error: Git commit blocked because files were modified by formatting."
  echo "Please restage these files ('git add .') and try committing again."
  exit 1
fi

echo "[Git Hook] Running go vet..."
go vet ./...
if [ $? -ne 0 ]; then
  echo "❌ Error: 'go vet' failed. Please fix before committing."
  exit 1
fi

echo "[Git Hook] Running tests..."
TMPDIR=/private/tmp go test $(go list ./... | grep -v /pkg/server)
if [ $? -ne 0 ]; then
  echo "❌ Error: Tests failed. Please fix before committing."
  exit 1
fi

echo "[Git Hook] Running golangci-lint via Docker..."
docker run --rm -v "$(pwd)":/app -w /app golangci/golangci-lint:latest golangci-lint run
if [ $? -ne 0 ]; then
  echo "❌ Error: golangci-lint found issues. Please fix before committing."
  exit 1
fi
echo "✅ Linting passed."

echo "✅ All pre-commit checks passed! Proceeding with commit."
exit 0
