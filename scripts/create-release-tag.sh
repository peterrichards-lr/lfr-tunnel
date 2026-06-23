#!/usr/bin/env bash
# scripts/create-release-tag.sh
# Verifies git workspace, validates release version, and pushes git tag to trigger CI/CD pipeline.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

# 1. Verify git is available
if ! command -v git &>/dev/null; then
    echo "❌ Error: git is not installed or not in PATH."
    exit 1
fi

# 2. Check current branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$CURRENT_BRANCH" != "master" ]; then
    echo "❌ Error: You must be on the 'master' branch to release. Current branch: $CURRENT_BRANCH"
    exit 1
fi

# 3. Check for pending changes (excluding whats-new.json, gemini.md, GEMINI.md)
echo "Checking git workspace status..."
PND_ERR=0
PND_FILES=()

# Run porcelain status and read line by line
while IFS= read -r line; do
    [ -z "$line" ] && continue
    # Extract file path (strip status prefix, e.g. " M path/to/file" or "?? path/to/file")
    file_path=$(echo "$line" | cut -c 4-)
    
    # Normalize paths for comparison
    clean_path=$(echo "$file_path" | xargs)
    lower_path=$(echo "$clean_path" | tr '[:upper:]' '[:lower:]')

    if [ "$lower_path" != "pkg/server/static/whats-new.json" ] && \
       [ "$lower_path" != "gemini.md" ] && \
       [ "$lower_path" != "gemini.md" ]; then # GEMINI.md matches gemini.md under tr
        PND_ERR=1
        PND_FILES+=("$clean_path")
    fi
done < <(git status --porcelain)

if [ "$PND_ERR" -ne 0 ]; then
    echo "❌ Error: You have pending changes that are not part of the release process:"
    for file in "${PND_FILES[@]}"; do
        echo "   - $file"
    done
    echo "Please stash, commit, or discard these changes before running the release tag script."
    exit 1
fi

# 4. Pull latest master
echo "Pulling latest master changes from origin..."
git pull origin master

# 5. Extract version from whats-new.json
WHATS_NEW="pkg/server/static/whats-new.json"
if [ ! -f "$WHATS_NEW" ]; then
    echo "❌ Error: Version details file $WHATS_NEW not found."
    exit 1
fi

VERSION=$(grep -o '"version": "[^"]*"' "$WHATS_NEW" | cut -d'"' -f4)
if [ -z "$VERSION" ]; then
    echo "❌ Error: Could not parse version from $WHATS_NEW."
    exit 1
fi

echo "Parsed release version: $VERSION"

# 6. Check if tag already exists (locally or remotely)
if git rev-parse "refs/tags/$VERSION" >/dev/null 2>&1; then
    echo "❌ Error: Git tag '$VERSION' already exists locally."
    echo "Please bump the version in $WHATS_NEW and commit it before creating a release."
    exit 1
fi

# Check remote tags
git fetch origin --tags --quiet
if git ls-remote --tags origin "refs/tags/$VERSION" | grep -q "$VERSION"; then
    echo "❌ Error: Git tag '$VERSION' already exists on remote 'origin'."
    echo "Please bump the version in $WHATS_NEW and commit it before creating a release."
    exit 1
fi

# 7. Ask for confirmation
CONFIRM=""
if [ -t 0 ]; then
    read -rp "Create git tag '$VERSION' and push it to origin to trigger release? (y/N): " CONFIRM
else
    # Non-interactive default or environment override
    CONFIRM="y"
fi

if [[ "$CONFIRM" =~ ^[Yy]$ ]]; then
    echo "Creating tag $VERSION..."
    git tag "$VERSION"
    echo "Pushing tag $VERSION to origin..."
    git push origin "$VERSION"
    echo "✅ Success: Pushed tag $VERSION to origin."
    echo "GitHub Actions CI/CD release workflow has been triggered."
else
    echo "Release tag operation aborted by user."
    exit 0
fi
