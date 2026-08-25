# Agent rules

- **NEVER run a bare `go test` in this repo. ALWAYS use `make test` instead.** This is a hard, non-negotiable rule, not a preference.
  - Why: **`GOTMPDIR` is the control, not `-o`** (#1337). The Go toolchain links every executable inside `GOTMPDIR` — or the system temp dir (`/private/var/folders/...` on macOS) when that is unset — and only *then* moves it to the `-o` path. So `-o` decides where a binary ends up; `GOTMPDIR` decides where it first exists, and an EDR watching temp directories only sees the second. Measured:
    ```
    $ GOTMPDIR=$WATCHED go build -o $OUT/bin ./cmd/lfr-tunnel-ops
    $WATCHED/go-build2292438673/b001/exe/a.out     <-- the complete linked binary, despite -o
    ```
    Many packages' tests then open real network listeners/sockets (e.g. `pkg/server`'s tests use `httptest.NewServer` and real WebSocket connections; `cmd/lfr-tunnel/main_test.go`'s `TestMain_ValidationFailure` even re-execs itself into the real client `main()`). An unsigned freshly-built binary, sitting in an arbitrary temp path, that then opens network connections is exactly the pattern endpoint protection (SentinelOne) flags as a dropped malicious binary — it does NOT matter whether the package under test is the client, the server, or anything else.
  - This already happened once (`server.test`, flagged "Malicious file executed", 2026-07-28) and cost a full local environment reinstall — S1 terminated the session and deleted unrelated tooling (Claude, jenv, python, LDM, Homebrew) as collateral damage.
  - The **only** safe way to run tests is `make test` — it **exports `GOTMPDIR`** as `LFT_TEST_DIR` (default `/private/tmp`, S1-whitelisted), asserts that the toolchain really resolved it (`make edr-guard`), and builds and executes there. The `-o` on that line only names the destination; it is the export that keeps the binary out of `/var/folders`. Enforced by a deny rule in `.claude/settings.json` blocking any `Bash(go test*)` invocation outright — do not try to work around it.
  - Adding `-o` to a `go` command does **not** make it safe. Any command that links or runs Go code needs `GOTMPDIR` pointed inside the whitelist — which in practice means going through `make`. `go run` is worse than `go test -c`, because it also *executes* from that directory.
  - Also never run the `lfr-tunnel` **client** binary/process directly on the host: `go run ./cmd/lfr-tunnel`, the built `bin/lfr-tunnel` binary, or the `lfr-tunnel.sh`/`lfr-tunnel.bat` wrappers. Also denied in `.claude/settings.json`.
  - Fine to run directly: `go build` (compiles but doesn't execute), `go vet`, `gofmt`, `go list`, and the `lfr-tunnel-ops` (deploy tooling) binary. **Not** `lfr-tunneld` — see below.
  - The client running inside a Docker container (e.g. `make e2e` / `tests/e2e/run.sh`) is a different risk profile and is not blocked.
  - If ever unsure whether a command would build-and-run code outside `LFT_TEST_DIR`, stop and ask the user first rather than guessing.
  - **There is no verified-safe way to run `lfr-tunneld` locally, at all, as of 2026-08-13.** Three incidents in a row: a bare `go test` binary (2026-07-28), `lfr-tunneld` built to a manual path under `/private/tmp` (wrong assumption — the whitelist is the *exact literal path* `make test` uses, not the directory tree), and then `lfr-tunneld` built via `make build` to `bin/lfr-tunneld` and run from the repo root — the exact pattern this file previously called "fine to run directly." That got killed too. If a task seems to need running the server locally (e.g. to check a UI change), don't — verify via `go build`/`tsc -b`/`make test`/code review instead, or use the Docker-based `make e2e-ui`. Bring it to the user as a real limitation rather than inventing another local-execution path.

- **`golangci-lint` is intentionally NOT installed locally via `brew`/`go install` — it is not "missing," this is by design.** This repo's own `scripts/pre-commit-hook.sh` runs it containerized:
  ```
  docker run --rm -v "$(pwd)":/app -w /app golangci/golangci-lint:latest golangci-lint run
  ```
  Always use that Docker invocation to lint locally (matches the CI job and the Docker carve-out above). Do NOT `brew install golangci-lint` or `go install github.com/golangci/golangci-lint/...` to "fix" its absence — that would add an unnecessary local binary and deviate from how this project actually runs it.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-08-25* | *Last Reviewed: 2026-08-25*
