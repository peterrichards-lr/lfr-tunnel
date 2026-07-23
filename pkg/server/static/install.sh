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

# Server URL injected by Gateway
SERVER_URL="{{SERVER_URL}}"
if [ -z "$SERVER_URL" ] || [ "$SERVER_URL" = "{{SERVER_URL}}" ]; then
  SERVER_URL="https://tunnel.lfr-demo.se"
fi

BINARY="lfr-tunnel-${OS}-${ARCH}"
URL="${SERVER_URL}/static/downloads/${BINARY}"

case "$OS" in
  darwin)
    case "$ARCH" in
      amd64) DEFAULT_INSTALL_DIR="{{LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR}}" ;;
      arm64) DEFAULT_INSTALL_DIR="{{LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR}}" ;;
    esac
    ;;
  linux)
    case "$ARCH" in
      amd64) DEFAULT_INSTALL_DIR="{{LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR}}" ;;
    esac
    ;;
esac

# Fallback if templating failed or script was executed directly from raw source
case "$DEFAULT_INSTALL_DIR" in
  ""|\{\{*) DEFAULT_INSTALL_DIR="${HOME}/runningpoc/bin" ;;
esac

INSTALL_DIR="${LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR:-${LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR:-${LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR:-${LFR_TUNNEL_INSTALL_DIR:-${LFT_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}}}}}"
INSTALL_PATH="${INSTALL_DIR}/lfr-tunnel"

echo "Downloading lfr-tunnel for ${OS}-${ARCH}..."
curl -sSfL "$URL" -o /tmp/lfr-tunnel
chmod +x /tmp/lfr-tunnel

# Ensure the target installation directory exists
if [ ! -d "$INSTALL_DIR" ]; then
  if mkdir -p "$INSTALL_DIR" 2>/dev/null; then
    :
  else
    echo "Requesting sudo permissions to create directory ${INSTALL_DIR}..."
    sudo mkdir -p "$INSTALL_DIR"
  fi
fi

# Move binary to target path, using sudo if write permissions are missing
if [ -w "$INSTALL_DIR" ]; then
  mv /tmp/lfr-tunnel "$INSTALL_PATH"
else
  echo "Requesting sudo permissions to install to ${INSTALL_DIR}..."
  sudo mv /tmp/lfr-tunnel "$INSTALL_PATH"
fi
echo "lfr-tunnel installed to ${INSTALL_PATH}"

# Advise on PATH if the target directory is not already present
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    echo ""
    echo "  NOTE: ${INSTALL_DIR} is not yet in your PATH."
    echo "  Add the following line to your shell profile (~/.zshrc, ~/.bashrc, etc.):"
    echo ""
    echo "    export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
    echo "  Then run: source ~/.zshrc  (or open a new terminal)"
    ;;
esac

