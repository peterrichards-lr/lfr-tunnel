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
gh api -X POST /repos/{owner}/{repo}/rulesets --input resources/github/branch_ruleset.json
```

### 2. Apply Version Tag Protection Ruleset
This ruleset targets `v*` tags, blocking tag deletion and preventing force-updates to tags:

```bash
gh api -X POST /repos/{owner}/{repo}/rulesets --input resources/github/tag_ruleset.json
```

### 3. Apply Checksums Branch Protection Ruleset
This ruleset targets the `checksums` branch, blocking branch deletion while still allowing force-pushes (necessary for the GitHub Actions release workflow):

```bash
gh api -X POST /repos/{owner}/{repo}/rulesets --input resources/github/checksums_ruleset.json
```

## Updating a ruleset that already exists

`POST` creates a *new* ruleset. To change one that is already applied, `PUT` it by id --
otherwise you end up with two rulesets whose requirements are unioned, which is confusing to
debug and easy to mistake for one misbehaving ruleset:

```bash
# List them to find the id
gh api /repos/{owner}/{repo}/rulesets --jq '.[] | "\(.id) \(.name)"'

# Replace the whole ruleset with the committed file
gh api -X PUT /repos/{owner}/{repo}/rulesets/<id> --input resources/github/branch_ruleset.json
```

`PUT` replaces the entire rule set, so the file has to be complete. If you edit these files by
hand, re-read the live version first (`gh api .../rulesets/<id>`) and diff it against the
committed copy -- the committed `branch_ruleset.json` had silently drifted from live by two
required contexts, the `required_linear_history` rule and the unattributed-changes approval
guard before #1365 caught it.

## Required checks come from TWO places, not one

This is the trap. `master` is protected by **both** a ruleset *and* classic branch protection,
and each carries its own independent list of required status checks. A check removed from one is
still enforced by the other, and the union is what actually gates a merge:

```bash
# Source 1: the ruleset
gh api /repos/{owner}/{repo}/rulesets/<id> \
  --jq '.rules[] | select(.type=="required_status_checks")
        | .parameters.required_status_checks[].context'

# Source 2: classic branch protection (note enforce_admins is on)
gh api /repos/{owner}/{repo}/branches/master/protection \
  --jq '.required_status_checks.contexts[]'
```

Check both before concluding anything about why a PR will or will not merge. A PR's
`statusCheckRollup` summarises only the checks that actually *reported*, so a context that was
never created is invisible there while still blocking the merge.

### Why that matters for the CI matrix

A job whose `if:` is false still reports its context as *skipped*, and a skipped check
**satisfies** a required context. A matrix leg that is never generated reports nothing at all
and blocks the merge forever. That is why `ci.yml` always generates all three `Test Suite` legs
and makes the *work* conditional inside them, rather than making the matrix itself conditional
(#1365).


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-26* | *Last Reviewed: 2026-08-26*
