#!/usr/bin/env bash
set -e

VERSION="${VERSION:-$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)}"

# Linux
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-linux-amd64 ./cmd/lfr-tunnel
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-linux-arm64 ./cmd/lfr-tunnel

# macOS (Darwin)
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-darwin-amd64 ./cmd/lfr-tunnel
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-darwin-arm64 ./cmd/lfr-tunnel

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o dist/lfr-tunnel-windows-amd64.exe ./cmd/lfr-tunnel

echo "Build complete!"
