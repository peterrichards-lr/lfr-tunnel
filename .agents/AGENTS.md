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
As you complete individual sub-issues:
1. Update the target JSON file, changing the sub-issue's `"completed"` property to `true`.
2. Execute the sync utility again:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *The utility will automatically detect the completed state, post a reference comment with the current git commit hash, and close the issue on GitHub.*

## 4. Edge Node Propagation & State Synchronization
- **Stateless Edge Nodes**: Regional Edge nodes (`lfr-tunneld` running with no DB) rely entirely on the Control Plane for authentication and validation. However, they maintain their own active memory `registry` of live tunnels.
- **State Changes**: ANY feature or API endpoint that modifies the active tunnel lease state in memory on the Control Plane (e.g., custom headers, rate limits, kicks) **MUST** include logic to propagate that state change to the specific Edge Node hosting the tunnel via the `edge_control_ws.go` WebSocket channel. Failure to do so will result in split-brain behavior where Edge nodes do not enforce the new policies.
