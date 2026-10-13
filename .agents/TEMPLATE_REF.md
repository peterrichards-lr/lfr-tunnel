# Template Reference

This repo's AI-agent governance files (`AGENTS.md`, `.agents/`, `CLAUDE.md`, `.claude/`) were established independently and have no automated relationship to any other repo. `ai-agent-template` (https://github.com/peterrichards-lr/ai-agent-template) is maintained as the shared "lessons learned" reference for AI-agent rules across the Liferay tooling repos, but nothing here pulls updates from it automatically. This file is a manual checkpoint so drift gets tracked deliberately instead of silently.

**Reference repo**: <https://github.com/peterrichards-lr/ai-agent-template>
**Reference version at last check**: `v2.1.0` (main @ `f304c4a`, 2026-09-22)
**Last checked**: 2026-10-13

## Known drift as of this check

None open. The three items recorded at the 2026-08-05 check are all closed:
[#931](https://github.com/peterrichards-lr/lfr-tunnel/issues/931) (release-bump volume and branch
naming), [#932](https://github.com/peterrichards-lr/lfr-tunnel/issues/932) (the EDR rule stated
three ways) and [#933](https://github.com/peterrichards-lr/lfr-tunnel/issues/933) (the doc-review
timestamp system).

## 2026-10-13: the REA lessons

The template's review of `morluto/rea` produced six lessons, each assessed here against what this
repo actually has. They were assessed from that review's written brief: the
`feature/rea-lessons` branch it describes was not on GitHub at this check, so none of it was
compared as merged template code.

| Lesson | Here |
| --- | --- |
| 1. Generate the skill index and check it | **Adopted as a check, not a generator** ([#2363](https://github.com/peterrichards-lr/lfr-tunnel/issues/2363)): `scripts/check-skill-routing.py`. AGENTS.md routes by what an agent is *about to do*, with reasoning, which frontmatter cannot produce. |
| 2. Turn bug classes into blocking guards | **Already the practice**: ~35 bespoke `scripts/check-*` gates and ratchets, each citing its incident and tested with a control. A shared `.guards.json` would refactor working code and lose the incident-specific messages. |
| 3. Engineering principles, not just etiquette | **Two of five adopted** ([#2361](https://github.com/peterrichards-lr/lfr-tunnel/issues/2361)): "a passing check is evidence only if it could have failed" and "unknown is not absent", in AGENTS.md. The other three add little to its Pragmatism section. |
| 4. A fast inner loop separate from the full gate | **Already met**: commit-time hooks are fast, push runs the full suite, and `make test PKG=… TEST_FLAGS=…` scopes a run. |
| 5. Reversible setup | **Not applicable**: no bootstrap. The one destructive helper, `make prune-branches`, already writes a recovery manifest and refuses `master` and `checksums`. |
| 6. Measure whether instructions help (skill evals) | **Not adopted yet**: the brief records its harness as never run against a real model, and here it would mean running agents in throwaway worktrees on an EDR-watched machine. Revisit once the template has results. |

## Skills available in the reference repo (for comparison; not all apply here)

`coding-standards`, `documentation`, `documentation-review`, `e2e-verification`, `github-workflow`,
`human-in-the-loop`, `multi-agent-orchestration`, `no-assumptions`, `reflection-and-planning`,
`release-management`, `rule-adherence`, `template-sync`, `tool-use-react`, `unit-testing`

## How to use this file

Before writing a new agent rule here, check whether `ai-agent-template` already documents a corrected/newer version of the same rule. If you find and fix a real process bug in this repo, consider whether the same lesson belongs in `ai-agent-template` too, so future repos don't inherit the same bug.

Update the "Last checked" date and the drift list above whenever this comparison is repeated.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-13* | *Last Reviewed: 2026-10-13*
