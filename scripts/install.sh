#!/bin/sh
set -e

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin)  OS="darwin" ;;
  linux)   OS="linux" ;;
  *)       echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect Architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)            echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

BINARY="lfr-tunnel-${OS}-${ARCH}"
URL="https://github.com/peterrichards-lr/lfr-tunnel/releases/latest/download/${BINARY}"

echo "Downloading lfr-tunnel for ${OS}-${ARCH}..."
curl -sSfL "$URL" -o lfr-tunnel

chmod +x lfr-tunnel

# Install to /usr/local/bin if directory is writeable, otherwise to ./bin or warn
if [ -w /usr/local/bin ]; then
  mv lfr-tunnel /usr/local/bin/lfr-tunnel
  echo "lfr-tunnel installed successfully to /usr/local/bin/lfr-tunnel"
else
  mkdir -p ./bin
  mv lfr-tunnel ./bin/lfr-tunnel
  echo "lfr-tunnel installed successfully to ./bin/lfr-tunnel"
  echo "Please add $(pwd)/bin to your PATH or copy the binary to a directory in your PATH (e.g. /usr/local/bin)."
fi
