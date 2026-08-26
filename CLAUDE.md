# Agent rules

All rules live in **[`AGENTS.md`](AGENTS.md)**, which routes to one canonical file per
topic under `.agents/skills/`. **Read it before taking any action in this repo.**

This file is a pointer only, deliberately. It previously restated the EDR rules inline
and had already drifted from the skill it copied — a day stale, missing #1402's findings,
and indistinguishable from current. One rule, one home (#1412).

Start with [`.agents/skills/edr-constraints/SKILL.md`](.agents/skills/edr-constraints/SKILL.md)
if you are about to run a Go test or any local binary. Those rules are non-negotiable and
getting them wrong has cost three full environment reinstalls.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-26* | *Last Reviewed: 2026-08-26*
