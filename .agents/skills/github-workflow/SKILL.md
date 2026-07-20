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
Before writing code for any feature or logic change:
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

### Tech Debt Tracking
If you encounter code smells, duplicated logic, or overly complex implementations during your work, you MUST record it by raising a GitHub issue via the `gh` CLI (`gh issue create`). 
- **Required Label**: You must attach the `tech debt` label to these issues.
- **Actionability**: You do not need to tackle the technical debt immediately unless it can be resolved without diverting significant effort from your primary task. The ultimate requirement is to ensure it is recorded in the backlog.

## 3. Resolving and Closing Tasks
- **Pull Request Flow (Preferred)**: When your tasks are tied to code changes, do **NOT** set `"completed": true` in the JSON. Leave it as `false`. Instead, include `Closes #<issue-number>` in your Pull Request body or commit message so GitHub automatically closes the issue when the PR merges.
- **Manual/Standalone Tasks**: ONLY for operational tasks that do NOT involve a PR (e.g. running scripts, config changes), you may set `"completed": true` and run the sync utility again:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *The utility will automatically detect the completed state, post a reference comment with the current git commit hash, and forcefully close the issue on GitHub.*

## 4. Pre-Commit / Pre-PR Checks
Before pushing commits and opening a PR, you MUST ensure that your changes will not fail the automated CI pipeline:
- **Go Formatting**: You MUST run `gofmt -w .` to format all modified Go files. Failure to format will result in a CI Lint & Format check failure.
- **UI Builds**: If you modify any React UI source files (under `ui/`), you MUST build the UI and commit the resulting `pkg/server/ui-dist` folder to keep it in sync. 
  - To safely build the UI without interactive prompts, ensure you are running Node v22 or higher, set `CI=true`, and run: 
    ```bash
    cd ui && CI=true pnpm install && pnpm run build && cd .. && rm -rf pkg/server/ui-dist && cp -r ui/dist pkg/server/ui-dist
    ```
  - The CI workflow includes a synchronization check that explicitly verifies `pkg/server/ui-dist` matches the `ui/` source code. If they do not match, the CI will intentionally fail.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-20* | *Last Reviewed: 2026-07-20*
