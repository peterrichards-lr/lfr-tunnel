---
name: edr-constraints
description: Critical SentinelOne End Point Detection and Response (EDR) constraints for the local development environment. Activate this skill to understand why test binaries are quarantined and how to safely run Go tests locally.
---

# SentinelOne Execution Constraints (CRITICAL)

> [!CAUTION]
> **DO NOT EVER RUN `go test ./...` OR `go test` DIRECTLY.**
> **SENTINELONE WILL DETECT THE DYNAMIC TEST EXECUTABLE (`*.test`), QUARANTINE IT, AND KILL ADJACENT SYSTEM PROCESSES (BREW, JENV, LDM). THIS CAN CRASH THE AGENT AND DESTROY THE USER'S LOCAL WORK ENVIRONMENT.**
> **ALWAYS, STRICTLY, WITHOUT EXCEPTION, USE `make test` FOR ANY GO TESTING.**

- **Local Binary Execution Constraints**: The local system EDR (SentinelOne) blocks unsigned `lfr-tunnel` binaries and dynamic Go test run executables (`*.test`). **This will crash the AI Agent.** 
- Do NOT run `go test ./...` or `go test` directly. 
- To run Go tests safely, you must iterate over packages and explicitly compile the test binary to the exact whitelisted file before executing it: `go test -c -o /private/tmp/lfr-tunnel <pkg> && /private/tmp/lfr-tunnel`. (This logic is codified in the `Makefile` and `pre-commit-hook.sh`).
- Use `make test` instead of `go test`.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
