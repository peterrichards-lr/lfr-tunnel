# GitHub Issue Sync Workflow Rules

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

## 3. Resolving and Closing Tasks
- **Pull Request Flow (Preferred)**: When your tasks are tied to code changes, do **NOT** set `"completed": true` in the JSON. Leave it as `false`. Instead, include `Closes #<issue-number>` in your Pull Request body or commit message so GitHub automatically closes the issue when the PR merges.
- **Manual/Standalone Tasks**: ONLY for operational tasks that do NOT involve a PR (e.g. running scripts, config changes), you may set `"completed": true` and run the sync utility again:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *The utility will automatically detect the completed state, post a reference comment with the current git commit hash, and forcefully close the issue on GitHub.*

## 4. Edge Node Propagation & State Synchronization
- **Stateless Edge Nodes**: Regional Edge nodes (`lfr-tunneld` running with no DB) rely entirely on the Control Plane for authentication and validation. However, they maintain their own active memory `registry` of live tunnels.
- **State Changes**: ANY feature or API endpoint that modifies the active tunnel lease state in memory on the Control Plane (e.g., custom headers, rate limits, kicks) **MUST** include logic to propagate that state change to the specific Edge Node hosting the tunnel via the `edge_control_ws.go` WebSocket channel. Failure to do so will result in split-brain behavior where Edge nodes do not enforce the new policies.


## Global Documentation Timestamps Rule

**Objective**: Ensure that all markdown documents across all projects maintain a consistent "Last Updated" and "Last Reviewed" timestamp footer to track documentation decay and relevance.

**Rules**:
1. Every time you create or modify a Markdown (`.md`) file, you MUST ensure it has a footer block at the very end in the exact format:
   `<!-- markdownlint-disable MD049 -->`
   `---`
   `*Last Updated: YYYY-MM-DD* | *Last Reviewed: YYYY-MM-DD*`
2. If working in a new repository without these footers, implement a Python script named `scripts/append_timestamps.py` using `Path.rglob` to recursively scan all `.md` files (ignoring `.venv`, `node_modules`, `.smoke_venv` etc.) and append this block if it does not exist.
3. You must also establish a `scripts/check_docs_review.py` script that parses this footer using the regex `r"\*Last Updated: ([\d\-]+)\* \| \*Last Reviewed: ([\d\-]+)\*"` and accepts arguments for `--max-review-days`, `--max-update-days`, and `--max-gap-days`. 
4. The script should alert the user via `sys.exit(1)` if any documents have exceeded these threshold values.

If you ever ask the AI to "review the project documentation for outdated files", it will automatically know to look for or construct these scripts.



<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-16* | *Last Reviewed: 2026-07-16*
