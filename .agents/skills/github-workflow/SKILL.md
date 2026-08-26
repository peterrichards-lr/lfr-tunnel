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

### Tech Debt Tracking
*Active Constraint*: Tech debt you notice but don't fix as part of the current task must still be tracked — untracked debt is debt that never gets paid down. File it as a GitHub issue; do not substitute a PR-description mention or a code comment, since those aren't discoverable or prioritizable later. The 10 catalogued categories are:
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

Apply this without derailing the task you're actually doing:
- **Don't halt mid-task.** Keep working; log tech debt at a natural checkpoint (before opening your PR is fine) rather than interrupting the current edit the moment you spot something.
- **Dedup first.** Before filing, check for an existing open issue covering the same thing: `gh issue list --label "tech debt" --search "<keyword>"`. If one exists, leave a comment or `+1` on it instead of creating a duplicate.
- **Batch related findings into one issue.** If a single pass surfaces several instances of the same category (e.g. three duplicated helper functions), file one issue describing the pattern with all instances listed, not one issue per instance. One issue per genuinely distinct problem, not per line.
- **Bar for filing.** File it if it's a real, specific problem you can point at (a named function, a missing test for a named case) — not a vague "this area could be cleaner." If you can't say what a future agent should do with it, it's not ready to file yet.
- Command: `gh issue create --title "Tech Debt: [Topic]" --body "[Details]" --label "tech debt"`.

## 3. Claiming an Issue

*Active Constraint*: Multiple agents work this backlog concurrently — Claude sessions in parallel,
and Gemini on a separate machine. Before starting work on any issue you MUST claim it, and you MUST
NOT start work on an issue another session holds.

### Identity: the label says *which agent*, the session ID says *which instance*

Two identifiers together:

- the `agent: <name>` label — `agent: claude`, `agent: gemini`
- your **session ID**, recorded in the claim comment

**Never infer ownership from the `agent:` label alone.** Two concurrent Claude sessions both carry
`agent: claude`, so a listing of claimed issues looks identical whether you hold them or another
session does. Resolve the session ID before touching anything:

```bash
gh issue view <N> --json comments \
  --jq '[.comments[] | select(.body | test("Claimed")) | .body] | last' \
  | grep -oE 'session_[A-Za-z0-9]+|transcript:[a-f0-9-]+'
```

If that returns a session ID that is not yours, the issue is taken. Leave it alone — including its
body, its labels, and any files its claim declares as territory.

Use whichever session identifier your runtime exposes. Prefer `session_01…`; where only a
transcript UUID is available, prefix it as `transcript:<uuid>` so the two forms are never confused.
**Never invent an ID in a namespace your runtime does not provide** — a fabricated identifier
cannot be traced back to a real session, which defeats the point.

### The label is a signal; the branch is the lock

GitHub label writes are last-write-wins with no compare-and-swap. Two agents polling "is there an
`agent:` label?" at the same moment both see *no* and both start. `git push` of a new branch
**fails if the ref already exists**, which is the only atomic test-and-set available across
machines — and every issue needs a branch anyway.

### Step 1 — find unclaimed work

```bash
gh issue list --state open --limit 100 --json number,title,labels \
  --jq '.[] | select([.labels[].name] | any(startswith("agent:")) | not) | "#\(.number) \(.title)"'
```

### Step 2 — check territory, not just the issue

Issue-level locking is not sufficient. Several open issues can target the same file, so two agents
holding disjoint issues still collide. Read the parent epic's file-overlap table if there is one,
and read the territory line of any active claim. Do not take an issue whose primary files are held.

### Step 3 — take the lock, before any work

Branch names are keyed on the issue number so `git ls-remote` acts as the lock table. Check by
number first (slugs vary between agents), then push:

```bash
git ls-remote --heads origin | grep -E "refs/heads/[^/]+/1323-" && echo "TAKEN -- pick another"

git switch -c fix/1323-escape-proxy-pages master
git push -u origin fix/1323-escape-proxy-pages   # atomic: fails if the ref exists
```

If the push is rejected, the issue is taken. Pick another. **Never force.**

### Step 4 — signal, only after the push succeeds

```bash
gh issue edit 1323 --add-label "agent: claude"
gh issue comment 1323 --body "🔒 **Claimed**

| | |
|---|---|
| **Agent** | \`claude\` |
| **Session** | \`session_01EXAMPLE\` |
| **Claimed** | 2026-08-25T16:22Z |
| **Branch** | \`fix/1323-escape-proxy-pages\` |
| **Territory** | \`pkg/server/proxy.go\`, \`pkg/server/*.html\` |"
```

Territory is the field other agents act on — list the files your PR will actually modify.

### Step 5 — release

- **Normal**: PR with `Closes #<N>`. Merging closes the issue and retires the claim.
- **Abandoned**: delete the remote branch, remove the `agent:` label, and comment why — so the next
  agent knows it was abandoned rather than finished.

### Races and stale claims

**Tie-break.** If two agents both end up claiming (slug divergence beat the number check), the
**lexicographically lower session ID yields**: delete the branch, remove the label, comment. String
comparison is total and deterministic across both ID forms, so this needs no negotiation.

**Staleness.** An `agent:` label whose branch has had no commits for **24h** is reclaimable by any
agent, after commenting on the issue. Without this, one crashed session removes an issue from the
pool permanently.

```bash
git log -1 --format='%cr' origin/fix/1323-escape-proxy-pages
```

## 4. Resolving and Closing Tasks
- **Pull Request Flow (Preferred)**: When your tasks are tied to code changes, do **NOT** set `"completed": true` in the JSON. Leave it as `false`. Instead, include `Closes #<issue-number>` in your Pull Request body or commit message so GitHub automatically closes the issue when the PR merges.
- **Manual/Standalone Tasks**: ONLY for operational tasks that do NOT involve a PR (e.g. running scripts, config changes), you may set `"completed": true` and run the sync utility again:
   ```bash
   node scripts/gh-issue-sync.cjs scripts/feature-xyz-plan.json
   ```
   *The utility will automatically detect the completed state, post a reference comment with the current git commit hash, and forcefully close the issue on GitHub.*

## 5. Pull Request Requirements
*Active Constraint*: Before creating a Pull Request (`gh pr create`), you MUST ensure the following criteria are met:
1. **Existing Issue Verification**: A GitHub issue MUST exist for the work being PR'd.
2. **Issue Linking**: The PR description or commit message MUST contain `Closes #<issue-number>` or `Resolves #<issue-number>` for all associated issues. A single PR may close multiple issues (e.g., closing the final sub-issue and the parent Epic simultaneously).
3. **Issue Content Constraints**: The GitHub issue(s) being resolved MUST contain:
    - A clear description of the problem or feature.
    - An analysis section detailing how to resolve or implement the fix/change.
    - A documented implementation plan.
If the issue lacks these elements, you MUST update the issue (`gh issue edit`) with this information BEFORE opening the PR.

**This is CI-enforced, not just documented here.** The "Issue Link Check" workflow
(`.github/workflows/issue-link-check.yml`) fails any PR that doesn't reference a
`Closes|Fixes|Resolves #N` issue, unless the PR carries the `no-issue-needed`
label (an intentional escape hatch for genuinely trivial changes — don't reach
for it just to skip filing an issue for real work). This exists because prose
rules alone have repeatedly not been followed in this repo, even by the agent
that wrote the prose rule in the same session — see the retrospective cleanup
in issues #894-#897. File the issue *before* running `gh pr create`; a CI
failure after the fact just means going back to create one anyway.

## 6. Pre-Commit / Pre-PR Checks
*Active Constraint*: Before pushing commits and opening a PR, you MUST actively execute the following verification steps:
1. **Branch Sync**: You MUST execute `git fetch origin && git merge origin/master` to ensure your feature branch is strictly up-to-date with `master`.
2. **Go Formatting**: Execute `gofmt -w .` to format all modified Go files.
3. **UI Builds**: `pkg/server/ui-dist` is generated and **must never be committed** (#1196) — it is gitignored apart from a `.gitkeep` that keeps `//go:embed` compiling. CI, the `Dockerfile` and the release workflow each build it from source, so a UI change needs nothing but the source change.

   Build it locally only when you need to *run* the server (the portal 503s with "UI not built" otherwise):
   ```bash
   make build
   ```
   If you ever see `pkg/server/ui-dist` in `git status`, something has force-added it — do not commit it. Its filenames are content-hashed, so committed bundles made every pair of concurrent UI branches conflict unresolvably.

## 7. CI Failure Remediation
*Active Constraint*: If a Pull Request fails its CI checks (e.g., a GitHub Action fails), stay on it until it's green:
1. **Fix on the same branch.** Diagnose the root cause and push a fix commit to the SAME branch/PR. Do not open a fresh PR for the same change, do not abandon the branch, and do not ask the user to route around the failure.
2. **Re-check, don't assume.** After pushing, re-poll status (`gh pr checks <number>`, or `gh pr checks <number> --watch` to block until it resolves) rather than declaring success from the fix alone. Repeat step 1 if it's still red.
3. **Never bypass instead of fixing.** Never merge with a failing or pending required check, and never use `gh pr merge --admin` (or equivalent) to get around one — this repo's branch protection has no bypass actors configured specifically so this isn't an option for anyone, agent or human.
4. **Flaky vs. real.** If a failure looks unrelated to your change (e.g. a known-flaky E2E step), don't just assume that and move on — re-run the specific job (`gh run rerun <run-id>`) and confirm it passes on rerun before treating it as flaky.
5. **Genuinely blocked.** If you cannot make a required check pass after reasonable diagnosis (e.g. it depends on credentials or infrastructure you don't have access to), stop and tell the user what's blocking it. Don't silently give up, and don't work around the gate.

Once it's green, clean up the failed job runs from the PR's history using the GitHub CLI (e.g. `gh run delete <run-id>`), so the repository keeps a clean history of only successful runs and failed attempts don't trigger false-positive corrective actions later.

### Adding a job to `ci.yml`? Wire it into `CI Gate`

*Active Constraint*: `master` requires a single aggregate context, **CI Gate**, rather than
naming each job. `ci-gate` in `.github/workflows/ci.yml` `needs:` every other job in that
file and fails if any of them ended in anything other than `success` or `skipped`.

**If you add a job to `ci.yml`, you MUST add it to `ci-gate`'s `needs:` list.**
`scripts/check-required-contexts.sh` runs inside "Lint & Format Check" and will fail your
build if you don't — the error names the job. This is not bureaucracy: the gate treats
`skipped` as acceptable so that path filters still work, and that is only safe while every
job is a dependency. A job skipped because an upstream job *failed* also reports `skipped`,
and the only thing stopping that from hiding the failure is that the upstream job is in the
same list and reports `failure` itself. A job missing from `needs:` is worse than ungated —
it is ungated while the gate reports green.

Two related rules the same script enforces, both learned from permanent merge blocks:

- **Never put a job-level `if:` on a job backing a required context if it has a `matrix:`.**
  A skipped matrix job reports one check run under the bare job name, so the required
  per-entry contexts (`Test Suite (ubuntu-latest)` and friends) never appear at all, and the
  PR can never merge (#1380).
- **Never add a workflow-level `paths:` filter to `pull_request` in a workflow that emits a
  required context.** The workflow simply does not run, no check run is created, and the
  context stays pending forever (#1386). A `paths:` filter under `push:` is fine.

A job-level `if:` on a normal, non-matrix job **is** fine: GitHub accepts a `skipped`
conclusion for a required status check. Verified — PR #1379 merged while two required
contexts were skipped. Don't remove existing path filters believing otherwise; that would
throw away the CI speed-up from #1363 for no benefit.

## 8. Resolving a Conflicting Dependabot PR

*Active Constraint*: A stale dependabot branch does not only contain its own bump. It was
cut from an older master, so accepting its `go.mod` wholesale **reverts every dependency
that moved since** — which can silently undo a security bump.

Seen for real in #1351: the branch was titled "bump scheduler 1.20.6 to 1.20.7", and also
took `config` back from 1.32.38 to 1.32.37, `route53` from 1.65.9 to 1.65.8, `sts` from
1.45.7 to 1.45.6, and five indirect lines with them.

**1. Diagnose before resolving.** Compare the dependency lines against master rather than
reading the conflict markers. Any line where the PR is *behind* master is a revert, not a
bump:

```bash
git show origin/master:go.mod | grep -n 'aws-sdk-go-v2'
git show origin/<dependabot-branch>:go.mod | grep -n 'aws-sdk-go-v2'
```

**2. Try `@dependabot recreate` first.** It costs nothing and keeps the branch
dependabot-owned. Only rebuild by hand when that will not converge.

**3. Rebuilding by hand.** Branch from current master, apply *only* the intended change,
and confirm the diff is minimal before pushing:

```bash
go get <module>@<version> && go mod tidy
git diff --stat                  # expect go.mod and go.sum only
git push --force-with-lease      # never bare --force
```

**4. Know what that costs.** Once anyone other than dependabot pushes to the branch,
**dependabot stops maintaining it** — it will neither rebase nor recreate it. If it goes
stale again it needs another manual rebuild, or an explicit `@dependabot recreate` comment.

**5. Verify the bump is real.** `go build ./...` and `go vet ./...`, and check the version
actually moved *forward*. The whole failure mode here is a bump that is quietly a
downgrade, so "CI is green" does not answer the question.

## 9. Don't Push Into a PR That's Already Merged Out From Under You
*Active Constraint*: A PR you say is "ready to merge" can be merged by someone else at any moment — check its live state (`gh pr view <number> --json state,mergedAt`) before pushing another commit to the same branch, not just before opening the PR. A merged PR is terminal: further pushes to its branch land nowhere (GitHub Actions may still run on them, which looks identical to a normal in-flight check from the CLI, but the code never reaches the target branch). This actually happened in this repo: a second commit was pushed to an already-merged PR, its checks appeared to pass normally, and the change silently never shipped until a later `git log` diff caught it.

After any merge you expect to close an issue (whether via a `Closes #N` reference or a manual close), verify the issue actually closed (`gh issue view <number> --json state`) instead of assuming the mechanism worked — squash-merge commit messages don't reliably carry every commit's closing reference from a multi-commit PR (this repo's squash setting concatenates commit messages, but a squash performed through the GitHub UI can still end up using only one of them). If it didn't close, close it manually with a comment pointing at the merge that actually resolved it.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-26* | *Last Reviewed: 2026-08-26*
