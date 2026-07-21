---
name: edr-constraints
description: Critical SentinelOne End Point Detection and Response (EDR) constraints for the local development environment. Activate this skill to understand why test binaries are quarantined and how to safely run Go tests locally.
---

# SentinelOne Execution Constraints (CRITICAL)

> [!CAUTION]
> **DO NOT EVER RUN `go test ./...` OR `go test` DIRECTLY.**
> **SENTINELONE WILL DETECT THE DYNAMIC TEST EXECUTABLE (`*.test`), QUARANTINE IT, AND KILL ADJACENT SYSTEM PROCESSES (BREW, JENV, LDM). THIS CAN CRASH THE AGENT AND DESTROY THE USER'S LOCAL WORK ENVIRONMENT.**
> **ALWAYS, STRICTLY, WITHOUT EXCEPTION, USE `make test` FOR ANY GO TESTING.**

- **Active Test Execution Constraint**: The moment you decide to run any Go tests, you MUST formulate the test command exactly as: `make test` or `go test -c -o /private/tmp/lfr-tunnel <pkg> && /private/tmp/lfr-tunnel`.
- **Pre-execution Verification**: Before executing a binary execution command, verify that the path strictly starts with `/private/tmp/lfr-tunnel`. Any deviation will trigger SentinelOne (S1), which will forcefully kill the process, Homebrew, and the Antigravity agent itself.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
