.PHONY: fmt vet test ui-dist compile-check test-hooks check-contexts check-attribution check-css check-contrast build deploy clean install-hook e2e e2e-sso e2e-edge e2e-ui help

VERSION ?= $(shell grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)

# Which gateway, status page and portal a build points at by default. These describe one
# deployment rather than the software, so they are not in the source tree (#1188) -- set
# them in your own environment to bake your deployment's values into your binaries:
#
#   LFT_DEFAULT_SERVER_URL=https://tunnel.example.com make build
#
# Unset is fully supported: the client then asks to be pointed at a gateway instead of
# guessing one, and the status-page and portal hints are simply omitted.
LFT_DEFAULT_SERVER_URL ?=
LFT_DEFAULT_STATUS_PAGE_URL ?=
LFT_DEFAULT_PORTAL_URL ?=
DEPLOYMENT_LDFLAGS = \
	-X lfr-tunnel/pkg/config.DefaultServerURL=$(LFT_DEFAULT_SERVER_URL) \
	-X lfr-tunnel/pkg/config.DefaultStatusPageURL=$(LFT_DEFAULT_STATUS_PAGE_URL) \
	-X lfr-tunnel/pkg/config.DefaultPortalURL=$(LFT_DEFAULT_PORTAL_URL)

# EDR-safe test execution directory (defaults to /private/tmp on macOS to match SentinelOne EDR whitelist, or /tmp / %TEMP% on Linux/Windows)
ifeq ($(OS),Windows_NT)
LFT_TEST_DIR ?= $(subst \,/,$(or $(TMPDIR),$(TEMP),$(TMP),/tmp))
else
LFT_TEST_DIR ?= $(shell [ -d /private/tmp ] && echo /private/tmp || echo /tmp)
endif
# := rather than ?=, because this is the variable the EDR whitelist actually depends on (#1335).
#
# The Go toolchain links every executable inside GOTMPDIR and only then moves it to the -o path,
# so GOTMPDIR -- not -o -- decides where an unsigned binary first appears on disk. With ?=, any
# inherited GOTMPDIR (a shell profile, direnv, an IDE terminal) silently won and the build left
# the whitelist while reporting nothing. LFT_TEST_DIR stays overridable on purpose; GOTMPDIR now
# always follows it.
export GOTMPDIR := $(LFT_TEST_DIR)
TEST_BINARY := $(LFT_TEST_DIR)/lfr-tunnel$(shell go env GOEXE)
# The ops tool is built to bin/ and run from there rather than through `go run` (#1333).
OPS_BINARY := bin/lfr-tunnel-ops$(shell go env GOEXE)

PKG ?= ./...
TEST_FLAGS ?=
# Compile-time flags for the test binary. TEST_FLAGS is passed to the binary at run
# time, so options that must be set when building -- notably -race -- belong here:
#   make test TEST_BUILD_FLAGS=-race
# The binary is still built to and executed from LFT_TEST_DIR, so this stays EDR-safe.
TEST_BUILD_FLAGS ?=


help:
	@echo "Liferay Tunnel Developer Commands:"
	@echo "  make fmt          - Format Go files using gofmt"
	@echo "  make vet          - Run go vet static analysis"
	@echo "  make test         - Run all unit tests (EDR safe via LFT_TEST_DIR=$(LFT_TEST_DIR))"
	@echo "  make e2e          - Run the Docker integration E2E tests"
	@echo "  make e2e-sso      - Run the SSO / Keycloak E2E integration tests"
	@echo "  make e2e-edge     - Run the multi-region edge E2E integration tests"
	@echo "  make e2e-ui       - Run the Playwright UI E2E integration tests"
	@echo "  make build        - Clean and build client and server binaries"
	@echo "  make deploy       - Cross-compile and deploy server binary to VPS"
	@echo "  make clean        - Delete build binaries"
	@echo "  make install-hook - Install the native Git pre-commit and pre-push hooks"
	@echo "  make help         - Show this help message"

fmt:
	gofmt -w .

vet:
	go vet ./...

# Branches whose work has landed (#1528). CONTRIBUTING.md has always said to delete them as they
# merge; nothing ran it, and the checkout reached 271 branches and 6 stale worktrees. Reports by
# default and fails only once the pile is over a threshold, so it nags when it matters. Refuses
# to touch master or checksums in code rather than in prose -- deleting checksums breaks the
# portal's checksum delivery silently.
check-branches:
	@./scripts/check-stale-branches.sh

prune-branches:
	@./scripts/check-stale-branches.sh --delete

# Ceiling on suppressed errcheck findings (#1331). Runs in CI's Lint & Format Check job as of
# #1498 -- until then it was wired nowhere, and the count drifted five over the ceiling without
# anything failing.
nolint-ratchet:
	@./scripts/check-nolint-ratchet.sh

# Asserts that the toolchain will really link inside the whitelisted directory, rather than
# assuming it (#1335). The macOS default is resolved by an existence test, so a missing
# /private/tmp would otherwise fall through to /tmp and compile outside the whitelist while
# reporting success -- the exact "looks configured, is not working" shape this repo keeps hitting.
edr-guard:
	@mkdir -p $(LFT_TEST_DIR)
	@if [ "$$(go env GOTMPDIR)" != "$(LFT_TEST_DIR)" ]; then \
		echo "EDR GUARD FAILED: go will link in [$$(go env GOTMPDIR)], not [$(LFT_TEST_DIR)]."; \
		echo "Every executable is written inside that directory before it is moved to -o,"; \
		echo "so building now would drop an unsigned binary outside the EDR whitelist."; \
		exit 1; \
	fi
	@if [ ! -d "$(LFT_TEST_DIR)" ]; then \
		echo "EDR GUARD FAILED: [$(LFT_TEST_DIR)] does not exist."; \
		exit 1; \
	fi
	@if [ "$$(uname)" = "Darwin" ] && [ -d /private/tmp ] && [ "$(LFT_TEST_DIR)" != "/private/tmp" ]; then \
		echo "EDR GUARD FAILED: on macOS the whitelist is the literal path /private/tmp,"; \
		echo "but LFT_TEST_DIR resolved to [$(LFT_TEST_DIR)]."; \
		echo "/tmp is a symlink to the same directory, but the whitelist matches on the path"; \
		echo "as written, so the two are not interchangeable here."; \
		exit 1; \
	fi

# The test loop below only ever compiles packages that have test files, because `go list` is
# filtered on TestGoFiles. A package with no _test.go never reaches `go test -c`, so its compile
# errors are invisible and `make test` exits 0 on a tree that does not build (#1556). Four
# packages are in that position today, `cmd/lfr-tunnel-ops` among them.
#
# `go build` with multiple packages compiles and DISCARDS the objects -- it writes no executable
# -- so this is a typecheck rather than a build step. It still links inside GOTMPDIR, which is
# why it depends on edr-guard rather than running as a bare recipe line.
compile-check: edr-guard
	@echo "Checking every package compiles, including those with no tests..."
	@go build ./...

test: compile-check
	@for pkg in $$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' $(PKG)); do \
		rm -f $(TEST_BINARY); \
		go test -c $(TEST_BUILD_FLAGS) -o $(TEST_BINARY) $$pkg || exit 1; \
		if [ -f $(TEST_BINARY) ]; then \
			(cd "$$(go list -f '{{.Dir}}' $$pkg | tr '\\' '/')" && $(TEST_BINARY) $(TEST_FLAGS)) || exit 1; \
		fi; \
	done
	@rm -f $(TEST_BINARY)

clean:
	rm -rf bin

# The UI build lives here on its own so `deploy` can invoke exactly this rule rather than
# depending on someone having run `make build` at some point in the past. That dependency is
# what shipped a 12-day-old portal v2 to production in #1632: ui-dist is gitignored and embedded
# via //go:embed, so the portal that ships is a function of when the operator last built, not of
# the commit being deployed.
ui-dist:
	@echo "Building UI..."
	@# The fallback used to name a pnpm store path with exact transitive versions in it --
	@# node_modules/.pnpm/vite@8.1.5_@types+node@24.13.3/... -- while ui/package.json declares
	@# "^8.1.1" and "^24.13.2". The next install resolving a new patch of either moved the
	@# directory and broke the fallback, and it only runs on machines without pnpm on PATH,
	@# which is exactly where nobody would notice (#1330).
	@#
	@# corepack ships with Node 16.9+ and is the supported way to get pnpm without installing
	@# it globally, so it covers the same case without pinning anything.
	@cd ui && \
	if command -v pnpm >/dev/null 2>&1; then \
		pnpm install && pnpm run build; \
	elif command -v corepack >/dev/null 2>&1; then \
		corepack pnpm install && corepack pnpm run build; \
	else \
		echo "Neither pnpm nor corepack found. Install pnpm, or Node 16.9+ for corepack."; \
		exit 1; \
	fi
	rm -rf pkg/server/ui-dist
	cp -r ui/dist pkg/server/ui-dist
	@# ui/dist has no .gitkeep, so the two lines above delete a TRACKED file on every build
	@# and leave it staged for whoever next runs `git commit -a` (#1511). Losing it means a
	@# fresh clone has no pkg/server/ui-dist/ at all and `//go:embed ui-dist/*` stops
	@# compiling -- and CI cannot catch that, because every job creates the directory itself
	@# before building.
	@#
	@# Restored here rather than by narrowing the rm to `ui-dist/*`, which spares the dotfile
	@# only because `*` does not match it in sh -- true by accident, and undone by the next
	@# person who tidies that line.
	touch pkg/server/ui-dist/.gitkeep

build: clean ui-dist
	mkdir -p bin
	go build -ldflags="-s -w $(DEPLOYMENT_LDFLAGS) -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunnel ./cmd/lfr-tunnel
	go build -ldflags="-s -w $(DEPLOYMENT_LDFLAGS) -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunneld ./cmd/lfr-tunneld

# Built and then executed, never `go run` (#1333). go run links the binary inside GOTMPDIR and
# executes it from there, which is precisely the unsigned-binary-in-a-temp-directory shape the
# local EDR quarantines -- and this one goes on to open SSH, AWS and Route53 connections, which
# is what makes it look malicious rather than merely unknown.
#
# The repo already forbade this pattern in three other places; only the Makefile disagreed.
deploy: edr-guard
	@go build -o $(OPS_BINARY) ./cmd/lfr-tunnel-ops
	@$(OPS_BINARY) deploy

e2e:
	@./scripts/run-e2e.sh standard

e2e-sso:
	@./scripts/run-e2e.sh sso

e2e-edge:
	@./scripts/run-e2e.sh edge

e2e-ui:
	@./scripts/run-e2e-ui.sh

# Installs hook SHIMS that exec the scripts in the working tree (#1425). It used to copy them, so
# every edit was inert until each person re-ran this -- silently, since a stale hook's output is
# indistinguishable from a current one. The attribution guard added in #1384 ran for nobody who
# had not reinstalled since.
#
# pre-commit is the fast, irreversible-only set; pre-push carries vet, tests and the conditional
# UI build (#1343). They go together: installing one leaves a gap rather than a slow hook.
#
# The destination is resolved with `git rev-parse --git-path hooks` rather than hardcoded to
# `.git/hooks` (#1377). In a linked worktree `.git` is a FILE, not a directory, so the old
# hardcoded path failed outright with "cp: .git/hooks/pre-commit: Not a directory" -- leaving a
# worktree with NO hooks installed, which is worse than a slow one. Hooks live in the common
# gitdir and are shared across worktrees, which is what rev-parse resolves to.
HOOKS_DIR = $(shell git rev-parse --git-path hooks)

install-hook:
	@./scripts/install-hook-shim.sh

# Tests the hook and guard scripts themselves (#1377, #1395, #1402). Fast: the stubbed cases
# need no Docker, and only the end-to-end cases do. Not part of `test`, which is the Go suite.
test-hooks:
	@./tests/hooks/test-scan-staged-secrets.sh
	@./tests/hooks/test-edr-guard.sh
	@./tests/hooks/test-shell-portability.sh
	@./tests/hooks/test-drain-and-wait.sh
	@./tests/hooks/test-check-staged-prettier.sh
	@./tests/hooks/test-hook-shim.sh
	@./tests/hooks/test-build-keeps-tracked-files.sh
	@./tests/hooks/test-stale-branches.sh
	@./tests/hooks/test-pull-images.sh
	@./tests/hooks/test-closing-refs.sh
	@./tests/hooks/test-compile-check.sh
	@./tests/hooks/test-install-paths.sh
	@./tests/hooks/test-e2e-teardown.sh
	@./tests/hooks/test-power-hook-credentials.sh
	@./tests/hooks/test-ci-hook-gate.sh
	@./tests/hooks/test-coverage-signal.sh

# The pre-merge CI-configuration gate (#1391). Worth a target rather than only a path to type:
# the whole point of this check is being run BEFORE pushing, and a check nobody can invoke
# conveniently is a check nobody invokes. Also makes the script reachable from the Makefile,
# which is how tests/hooks/test-shell-portability.sh discovers what must stay bash 3.2 clean.
check-contexts:
	@./scripts/check-required-contexts.sh

# Same reasoning as check-contexts above: a gate with no convenient invocation is a gate
# nobody runs, and being make-reachable is what puts it in the bash 3.2 portable set.
check-attribution:
	@./scripts/check-commit-attribution.sh

# Catches a Portal V2 BEM modifier class that is used but has no CSS rule (#1383). Node
# rather than shell because it has to parse both TSX and CSS; the bash 3.2 rule in
# AGENTS.md covers .sh files, so this one is outside that constraint by construction.
check-css:
	@node scripts/check-css-modifiers.cjs

# Checks every theme's danger colours against WCAG AA (#1458). Discovers theme files
# rather than listing them, so a theme added later is covered without touching this.
check-contrast:
	@node scripts/check-theme-contrast.cjs

