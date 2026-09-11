---
name: edr-constraints
description: Critical SentinelOne End Point Detection and Response (EDR) constraints for the local development environment. Activate this skill to understand why test binaries are quarantined and how to safely run Go tests locally.
---

# SentinelOne Execution Constraints (CRITICAL)

> [!CAUTION]
> **NEVER run a bare `go test` (or `go test ./...`) in this repo. ALWAYS use `make test` instead.** This is a hard, non-negotiable rule, not a preference.

- **`GOTMPDIR` is the control, not `-o`** (#1337). The toolchain links every executable inside `GOTMPDIR` — or the system temp dir (`/private/var/folders/...` on macOS) when it is unset — and only *then* moves it to the `-o` path. `-o` decides where a binary ends up; `GOTMPDIR` decides where it first exists, and an EDR watching temp directories only sees the second. Measured:
  ```
  $ GOTMPDIR=$WATCHED go build -o $OUT/bin ./cmd/lfr-tunnel-ops
  $WATCHED/go-build2292438673/b001/exe/a.out     <-- the complete linked binary, despite -o
  ```
  **Adding `-o` to a command does not make it safe.** This misreading is what put an unsafe `go test -c -o` in the repo's own pre-commit hook and let the safety check exempt it.
- **Why it matters here**: plain `go test [pkg]` compiles an unsigned test binary into the default Go temp dir, NOT `/private/tmp`. Many packages' tests open real network listeners/sockets (e.g. `pkg/server`'s tests use `httptest.NewServer` and real WebSocket connections; `cmd/lfr-tunnel/main_test.go`'s `TestMain_ValidationFailure` even re-execs itself into the real client `main()`). An unsigned freshly-built binary, sitting in an arbitrary temp path, that then opens network connections is exactly the pattern SentinelOne (S1) flags as a dropped malicious binary — it does NOT matter whether the package under test is the client, the server, or anything else.
- **This already happened once**: `server.test`, flagged "Malicious file executed", 2026-07-28 — cost a full local environment reinstall. S1 terminated the session and deleted unrelated tooling (Claude, jenv, python, LDM, Homebrew) as collateral damage.
- **The only safe way to run tests is `make test`** — it **exports `GOTMPDIR`** as `LFT_TEST_DIR` (default `/private/tmp`, S1-whitelisted), asserts the toolchain really resolved it (`make edr-guard`), and builds and executes there. The `-o` only names the destination; the export is what keeps the binary out of `/var/folders`. This is enforced by a deny rule in `.claude/settings.json` blocking any `Bash(go test*)` invocation outright — do not try to work around it, including by manually exporting `GOTMPDIR`/`LFT_TEST_DIR` and invoking `go test` directly. `make test` sets those variables internally as part of its own build step; they are not a substitute for running it.
- **Pre-execution verification**: if you need to confirm where a test binary landed, verify it targets `$(LFT_TEST_DIR)/lfr-tunnel` (defaulting to `/private/tmp/lfr-tunnel`). Anything running out of `/var/folders/...` or with an arbitrary binary filename means `make test` wasn't actually used.
- **Also never run the `lfr-tunnel` client binary/process directly on the host**: `go run ./cmd/lfr-tunnel`, the built `bin/lfr-tunnel` binary, or the `lfr-tunnel.sh`/`lfr-tunnel.bat` wrappers. Also denied in `.claude/settings.json`.
- **Fine to run directly**: `go build` (compiles but doesn't execute), `go vet`, `gofmt`, `go list`, and the `lfr-tunnel-ops` (deploy tooling) binary. **Not** `lfr-tunneld` -- see "Running the server locally" below, that claim was wrong. The client running inside a Docker container (e.g. `make e2e` / `tests/e2e/run.sh`) is a different risk profile and is not blocked.
- If ever unsure whether a command would build-and-run code outside `LFT_TEST_DIR`, stop and ask the user first rather than guessing.
- **A tool you were told to build rather than `go run` can still spawn `go run` itself** (#1402). The rule had always been applied to how `lfr-tunnel-ops` is *invoked* — build it, never `go run ./cmd/lfr-tunnel-ops` — and never to what it *does*. `pkg/ops/sign.go` shelled out to `go run scripts/minisign_helper.go` on every `sign`, and `sign` is documented as being run directly (`op run -- lfr-tunnel-ops sign`), never through `make`, so `GOTMPDIR` was unset and it linked and executed out of `/var/folders` each time. Now done in-process via `pkg/minisign`.
- **`scripts/check-edr-safety.sh` covers Go source as of #1402, and needed two changes to do it.** Adding `--include=*.go` alone caught nothing: the script's patterns are the literal `go run` / `go test`, and Go spells it as separate string literals — `exec.Command("go", "run", ...)`. Measured against the real defect: includes alone → exit 0; includes plus a `"go"[[:space:]]*,[[:space:]]*"(run|test)"` pattern → exit 1. `tests/hooks/test-edr-guard.sh` (via `make test-hooks`) holds both halves in place. When widening this guard again, check that the new coverage actually fires — an include that matches no pattern reads as coverage and is none.

## The `go` PATH shim -- local defence in depth (#1860)

**`GOTMPDIR` is pinned by the Makefile and by nothing else.** `Makefile:34` exports it, so every
`make` target is safe -- but a bare `go build` at a prompt, and `lfr-tunnel-ops build` through
`exec.Command`, inherit nothing and link into `/var/folders`. That is what SentinelOne acted on at
2026-09-09 10:55: it quarantined this project's freshly built Mach-O binaries (size-matched to
`dist/lfr-tunnel-darwin-*` and `bin/lfr-tunnel-ops` within 16 bytes) and took 61 tracked gate
scripts, the three `.git/hooks` shims and two node `bin` directories with them as remediation
collateral. **The scripts were never the detection.** See #1859 for the repo-side fix.

`make install-go-guard` installs `scripts/edr-go-wrapper.sh` as `go` on PATH ahead of the real
toolchain. It **fixes rather than blocks** -- `deploy` -> `make ui-dist` genuinely needs
`go build`, so refusing the command would break releases:

| Form | Verdict |
| --- | --- |
| `go run …` | refused -- links *and executes* from an arbitrary temp path |
| `go test …` without `-c` | refused -- same shape |
| `go test -c -o …` | **allowed** -- compiles without executing, and `Makefile:154` depends on it |
| everything else | allowed, with `GOTMPDIR` pinned to the whitelist |

Measured before and after, same command: `WORK=/var/folders/62/.../T/go-build1726362403` became
`WORK=/private/tmp/go-build3661106187`.

**Why a PATH shim and not an alias or a rename**, each measured rather than assumed: agent shells
are non-interactive so `.zshrc` is never read, and `make` recipes run under `/bin/sh` which never
reads it either, so an alias would miss the Makefile and `ops build` entirely. Renaming the
toolchain silently self-heals, because `/opt/homebrew/bin/go` is a brew symlink that the next
`brew upgrade go` restores -- leaving a guard that has stopped existing and looks identical to one
that works, the same trap `AGENTS.md` rejected `core.hooksPath` for.

Two things to know:

- **It is machine-local. It is not the fix.** CI, a fresh clone and anyone else's laptop have no
  shim. #1859 is the durable half.
- **It is silent when nothing needs changing.** Inside `make`, `GOTMPDIR` is already correct, so
  the shim says nothing -- verified by `make test` passing 18/18 with zero notices. If the two
  ever disagree about the whitelist, `Makefile:34`'s `:=` wins and the shim's guarantee
  evaporates, which is why both derive it the same way from `LFT_TEST_DIR`.

`tests/hooks/test-go-guard.sh` holds all of it in place, including a CONTROL that strips the
refusal and requires the test to notice. It earned that: an earlier draft resolved `head`/`grep`
through PATH, so on a minimal PATH the shim failed to recognise itself, chose itself as the
toolchain and `exec`'d itself forever. The test hung, which is how it was found.

## Running the server locally -- DON'T (as of 2026-08-13, no verified-safe way exists)

Three incidents in a row now, each one a full local environment reinstall (Homebrew, jenv,
Python, Claude Code itself all deleted as collateral damage):
1. `server.test` (2026-07-28) -- a bare `go test` binary.
2. `lfr-tunneld` built to a manually-chosen path under `/private/tmp` (2026-08-13) -- wrong
   assumption that the whole tree under `/private/tmp` is whitelisted; it's actually the
   *exact literal path* `make test` uses (`$LFT_TEST_DIR/lfr-tunnel`), not the directory tree.
3. `lfr-tunneld` built via `make build` to `bin/lfr-tunneld` inside the repo and run from there
   (2026-08-13, same day) -- this is the pattern this document *previously* called "fine to run
   directly" above. It got killed too. That claim was wrong.

**Conclusion: there is no currently-verified-safe way to execute `lfr-tunneld` or `lfr-tunnel`
locally on this machine, regardless of build location.** Only `make test`'s own mechanism has
ever actually completed without being killed. Do not invent another script or path meant to
make this safe -- that reasoning has now failed three times.

**If a task seems to need running the server locally** (screenshot a UI change, smoke-test an
endpoint): don't. Verify via `go build`/`tsc -b`/`make test`/code review instead, or use the
Playwright E2E suite (`make e2e-ui`), which runs inside Docker -- a different risk profile,
not blocked by this rule. Surface it to the user as a real limitation rather than trying another
local-execution workaround.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-11* | *Last Reviewed: 2026-09-11*
