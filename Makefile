.PHONY: fmt vet test build deploy clean install-hook e2e e2e-sso help

VERSION ?= $(shell git describe --tags --abbrev=0 --dirty 2>/dev/null || git describe --always --dirty 2>/dev/null || echo "dev")


help:
	@echo "Liferay Tunnel Developer Commands:"
	@echo "  make fmt          - Format Go files using gofmt"
	@echo "  make vet          - Run go vet static analysis"
	@echo "  make test         - Run all unit tests"
	@echo "  make e2e          - Run the Docker integration E2E tests"
	@echo "  make e2e-sso      - Run the SSO / Keycloak E2E integration tests"
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
	TMPDIR=/private/tmp go test -v $$(go list ./... | grep -v /pkg/server)

clean:
	rm -rf bin

build: clean
	mkdir -p bin
	go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunnel ./cmd/lfr-tunnel
	go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$(VERSION)" -trimpath -o bin/lfr-tunneld ./cmd/lfr-tunneld

deploy:
	@./scripts/deploy.sh

e2e:
	@./scripts/run-e2e.sh standard

e2e-sso:
	@./scripts/run-e2e.sh sso

install-hook:
	@echo "Installing native git pre-commit hook..."
	@cp scripts/pre-commit-hook.sh .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

