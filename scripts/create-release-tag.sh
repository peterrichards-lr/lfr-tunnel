#!/bin/bash
set -e

if [ -z "$1" ]; then
  echo "Usage: $0 <NEW_VERSION_TAG>"
  echo "Example: $0 v1.44.0"
  exit 1
fi

NEW_VERSION=$1
DATE=$(date +%Y-%m-%d)

echo "Starting release process for $NEW_VERSION..."

# Ensure we are on master
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" != "master" ]; then
  echo "Error: You must be on the master branch."
  exit 1
fi

echo "Checking for an already-open release PR..."
EXISTING_RELEASE_PR=$(gh pr list --state open --json number,headRefName,url \
  -q '.[] | select(.headRefName | startswith("release/")) | .url' | head -n1)
if [ -n "$EXISTING_RELEASE_PR" ]; then
  echo "Error: a release PR is already open and hasn't merged yet: $EXISTING_RELEASE_PR"
  echo "Wait for it to merge (or close it) before starting another release -- see #931:"
  echo "batching small bumps behind one release cycle avoids a burst of near-identical"
  echo "version-bump PRs landing on master within minutes of each other."
  exit 1
fi

echo "Pulling latest master..."
git pull origin master

echo "Creating release branch..."
git checkout -b release/$NEW_VERSION

echo "Updating pkg/config/version.go..."
sed -i.bak -e "s/var Version = \".*\"/var Version = \"$NEW_VERSION\"/" pkg/config/version.go
rm -f pkg/config/version.go.bak

echo "Updating pkg/server/static/whats-new.json..."
python3 -c "
import json
import sys

path = 'pkg/server/static/whats-new.json'
try:
    with open(path, 'r') as f:
        data = json.load(f)
except Exception as e:
    print('Failed to read', path, e)
    sys.exit(1)

if data and data[0]['version'] == '$NEW_VERSION':
    print('Version $NEW_VERSION already exists in whats-new.json')
else:
    existing = [e for e in data if e.get('version') == '$NEW_VERSION']
    if existing:
        print('Version $NEW_VERSION already present in whats-new.json, retaining release notes.')
    else:
        import subprocess
        features = []
        try:
            last_tag = subprocess.check_output(['git', 'describe', '--tags', '--abbrev=0'], text=True).strip()
            log_out = subprocess.check_output(['git', 'log', f'{last_tag}..HEAD', '--oneline', '--no-merges'], text=True).strip()
            for line in log_out.splitlines():
                if line:
                    parts = line.split(' ', 1)
                    if len(parts) > 1 and not parts[1].startswith('chore: bump version'):
                        features.append(parts[1])
        except Exception:
            pass
        if not features:
            features = ['Release $NEW_VERSION']
        data.insert(0, {
            'version': '$NEW_VERSION',
            'release_date': '$DATE',
            'features': features
        })
        data = data[:5]
        with open(path, 'w') as f:
            json.dump(data, f, indent=2)
            f.write('\n')
        print('Added $NEW_VERSION to whats-new.json')
"

echo "Committing changes..."
git add pkg/config/version.go pkg/server/static/whats-new.json
git commit -m "chore: bump version to $NEW_VERSION"

echo "Pushing branch..."
git push -u origin release/$NEW_VERSION

echo "Creating auto-merging PR..."
gh pr create --title "chore: bump version to $NEW_VERSION" --body "Automated release bump to $NEW_VERSION." --label no-issue-needed
gh pr merge --auto -s

echo "------------------------------------------------------"
echo "Success! The release PR has been created and set to auto-merge."
echo "Once the CI checks pass and it merges into master, you must cut the tag:"
echo ""
echo "  git checkout master"
echo "  git pull origin master"
echo "  git tag $NEW_VERSION"
echo "  git push origin $NEW_VERSION"
echo "------------------------------------------------------"
