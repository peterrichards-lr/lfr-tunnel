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

- **No Assumptions (Anti-Hallucination Protocol)**: 
  *Active Constraint*: You are FORBIDDEN from answering any question regarding system architecture, logic, or behavior in a single turn. 
  - **Step 1**: You MUST first explicitly state your intent to verify the codebase, execute `grep_search` or `run_command` to find the relevant code, and then **end your turn** without answering the user's question.
  - **Step 2**: Only in the *subsequent* turn, after you have read the actual source code into your context window, are you permitted to formulate your answer. You MUST cite the exact file and line number in your response.
- **EDR Whitelist Restrictions**: 
  *Active Constraint*: You are FORBIDDEN from executing `go run` or `go test` directly on this machine. If you need to test Go code, you MUST formulate your command exactly as: `go test -c -o /private/tmp/lfr-tunnel <pkg> && /private/tmp/lfr-tunnel`. Any deviation will trigger a SentinelOne EDR kill-switch.

- **Mandatory Client Binary Signing**:
  *Active Constraint*: You are FORBIDDEN from deploying client binaries to the VPS or creating a final release unless you have successfully executed codesigning signatures on the binaries. You MUST run the signing command using 1Password credentials integration (`op run -- ./bin/lfr-tunnel-ops sign` or equivalent CLI options) to ensure Darwin, Windows, and Linux targets are properly signed.

- **Tech Debt Tracking (Immediate Logging)**: 
  *Active Constraint*: The moment you identify any of the 10 catalogued tech debt categories during your work, you MUST pause your current workflow and immediately execute `gh issue create --title "Tech Debt: [Topic]" --body "[Details]" --label "tech debt"` before taking any other action.
  The 10 categories are:
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

## Internal Tools & Customization

The `.agents/skills` directory also contains operational scripts and infrastructure logic:
- `lfr-tunnel-ops`: Scripts and commands for deploying the Gateway to the VPS.
- `jira_tracker`: Logic for categorizing JIRA bugs and upstream constraints.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-22* | *Last Reviewed: 2026-07-22*
