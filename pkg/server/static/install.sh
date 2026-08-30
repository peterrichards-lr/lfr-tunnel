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
  SERVER_URL="https://lfr-demo.se"
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
  # The path agreed with the S1 team (#1591). The EDR exclusion is the wildcard
  # */liferay/lfr-tunnel/lfr-tunnel, so the last two directory segments and the binary name are
  # all load-bearing -- installing anywhere else leaves the client unexcluded and quarantined.
  ""|\{\{*) DEFAULT_INSTALL_DIR="${HOME}/liferay/lfr-tunnel" ;;
esac

INSTALL_DIR="${LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR:-${LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR:-${LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR:-${LFR_TUNNEL_INSTALL_DIR:-${LFT_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}}}}}"

# Expand a leading tilde ourselves. The gateway templates this value in as "~/liferay/lfr-tunnel",
# and a tilde inside a quoted variable is NOT expanded by the shell -- `mkdir -p "$INSTALL_DIR"`
# would create a directory literally named "~" in whatever directory the installer happened to be
# run from, and install the client there rather than in the user's home folder.
# shellcheck disable=SC2088  # the literal tilde is the point: these are case patterns matching
# a value that arrives unexpanded from the gateway, which is the bug being corrected here.
case "$INSTALL_DIR" in
  "~") INSTALL_DIR="$HOME" ;;
  "~/"*) INSTALL_DIR="${HOME}/${INSTALL_DIR#\~/}" ;;
esac

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

# LDM (liferay-docker-manager) auto-discovery integration (#1311)
mkdir -p "${HOME}/.ldm/bin" 2>/dev/null || true
ln -sf "$INSTALL_PATH" "${HOME}/.ldm/bin/lfr-tunnel" 2>/dev/null || true

# Persist LDM_LFR_TUNNEL_BIN environment variable for LDM auto-discovery (#1311)
for rc in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.bash_profile"; do
  if [ -f "$rc" ] && [ -w "$rc" ]; then
    if ! grep -q "LDM_LFR_TUNNEL_BIN" "$rc" 2>/dev/null; then
      echo "export LDM_LFR_TUNNEL_BIN=\"$INSTALL_PATH\"" >> "$rc"
    fi
  fi
done
export LDM_LFR_TUNNEL_BIN="$INSTALL_PATH"

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

