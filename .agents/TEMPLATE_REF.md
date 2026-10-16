# Template Reference

This repo's AI-agent governance files (`AGENTS.md`, `.agents/`, `CLAUDE.md`, `.claude/`) were established independently and have no automated relationship to any other repo. `ai-agent-template` (https://github.com/peterrichards-lr/ai-agent-template) is maintained as the shared "lessons learned" reference for AI-agent rules across the Liferay tooling repos, but nothing here pulls updates from it automatically. This file is a manual checkpoint so drift gets tracked deliberately instead of silently.

**Reference repo**: <https://github.com/peterrichards-lr/ai-agent-template>
**Reference version at last check**: `v2.6.0` (main @ `82860fb`, 2026-10-16)
**Last checked**: 2026-10-16

## Path mapping

A governed file this repo keeps at a different path from the template, one per line as
`` `local path` ← `upstream path` ``. `scripts/check_template_drift.py` compares the two files
instead of reporting one as missing and the other as extra.

- `docs/assets/stylesheets/liferay-blue.css` ← `.agents/templates/docs-profiles/liferay/liferay-blue.css`

## Known drift as of this check

- **`exclude_docs` on the profile stylesheet switches the profile off.** v2.6.0 looks for the
  `docs-profile: liferay` marker only in the built site, and `exclude_docs` drops the file from the
  build while the source stays byte-identical, so the drift check stays green. With the stylesheet
  also removed from `extra_css`, and `required` and the Google token deleted, everything passes.
  It takes those four edits together. Raised as
  [ai-agent-template#179](https://github.com/peterrichards-lr/ai-agent-template/issues/179). The
  variant that keeps `required` (Google's token swapped for Bing's) is closed here by listing
  `MISSING-VERIFICATION:google` in `required`.

v2.6.0 cleared the earlier items: a check listed in `required` now holds everything the profile's
default does (ai-agent-template#175), and the profile stylesheet is identified by the marker, so
a query string, a rename, a move or dropping it from `extra_css` alone no longer switches the
profile's checks off, and a renamed, unmapped, edited copy exits 3.

Resolved upstream and their local stopgaps removed: #141 by v2.3.0/v2.3.1 (the `--path`
arguments, [#2409](https://github.com/peterrichards-lr/lfr-tunnel/issues/2409)); #155 by v2.4.0
(most of `scripts/check-docs-config.py`, [#2411](https://github.com/peterrichards-lr/lfr-tunnel/issues/2411));
by v2.5.0, the related block, Google's token by name and the stylesheet suffix; and by v2.6.0
#175 and the stylesheet marker
([ai-agent-template#163](https://github.com/peterrichards-lr/ai-agent-template/issues/163); the last
of `scripts/check-docs-config.py`, [#2413](https://github.com/peterrichards-lr/lfr-tunnel/issues/2413)).
No local stopgap remains.

The three items recorded at the 2026-08-05 check are all closed:
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
| 2. Turn bug classes into blocking guards | **Already the practice**: ~35 bespoke `scripts/check-*` gates and ratchets, each citing its incident and most tested with a control in `tests/hooks`. A shared `.guards.json` would refactor working code and lose the incident-specific messages. |
| 3. Engineering principles, not just etiquette | **Two of five adopted** ([#2361](https://github.com/peterrichards-lr/lfr-tunnel/issues/2361)): "a passing check is evidence only if it could have failed" and "unknown is not absent", in AGENTS.md. The other three add little to its Pragmatism section. |
| 4. A fast inner loop separate from the full gate | **Already met**: commit-time hooks are fast, push runs the full suite, and `make test PKG=… TEST_FLAGS=…` scopes a run. |
| 5. Reversible setup | **Not applicable**: no bootstrap. The one destructive helper, `make prune-branches`, already writes a recovery manifest and refuses `master` and `checksums`. |
| 6. Measure whether instructions help (skill evals) | **Not adopted yet**: the brief records its harness as never run against a real model, and here it would mean running agents in throwaway worktrees on an EDR-watched machine. Revisit once the template has results. |

## 2026-10-16: the docs-site standard (v2.2.0)

The shared docs files moved from this repo to the template (ai-agent-template#135;
[#2407](https://github.com/peterrichards-lr/lfr-tunnel/issues/2407) here). The alerts hook, both
overrides, `liferay-blue.css` (mapped above), `scripts/check_docs_rendered.py` and
`scripts/check_template_drift.py` are verbatim copies of v2.2.0. Docs CI fails on any local edit to
them (`check_template_drift.py --docs-site --fail-on-drift`). `check_docs_rendered.py` replaced
this repo's `check-docs-rendered.sh`, `check-docs-home-layout.py` and `check-docs-nav-order.py`.

**Deliberate divergence**: `scripts/check_docs_review.py` is this repo's own (`--review-only`,
`--changed-files`, #2389) and is kept. Since v2.3.1 the drift checker imports nothing from it, so
the `FOOTER_REGEX` once added for that import is gone again (#2409).

## 2026-10-16: synced to v2.3.1

[#2409](https://github.com/peterrichards-lr/lfr-tunnel/issues/2409): one sync from v2.2.0, skipping
v2.2.1 and v2.3.0. Changed files: `liferay-blue.css` (the section-title rule), `check_docs_rendered.py`
and `check_template_drift.py`; the hook and both overrides were unchanged. The stamp records the
v2.3.1 tag's own commit, `84bf45a`; the template's `main` was one CHANGELOG-only commit ahead.

## 2026-10-16: synced to v2.4.0

[#2411](https://github.com/peterrichards-lr/lfr-tunnel/issues/2411): `check_docs_rendered.py` and
`check_template_drift.py` changed; the hook, both overrides and `liferay-blue.css` did not. The
deploy's drift step takes the template's stamp-aware form. Stamped on `d7abfa6`, the v2.4.0 tag,
which was also the template's `main`.

## 2026-10-16: synced to v2.5.0

[#2413](https://github.com/peterrichards-lr/lfr-tunnel/issues/2413): `check_docs_rendered.py` and
`check_template_drift.py` changed; the hook, both overrides and `liferay-blue.css` did not. PR CI
and the deploy both run the drift check `--against-recorded`, and Dependabot's PRs skip it with a
step-level `if` while still building and running the rendered check. Stamped on `cadd17b`, the
v2.5.0 tag, which was also the template's `main`. The template's mkdocs `>= 2` Dependabot ignore
is not added: this repo's Dependabot has no `pip` entry, and `requirements-dev.txt` pins
`mkdocs==1.6.1`, so nothing can propose a 2.x bump.

## 2026-10-16: synced to v2.6.0

[#2415](https://github.com/peterrichards-lr/lfr-tunnel/issues/2415): `liferay-blue.css` (the
`docs-profile: liferay` marker comment), `check_docs_rendered.py` and `check_template_drift.py`
changed; the hook and both overrides did not, nor the template's `docs.yml` or `dependabot.yml`.
Stamped on `82860fb`, the v2.6.0 tag and the template's `main`. One known-drift item remains
(`exclude_docs`, above).

## Skills available in the reference repo (for comparison; not all apply here)

`coding-standards`, `docs-site-standard`, `documentation`, `documentation-review`, `e2e-verification`, `github-workflow`,
`human-in-the-loop`, `multi-agent-orchestration`, `no-assumptions`, `reflection-and-planning`,
`release-management`, `rule-adherence`, `template-sync`, `tool-use-react`, `unit-testing`

## How to use this file

Before writing a new agent rule here, check whether `ai-agent-template` already documents a corrected/newer version of the same rule. If you find and fix a real process bug in this repo, consider whether the same lesson belongs in `ai-agent-template` too, so future repos don't inherit the same bug.

Update the "Last checked" date and the drift list above whenever this comparison is repeated.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-16* | *Last Reviewed: 2026-10-16*
