.PHONY: fmt vet test build deploy clean install-hook e2e e2e-sso e2e-ui help

VERSION ?= $(shell grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)

# EDR-safe test execution directory (defaults to $(HOME)/runningpoc/bin to match SentinelOne EDR whitelist)
LFT_TEST_DIR ?= $(HOME)/runningpoc/bin
export GOTMPDIR ?= $(LFT_TEST_DIR)
TEST_BINARY := $(LFT_TEST_DIR)/lfr-tunnel


help:
	@echo "Liferay Tunnel Developer Commands:"
	@echo "  make fmt          - Format Go files using gofmt"
	@echo "  make vet          - Run go vet static analysis"
	@echo "  make test         - Run all unit tests (EDR safe via LFT_TEST_DIR=$(LFT_TEST_DIR))"
	@echo "  make e2e          - Run the Docker integration E2E tests"
	@echo "  make e2e-sso      - Run the SSO / Keycloak E2E integration tests"
	@echo "  make e2e-ui       - Run the Playwright UI E2E integration tests"
	@echo "  make build        - Clean and build client and server binaries"
	@echo "  make deploy       - Cross-compile and deploy server binary to VPS"
	@echo "  make clean        - Delete build binaries"
	@echo "  make install-hook - Install the native Git secrets pre-commit hook"
	@echo "  make help         - Show this help message"

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	@mkdir -p $(LFT_TEST_DIR)
	@for pkg in $$(go list -f '{{if .TestGoFiles}}{{.ImportPath}}{{end}}' ./...); do \
		rm -f $(TEST_BINARY); \
		go test -c -o $(TEST_BINARY) $$pkg || exit 1; \
		if [ -f $(TEST_BINARY) ]; then \
			(cd $$(go list -f '{{.Dir}}' $$pkg) && $(TEST_BINARY)) || exit 1; \
		fi; \
	done
	@rm -f $(TEST_BINARY)

clean:
	rm -rf bin

build: clean
	mkdir -p bin
	@echo "Building UI..."
	cd ui && pnpm install && pnpm run build
	rm -rf pkg/server/ui-dist
	cp -r ui/dist pkg/server/ui-dist
	go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunnel ./cmd/lfr-tunnel
	go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunneld ./cmd/lfr-tunneld

deploy:
	@go run ./cmd/lfr-tunnel-ops deploy

e2e:
	@./scripts/run-e2e.sh standard

e2e-sso:
	@./scripts/run-e2e.sh sso

e2e-ui:
	@./scripts/run-e2e-ui.sh

install-hook:
	@echo "Installing native git pre-commit hook..."
	@cp scripts/pre-commit-hook.sh .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

