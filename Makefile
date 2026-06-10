.PHONY: fmt vet test build help

help:
	@echo "Liferay Tunnel Developer Commands:"
	@echo "  make fmt        - Format Go files using gofmt"
	@echo "  make vet        - Run go vet static analysis"
	@echo "  make test       - Run all unit tests"
	@echo "  make build      - Build client and server binaries"
	@echo "  make help       - Show this help message"

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test -v ./...

build:
	mkdir -p bin
	go build -o bin/lfr-tunnel ./cmd/lfr-tunnel
	go build -o bin/lfr-tunneld ./cmd/lfr-tunneld
