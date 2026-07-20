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

- **EDR Whitelist Restrictions**: Do NOT run the `lfr-tunnel` or `lfr-tunneld` binaries outside the whitelisted directory (`/private/tmp/lfr-tunnel`). Doing so will trigger SentinelOne (S1), which will forcefully kill the process, Homebrew, and the Antigravity agent itself. Rely on automated GitHub workflows or explicit whitelist paths for testing.

## Internal Tools & Customization

The `.agents/skills` directory also contains operational scripts and infrastructure logic:
- `lfr-tunnel-ops`: Scripts and commands for deploying the Gateway to the VPS.
- `jira_tracker`: Logic for categorizing JIRA bugs and upstream constraints.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
