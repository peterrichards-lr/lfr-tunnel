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

echo "[Git Hook] Running SentinelOne EDR Safety Guard check..."
./scripts/check-edr-safety.sh
if [ $? -ne 0 ]; then
  echo "❌ Error: EDR safety check failed."
  exit 1
fi

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

echo "[Git Hook] Checking JavaScript syntax..."
if command -v node &>/dev/null; then
  for js_file in pkg/server/static/*.js; do
    if [ -f "$js_file" ]; then
      node -c "$js_file"
      if [ $? -ne 0 ]; then
        echo "❌ Error: JavaScript syntax check failed for $js_file."
        exit 1
      fi
    fi
  done
  echo "✅ Vanilla JavaScript syntax check passed."
else
  echo "⚠️ Warning: 'node' not found in PATH. Skipping Vanilla JavaScript syntax check."
fi

echo "[Git Hook] Checking React UI syntax and types..."
if command -v pnpm &>/dev/null; then
  (cd ui && pnpm install && pnpm run lint && pnpm run build)
  if [ $? -ne 0 ]; then
    echo "❌ Error: React UI lint or build failed. Please fix before committing."
    exit 1
  fi
  echo "✅ React UI checks passed."
else
  echo "⚠️ Warning: 'pnpm' not found in PATH. Skipping React UI checks."
fi

echo "[Git Hook] Running go vet..."
go vet ./...
if [ $? -ne 0 ]; then
  echo "❌ Error: 'go vet' failed. Please fix before committing."
  exit 1
fi

echo "[Git Hook] Running tests..."
# Delegated to `make test` rather than repeating its loop here (#1334).
#
# What this used to do: set TMPDIR=/private/tmp -- without exporting it, so the go child never
# saw it -- and then build with -o. With GOTMPDIR unset, the toolchain linked every test binary
# inside /var/folders and only then moved it to the -o path. That is the unsigned-binary-in-a-
# temp-directory shape the local EDR quarantines, and it ran on every single commit.
#
# make test exports GOTMPDIR and asserts it before building, so there is now one sanctioned
# path instead of two that could drift apart -- and only one place to get it right.
#
# pkg/server stays excluded here, as before: its tests open real listeners and are slow enough
# to make a commit hook painful. CI runs the full set.
PKGS=$(go list ./... | grep -v /pkg/server | tr '\n' ' ')
make test PKG="$PKGS"
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
