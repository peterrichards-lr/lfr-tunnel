---
name: edr-constraints
description: Critical SentinelOne End Point Detection and Response (EDR) constraints for the local development environment. Activate this skill to understand why test binaries are quarantined and how to safely run Go tests locally.
---

# SentinelOne Execution Constraints (CRITICAL)

> [!CAUTION]
> **DO NOT EVER RUN `go test ./...` OR `go test` DIRECTLY.**
> **SENTINELONE WILL DETECT THE DYNAMIC TEST EXECUTABLE (`*.test`), QUARANTINE IT, AND KILL ADJACENT SYSTEM PROCESSES (BREW, JENV, LDM). THIS CAN CRASH THE AGENT AND DESTROY THE USER'S LOCAL WORK ENVIRONMENT.**
> **ALWAYS, STRICTLY, WITHOUT EXCEPTION, USE `make test` FOR ANY GO TESTING.**

- **Active Test Execution Constraint**: Always run unit tests via `make test` or explicitly export `GOTMPDIR=/private/tmp` (or `LFT_TEST_DIR=/private/tmp`) when compiling Go test binaries.
- **Pre-execution Verification**: Verify test binaries target `$(LFT_TEST_DIR)/lfr-tunnel` (defaulting to `/private/tmp/lfr-tunnel`). Any execution out of `/var/folders/...` or with arbitrary binary filenames will trigger SentinelOne (S1), which forcefully terminates adjacent system processes and daemon tasks.
- **Environment Variable Overrides**:
  - `LFT_TEST_DIR`: Directory where test binaries are pre-compiled and run (defaults to `/private/tmp`).
  - `GOTMPDIR`: Automatically set to `LFT_TEST_DIR` during `make test` to prevent Go from creating temporary build caches in `/var/folders`.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-23* | *Last Reviewed: 2026-07-23*
