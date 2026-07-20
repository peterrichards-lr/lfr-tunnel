# Liferay Tunnel Agents Ecosystem

This directory contains the central repository of rules, constraints, and operational patterns for AI Agents working on the `lfr-tunnel` project.

> **Note to Agents**: This file acts as a routing index. When you are performing specific actions, you must review the dedicated SKILL or module listed below.

## Modular Skills & Operational Directives

The monolithic rules have been broken down into modular skills so that they can be loaded contextually. Always check the `skills` directory for relevant operational directives before executing complex workflows.

- **GitHub Issue Sync & PRs**: [`.agents/skills/github-workflow/SKILL.md`](skills/github-workflow/SKILL.md)
  *Must be loaded when planning tasks, creating GitHub issues, or opening Pull Requests. Includes the mandatory `Closes #<issue>` constraint.*

- **SentinelOne & EDR Constraints**: [`.agents/skills/edr-constraints/SKILL.md`](skills/edr-constraints/SKILL.md)
  *Must be loaded when running Go tests or local binaries to prevent SentinelOne from crashing your environment.*

- **Documentation Standards & Timestamps**: [`.agents/skills/global-docs/SKILL.md`](skills/global-docs/SKILL.md)
  *Must be loaded when reviewing, creating, or modifying markdown documentation, and after implementing any feature or code change.*

- **Edge Node Synchronization**: [`.agents/skills/edge-sync/SKILL.md`](skills/edge-sync/SKILL.md)
  *Must be loaded when modifying the control plane's tunnel state to ensure it propagates to edge nodes.*

## Active Runtime Constraints (CRITICAL)

- **No Assumptions (Anti-Hallucination Rule)**: Any technical statement, explanation, or conclusion you make MUST be strictly based on actual, referenceable code or documentation in this repository. You are expressly forbidden from making blind assumptions about how systems (like edge nodes or routing logic) behave without verifying them via search, reading the code, or consulting `AGENTS.md`/`GEMINI.md`. When the resources are available to you, use them before you speak.
- **EDR Whitelist Restrictions**: Do NOT run the `lfr-tunnel` or `lfr-tunneld` binaries outside the whitelisted directory (`/private/tmp/lfr-tunnel`). Doing so will trigger SentinelOne (S1), which will forcefully kill the process, Homebrew, and the Antigravity agent itself. Rely on automated GitHub workflows or explicit whitelist paths for testing.
- **Tech Debt Tracking**: If you encounter any of the 10 catalogued tech debt categories during your work, you MUST record it by raising a GitHub issue with the `tech debt` label via the `gh` CLI. The 10 categories are:
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
  You do not need to tackle the technical debt immediately unless it can be resolved without diverting significant effort from your primary task. The ultimate requirement is to ensure it is recorded in the backlog.

## Internal Tools & Customization

The `.agents/skills` directory also contains operational scripts and infrastructure logic:
- `lfr-tunnel-ops`: Scripts and commands for deploying the Gateway to the VPS.
- `jira_tracker`: Logic for categorizing JIRA bugs and upstream constraints.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
