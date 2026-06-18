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
INSTALL_DIR="${HOME}/bin"
INSTALL_PATH="${INSTALL_DIR}/lfr-tunnel"

echo "Downloading lfr-tunnel for ${OS}-${ARCH}..."
curl -sSfL "$URL" -o /tmp/lfr-tunnel-download
chmod +x /tmp/lfr-tunnel-download

# Always install to ~/bin — the single canonical location
mkdir -p "$INSTALL_DIR"
mv /tmp/lfr-tunnel-download "$INSTALL_PATH"
echo "lfr-tunnel installed to ${INSTALL_PATH}"

# Advise on PATH if ~/bin is not already present
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    echo ""
    echo "  NOTE: ${INSTALL_DIR} is not yet in your PATH."
    echo "  Add the following line to your shell profile (~/.zshrc, ~/.bashrc, etc.):"
    echo ""
    echo '    export PATH="$HOME/bin:$PATH"'
    echo ""
    echo "  Then run: source ~/.zshrc  (or open a new terminal)"
    ;;
esac
