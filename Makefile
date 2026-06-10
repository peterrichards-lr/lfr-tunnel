.PHONY: fmt vet test build clean install-hook help

help:
	@echo "Liferay Tunnel Developer Commands:"
	@echo "  make fmt          - Format Go files using gofmt"
	@echo "  make vet          - Run go vet static analysis"
	@echo "  make test         - Run all unit tests"
	@echo "  make build        - Clean and build client and server binaries"
	@echo "  make clean        - Delete build binaries"
	@echo "  make install-hook - Install the native Git secrets pre-commit hook"
	@echo "  make help         - Show this help message"

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test -v ./...

clean:
	rm -rf bin

build: clean
	mkdir -p bin
	go build -o bin/lfr-tunnel ./cmd/lfr-tunnel
	go build -o bin/lfr-tunneld ./cmd/lfr-tunneld

install-hook:
	@echo "Installing native git pre-commit hook..."
	@cp scripts/pre-commit-hook.sh .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

