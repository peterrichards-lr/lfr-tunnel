# GitHub Rulesets Configuration

This directory contains the branch and tag protection rulesets for the `lfr-tunnel` repository.

## Prerequisites
Due to GitHub platform limitations, repository rulesets are only supported on:
1. Public repositories (Free/Pro/Team/Enterprise).
2. Private repositories under a GitHub Pro, Team, or Enterprise subscription.

If the repository is private and on a Free plan, trying to create these rulesets will result in a `403 Forbidden` error.

## Applying Rulesets

Once the repository is public or upgraded, you can apply these rulesets using the GitHub CLI (`gh`):

### 1. Apply Master Branch Protection Ruleset
This ruleset targets `master`, blocks force-pushes and deletions, requires pull requests, and requires CI checks to pass before merging:

```bash
gh api -X POST /repos/{owner}/{repo}/rulesets --input docs/github/branch_ruleset.json
```

### 2. Apply Version Tag Protection Ruleset
This ruleset targets `v*` tags, blocking tag deletion and preventing force-updates to tags:

```bash
gh api -X POST /repos/{owner}/{repo}/rulesets --input docs/github/tag_ruleset.json
```
