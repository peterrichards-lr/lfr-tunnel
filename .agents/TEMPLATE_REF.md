# Template Reference

This repo's AI-agent governance files (`AGENTS.md`, `.agents/`, `CLAUDE.md`, `.claude/`) were established independently and have no automated relationship to any other repo. `ai-agent-template` (https://github.com/peterrichards-lr/ai-agent-template) is maintained as the shared "lessons learned" reference for AI-agent rules across the Liferay tooling repos, but nothing here pulls updates from it automatically. This file is a manual checkpoint so drift gets tracked deliberately instead of silently.

**Reference repo**: <https://github.com/peterrichards-lr/ai-agent-template>
**Reference version at last check**: `v1.2.0` (origin/main @ `83b1cf2`, 2026-08-01)
**Last checked**: 2026-08-05

## Known drift as of this check

- [#931](https://github.com/peterrichards-lr/lfr-tunnel/issues/931) — pre-release version-bump PRs merge directly to `master` at high volume (10 in ~6 hours on one day); release branch naming disagrees between the script and `CONTRIBUTING.md`.
- [#932](https://github.com/peterrichards-lr/lfr-tunnel/issues/932) — an EDR/SentinelOne test-safety rule is stated with different strictness across 3 files; the file designated as the single source of truth is stale relative to the real incident that hardened the rule elsewhere. `AGENTS.md` itself states "single source of truth per topic... if you see the same rule stated differently in two places, that's a bug" — this repo is currently in violation of its own stated principle.
- [#933](https://github.com/peterrichards-lr/lfr-tunnel/issues/933) — the doc-review timestamp system is unreliable (an 18-day undetected drift was found) and duplicated across two near-identical checker scripts. `ai-agent-template` fixed the underlying cause of this class of bug in its PR #16 (append-only timestamp injection instead of rewrite-on-touch).

## Skills available in the reference repo (for comparison; not all apply here)

`coding-standards`, `documentation`, `github-workflow`, `human-in-the-loop`, `multi-agent-orchestration`, `reflection-and-planning`, `release-management`, `rule-adherence`, `tool-use-react`, `unit-testing`

`ai-agent-template`'s `release-management` and `rule-adherence` skills are directly relevant to the drift found above and are worth reading before fixing these issues locally.

## How to use this file

Before writing a new agent rule here, check whether `ai-agent-template` already documents a corrected/newer version of the same rule. If you find and fix a real process bug in this repo, consider whether the same lesson belongs in `ai-agent-template` too, so future repos don't inherit the same bug.

Update the "Last checked" date and the drift list above whenever this comparison is repeated.

---
*Last Updated: 2026-08-05* | *Last Reviewed: 2026-08-05*
