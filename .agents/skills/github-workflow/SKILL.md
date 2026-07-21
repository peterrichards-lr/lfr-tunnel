---
name: github-workflow
description: Strict rules for synchronizing tasks with GitHub Issues and managing Pull Requests. Activate this skill whenever planning features, opening PRs, or closing tasks.
---

# GitHub Issue Sync & PR Workflow Rules

When planning or implementing new features, you must use the automated JSON-driven issue sync tool located at `scripts/gh-issue-sync.cjs` to synchronize your task checklist with the GitHub issue tracker.

## 1. Tool Setup & Location
- Script: `scripts/gh-issue-sync.cjs` (executable Node.js script)
- Sample Config: `scripts/issues.sample.json`

## 2. Issue Tracking Workflow
*Active Constraint*: Before writing ANY code for any feature or logic change, you MUST explicitly execute the following steps to track the task:
1. **Plan & Draft**: Create a temporary JSON file (e.g., `scripts/feature-xyz-plan.json`) containing the Epic description and target sub-issues. Follow the schema defined in `scripts/issues.sample.json`.
2. **Dry Run**: Preview the CLI commands that will run:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json --dry-run
   ```
3. **Apply & Link**: Generate the Epic and sub-issues on GitHub:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *Note: The script automatically links all sub-issues to the parent Epic.*

### Tech Debt Tracking (Immediate Logging)
*Active Constraint*: The moment you identify any of the 10 catalogued tech debt categories during your work, you MUST pause your current workflow and immediately execute `gh issue create --title "Tech Debt: [Topic]" --body "[Details]" --label "tech debt"` before taking any other action.
The 10 categories are:
1. Code smells
2. Duplication
3. Over-complexity
4. Fragile coupling
5. Missing safety guards
6. Missing tests
7. Security hygiene
8. Deprecated patterns
9. Config drift
10. Documentation debt

## 3. Resolving and Closing Tasks
- **Pull Request Flow (Preferred)**: When your tasks are tied to code changes, do **NOT** set `"completed": true` in the JSON. Leave it as `false`. Instead, include `Closes #<issue-number>` in your Pull Request body or commit message so GitHub automatically closes the issue when the PR merges.
- **Manual/Standalone Tasks**: ONLY for operational tasks that do NOT involve a PR (e.g. running scripts, config changes), you may set `"completed": true` and run the sync utility again:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *The utility will automatically detect the completed state, post a reference comment with the current git commit hash, and forcefully close the issue on GitHub.*

## 4. Pull Request Requirements
*Active Constraint*: Before creating a Pull Request (`gh pr create`), you MUST ensure the following criteria are met:
1. **Existing Issue Verification**: A GitHub issue MUST exist for the work being PR'd.
2. **Issue Linking**: The PR description or commit message MUST contain `Closes #<issue-number>` or `Resolves #<issue-number>` for all associated issues. A single PR may close multiple issues (e.g., closing the final sub-issue and the parent Epic simultaneously).
3. **Issue Content Constraints**: The GitHub issue(s) being resolved MUST contain:
    - A clear description of the problem or feature.
    - An analysis section detailing how to resolve or implement the fix/change.
    - A documented implementation plan.
If the issue lacks these elements, you MUST update the issue (`gh issue edit`) with this information BEFORE opening the PR.

## 5. Pre-Commit / Pre-PR Checks
*Active Constraint*: Before pushing commits and opening a PR, you MUST actively execute the following verification steps:
1. **Branch Sync**: You MUST execute `git fetch origin && git merge origin/master` to ensure your feature branch is strictly up-to-date with `master`. If there are any merge conflicts, you MUST resolve them and re-build any affected components (e.g. `ui-dist`) before proceeding.
2. **Go Formatting**: Execute `gofmt -w .` to format all modified Go files.
3. **UI Builds**: If you modify any React UI source files (under `ui/`), you MUST execute the UI build to sync `pkg/server/ui-dist`. Execute exactly:
   ```bash
   cd ui && CI=true pnpm install && pnpm run build && cd .. && rm -rf pkg/server/ui-dist && cp -r ui/dist pkg/server/ui-dist
   ```

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-21* | *Last Reviewed: 2026-07-21*
