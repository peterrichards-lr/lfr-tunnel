.PHONY: fmt vet test build deploy clean install-hook e2e e2e-sso e2e-edge e2e-ui help

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

# Ceiling on suppressed errcheck findings (#1331). Not wired into CI here:
# .github/workflows/ci.yml is another agent's territory under #1328, so wiring belongs with
# whoever holds it. Runnable now, and by the pre-commit hook.
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

test: edr-guard
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

build: clean
	mkdir -p bin
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

# Installs both hooks (#1343). pre-commit is the fast, irreversible-only set; pre-push carries
# vet, tests and the conditional UI build. Installing only one of the two leaves a gap rather
# than a slow hook, so they go together.
install-hook:
	@echo "Installing native git hooks..."
	@cp scripts/pre-commit-hook.sh .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@cp scripts/pre-push-hook.sh .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "pre-commit and pre-push hooks installed successfully."

