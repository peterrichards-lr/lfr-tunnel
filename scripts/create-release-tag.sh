#!/usr/bin/env bash
# scripts/create-release-tag.sh
# Automates the entire release lifecycle:
# 1. Bumps version in whats-new.json
# 2. Creates a release branch, commits, and tags the commit
# 3. Pushes the branch and tag to origin
# 4. Creates a Pull Request and enables Auto-Merge
# 5. Returns local workspace to master

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

# 1. Verify git and gh CLI are available
if ! command -v git &>/dev/null; then
    echo "❌ Error: git is not installed or not in PATH."
    exit 1
fi
if ! command -v gh &>/dev/null; then
    echo "❌ Error: gh (GitHub CLI) is not installed. Required for automated PR creation."
    exit 1
fi

# Check gh authentication status
if ! gh auth status &>/dev/null; then
    echo "❌ Error: GitHub CLI is not authenticated. Please run 'gh auth login' first."
    exit 1
fi

# 2. Check current branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$CURRENT_BRANCH" != "master" ]; then
    echo "❌ Error: You must start on the 'master' branch to release. Current branch: $CURRENT_BRANCH"
    exit 1
fi

# 3. Check for pending changes (excluding gemini.md, GEMINI.md)
echo "Checking git workspace status..."
PND_ERR=0
PND_FILES=()

# Run porcelain status and read line by line
while IFS= read -r line; do
    [ -z "$line" ] && continue
    file_path=$(echo "$line" | cut -c 4-)
    clean_path=$(echo "$file_path" | xargs)
    lower_path=$(echo "$clean_path" | tr '[:upper:]' '[:lower:]')

    if [ "$lower_path" != "gemini.md" ]; then # GEMINI.md is normalized to gemini.md under tr
        PND_ERR=1
        PND_FILES+=("$clean_path")
    fi
done < <(git status --porcelain)

if [ "$PND_ERR" -ne 0 ]; then
    echo "❌ Error: You have pending changes in files that are not part of the release process:"
    for file in "${PND_FILES[@]}"; do
        echo "   - $file"
    done
    echo "Please stash, commit, or discard these changes before running the release script."
    exit 1
fi

# 4. Pull latest master
echo "Pulling latest master changes from origin..."
git pull origin master

# 5. Extract and parse current version
WHATS_NEW="pkg/server/static/whats-new.json"
if [ ! -f "$WHATS_NEW" ]; then
    echo "❌ Error: Version details file $WHATS_NEW not found."
    exit 1
fi

CURRENT_VER=$(grep -o '"version": "[^"]*"' "$WHATS_NEW" | cut -d'"' -f4)
if [ -z "$CURRENT_VER" ]; then
    echo "❌ Error: Could not parse current version from $WHATS_NEW."
    exit 1
fi

echo "Current version in whats-new.json: $CURRENT_VER"

# Calculate default next patch version
if [[ "$CURRENT_VER" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    patch="${BASH_REMATCH[3]}"
    DEFAULT_NEXT="v$major.$minor.$((patch + 1))"
else
    DEFAULT_NEXT=""
fi

# Prompt for version tag
if [ -t 0 ]; then
    read -rp "Enter new version tag [default: $DEFAULT_NEXT]: " NEW_VER
else
    NEW_VER=""
fi

if [ -z "$NEW_VER" ]; then
    if [ -z "$DEFAULT_NEXT" ]; then
        echo "❌ Error: Could not calculate default next version, and no version was provided."
        exit 1
    fi
    NEW_VER="$DEFAULT_NEXT"
fi

# Validate version format
if [[ ! "$NEW_VER" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "❌ Error: Version tag must match semantic format 'vX.Y.Z' (e.g. v1.14.1)."
    exit 1
fi

# 6. Check if tag already exists locally or remotely
if git rev-parse "refs/tags/$NEW_VER" >/dev/null 2>&1; then
    echo "❌ Error: Git tag '$NEW_VER' already exists locally."
    exit 1
fi
git fetch origin --tags --quiet
if git ls-remote --tags origin "refs/tags/$NEW_VER" | grep -q "$NEW_VER"; then
    echo "❌ Error: Git tag '$NEW_VER' already exists on remote origin."
    exit 1
fi

echo "Preparing release $NEW_VER..."

# 7. Update whats-new.json using Perl
perl -pi -e 's/"version": "[^"]*"/"version": "'"$NEW_VER"'"/g' "$WHATS_NEW"

# 8. Create release branch
BRANCH_NAME="release/$NEW_VER"
echo "Creating and checking out branch $BRANCH_NAME..."
git checkout -b "$BRANCH_NAME"

# 9. Stage, Commit and Tag
git add "$WHATS_NEW"
if git status --porcelain | grep -q "gemini.md"; then
    git add gemini.md
fi

git commit -m "chore: bump version to $NEW_VER"
echo "Tagging commit with $NEW_VER..."
git tag "$NEW_VER"

# 10. Push branch and tag
echo "Pushing branch $BRANCH_NAME to origin..."
git push origin "$BRANCH_NAME"

echo "Pushing tag $NEW_VER to origin..."
git push origin "$NEW_VER"

# 11. Create Pull Request & Enable Auto-Merge
echo "Creating Pull Request on GitHub..."
PR_URL=$(gh pr create \
    --title "chore: release $NEW_VER" \
    --body "Automated release PR for version $NEW_VER. Once status checks pass, this will merge automatically." \
    --head "$BRANCH_NAME" \
    --base master)

echo "PR created: $PR_URL"

echo "Enabling Auto-Merge for PR..."
# Enable auto-merge, trying merge commit first, then falling back to squash merge
gh pr merge "$PR_URL" --auto --merge || gh pr merge "$PR_URL" --auto --squash

# 12. Cleanup & Return to master
echo "Cleaning up local workspace..."
git checkout master
git pull origin master

echo "=== Release tagging automated successfully! ==="
echo "Version bumped to $NEW_VER, branch and tag pushed, PR raised and set to auto-merge."
