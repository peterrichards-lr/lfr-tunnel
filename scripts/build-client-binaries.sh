#!/usr/bin/env bash
set -e

VERSION="${VERSION:-$(git describe --tags --abbrev=0 --dirty 2>/dev/null || git describe --always --dirty 2>/dev/null || echo "dev")}"

# Linux
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-linux-amd64 ./cmd/lfr-tunnel
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-linux-arm64 ./cmd/lfr-tunnel

# macOS (Darwin)
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-darwin-amd64 ./cmd/lfr-tunnel
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-darwin-arm64 ./cmd/lfr-tunnel

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-windows-amd64.exe ./cmd/lfr-tunnel

echo "Build complete!"
