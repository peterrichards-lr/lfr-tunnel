# Liferay Tunnel — Agent Rules Router

This is the entry point for any AI coding agent working in this repository, at the
location most agent tooling looks for automatically. It is deliberately short: it
routes to one canonical file per topic under `.agents/skills/`, rather than
restating their content here. If you're an agent and this is the first file you've
read in this repo, read the relevant skill file(s) below **before** taking any
action that matches their trigger condition.

**Single source of truth per topic.** Each rule below lives in exactly one file.
If you ever see the same rule stated differently in two places, that's a bug —
fix the drift (make one link to the other) rather than trusting either
in isolation.

## Skills are discoverable through `.claude/skills`

`.claude/skills` is a symlink to `.agents/skills`. It has to exist: Claude Code enumerates
project skills from `.claude/skills/`, so without it none of the skills below appear in an
agent's skill list and the only route to them is this file — which makes one missed read cost
everything they contain (#1433).

Keep the files themselves in `.agents/`, which is tool-neutral. Add a new skill as
`.agents/skills/<name>/SKILL.md` with `name` and `description` frontmatter; the symlink picks it
up with no further wiring. `/reload-skills` re-scans without restarting a session.

## Rules, by topic

- **Local test/build safety (SentinelOne EDR)** — [`.agents/skills/edr-constraints/SKILL.md`](.agents/skills/edr-constraints/SKILL.md)
  Read before running any Go test or local binary. Non-negotiable: `make test` only,
  never bare `go test`, never run the `lfr-tunnel` client binary directly.
- **GitHub issue tracking & PR workflow** — [`.agents/skills/github-workflow/SKILL.md`](.agents/skills/github-workflow/SKILL.md)
  Read before planning a feature, filing an issue, or opening a PR. Covers the
  issue → branch → PR → close lifecycle and tech-debt tracking.
  **Claim an issue before starting work on it** (§3). Multiple agents run this
  backlog concurrently, so resolve the session ID in an issue's claim comment
  before touching it — the `agent:` label alone cannot tell two Claude sessions
  apart, and you must not work on what another session holds.
- **Documentation timestamps & review** — [`.agents/skills/global-docs/SKILL.md`](.agents/skills/global-docs/SKILL.md)
  Read before creating or editing any `.md` file.
- **Edge node state synchronization** — [`.agents/skills/edge-sync/SKILL.md`](.agents/skills/edge-sync/SKILL.md)
  Read before modifying any in-memory tunnel/lease state on the control plane.
- **Operations, deployment, builds, signing** — [`.agents/skills/lfr-tunnel-ops/SKILL.md`](.agents/skills/lfr-tunnel-ops/SKILL.md)
  Read before building, signing, deploying, or running maintenance operations.
- **Contribution workflow, branching and cleanup** — [`CONTRIBUTING.md`](CONTRIBUTING.md)
  Read before merging a PR or tidying branches. It is the only place several rules live, and
  they are not derivable from the code:
  - **Never delete the `checksums` branch.** It is an orphaned branch the release workflow
    pushes to, and the portal fetches client checksums from it over
    `raw.githubusercontent.com` because GitHub Release assets fail CORS
    (`docs/architecture.md` "Decoupled 'checksums' CDN Branch"). Deleting it breaks checksum
    delivery to the portal silently. Stated here rather than one hop away because a
    destructive action should not need a second read to be warned about.
  - **Delete feature and fix branches, local and remote, as soon as they merge** — but that
    rule and the one above sit two clauses apart, so tidying branches on general principle is
    exactly how the `checksums` branch gets removed.
  - Release branches are `release/<version>`, and only one release PR may be open at a time.
    `scripts/create-release-tag.sh` enforces both.
  - Every PR builds standalone binaries for Linux, macOS and Windows, downloadable from its
    Actions run — useful for reproducing a report without a local build.
- **Upstream/JIRA bug tracking** — [`.agents/skills/jira-tracker/SKILL.md`](.agents/skills/jira-tracker/SKILL.md)
  Read when you discover an upstream platform bug or limitation, not a bug in this repo. Covers
  the `jira/todo` → `jira/open` → `jira/closed` lifecycle, the file naming, and the report
  template.

## Groundedness — verify before asserting

Don't answer questions about this codebase's architecture, logic, or behavior from
assumption, memory of similar codebases, or a filename's implication. Before making
a factual claim, actually read the relevant source and cite what you found (file
path and line number). This applies within your normal turn — there is no
requirement to split verification and answering across separate turns; the
requirement is that the answer be grounded, not that it take a particular number
of turns to produce.

If you cannot verify a claim with reasonable effort, say so explicitly (e.g. "I
could not confirm this in the codebase — treating it as unverified") rather than
presenting a guess as fact. A wrong grounded-sounding answer is worse than an
honest "I don't know."

## Pragmatism & Velocity Principles

To maintain high developer velocity while preserving software quality:

- **Surgical Fixes & Minimal Diffs**: Focus strictly on the requested bug or feature. Do not refactor surrounding working code or re-architect functional logic unless explicitly asked.
- **Preserve CLI, API & Operational Semantics**: Never alter or break existing CLI flags, API behavior, or user habits under the guise of "intent vs mechanism" or "semantic purity."
- **Verify Claims Historically**: Before asserting that a feature "never worked" or "is broken," inspect past commit history (`git log -S`) to avoid misdiagnosing a recent regression as an initial design flaw.
- **No Unsolicited Audit Cascades**: Do not create cascades of secondary micro-debt issues during routine bug fixes unless a full audit was explicitly requested.

## Scratch files

Don't commit temporary scratch scripts, one-off plan files, or debug helper
scripts to the repository. Use whatever scratch/temp mechanism your own tooling
provides (most agent harnesses expose one); if yours doesn't, use a local
`.scratch/`-style directory that's already `.gitignore`d, or delete the file
before finishing your task. This rule is intentionally tool-agnostic — don't hardcode
a specific tool's temp-directory convention here, since different agents use
different ones.

## Signing and release

- **Never bypass branch protection** (`gh pr merge --admin` or equivalent) to force
  a merge. Let CI checks pass naturally. If a check is wrong, fix the check, don't
  route around it.
- `bin/lfr-tunnel-ops` is not a committed binary — build it first
  (`go build -o bin/lfr-tunnel-ops ./cmd/lfr-tunnel-ops`) before using it. Never
  substitute `go run ./cmd/lfr-tunnel-ops` for this (see the EDR constraints skill
  above — that pattern risks the same local-execution problem).
- Client binaries must be signed before release/deployment — see the
  `lfr-tunnel-ops` skill for the exact signing command and required environment.

## Linting locally: containerized, not installed

`golangci-lint` is deliberately **not** installed via `brew` or `go install`. Its
absence is the intended state, not something to fix. This repo's own
`scripts/pre-commit-hook.sh` runs it in a container, and CI runs the same image:

```
docker run --rm -v "$(pwd)":/app -w /app golangci/golangci-lint:latest golangci-lint run
```

Use that. Installing the binary adds a local toolchain the project does not use and
diverges from what CI actually enforces.

Known local limitation: it can OOM inside the colima VM while compiling the AWS SDK
(`signal: killed` on `pkg/provisioner/aws.go`), including when scoped to one package,
because `pkg/server` imports `pkg/provisioner` transitively. That is a memory limit,
not a code defect — CI's Lint & Format Check is authoritative. Scoping the run to the
packages you touched (`golangci-lint run ./pkg/ops/...`) usually gets under it.

## Shell scripts: bash 3.2 if a developer runs it

macOS ships **bash 3.2.57** as `/bin/bash` — the last GPLv2 release, frozen in 2007.
Anything a developer is expected to run locally has to work there, or it silently
becomes CI-only.

So in `scripts/` and `tests/hooks/`: **no `declare -A` / `local -A`, no `mapfile` /
`readarray`, no `${var^^}` / `${var,,}`.** Use a delimited string plus
`while IFS='|' read -r`, or two indexed arrays.

Scripts that only ever run on Linux — `scripts/common/`, `scripts/liferay/`,
`tests/e2e/`, and workflow `run:` blocks — may use bash 4+ freely.

`tests/hooks/test-shell-portability.sh` (via `make test-hooks`) enforces this. The
portable set is **derived**, not listed: it is every `.sh` the Makefile or a git
hook invokes, minus the exempt prefixes. So wiring a new script into `make` covers
it automatically, and nothing has to be kept in sync by hand.

The corollary: **a script must be make-reachable to be covered.** That is why
`scripts/check-required-contexts.sh` has a `make check-contexts` target — a
pre-push gate with no convenient invocation is a gate nobody runs. The test
asserts the derived set is non-empty and contains that script, so a broken
derivation fails loudly instead of reporting a clean pass over nothing.

It then actually runs the gate under both bash 3.2 and bash 5 and requires them to
agree — on a passing tree *and* a deliberately broken one.

**`bash -n` does not catch this** (verified): `declare -A` parses fine under 3.2 and
only fails at run time, as `<key>: unbound variable` under `set -u`. That is how
`check-required-contexts.sh` shipped in #1391 exiting 1 before checking anything — a
gate whose entire value was being runnable before pushing (#1395).

## Rules-integrity check

Every path, script, and directory named in this file and the skill files under
`.agents/skills/` is expected to actually exist in this repo. A rule that points
at something nonexistent is worse than no rule — it teaches distrust of the whole
rules corpus. If you find a reference here that doesn't resolve, either fix the
target (build the missing thing, if it's genuinely still wanted) or fix the
reference (update or remove it) — don't leave it dangling for the next agent to
trip over.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-26* | *Last Reviewed: 2026-08-26*
