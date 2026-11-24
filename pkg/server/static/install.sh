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

# Server URL injected by the gateway that served this script.
#
# Matched on the BRACES, not against the placeholder's own text (#1684). The gateway substitutes
# with strings.ReplaceAll, so a comparison written as `= "{{SERVER_URL}}"` has its right-hand
# side replaced too -- both sides then hold the gateway URL, the guard is always true, and the
# fallback below fired on every run. Every user of the documented one-liner was silently pointed
# at a host that does not serve the binaries.
#
# No hardcoded fallback either. A script that does not know its gateway cannot guess one, and
# baking a single deployment's hostname into shared source is how the wrong host got here.
SERVER_URL="{{SERVER_URL}}"
case "$SERVER_URL" in
  ""|*"{{"*)
    echo "This installer does not know which gateway to download from." >&2
    echo "It was not served by one -- fetch it from your gateway instead:" >&2
    echo "  curl -fsSL https://<your-gateway>/install.sh | sh" >&2
    exit 1
    ;;
esac

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

# Ensure the target installation directory exists
if [ ! -d "$INSTALL_DIR" ]; then
  if mkdir -p "$INSTALL_DIR" 2>/dev/null; then
    :
  else
    echo "Requesting sudo permissions to create directory ${INSTALL_DIR}..."
    sudo mkdir -p "$INSTALL_DIR"
  fi
fi

# sudo only where the directory is not ours to write.
SUDO=""
if [ ! -w "$INSTALL_DIR" ]; then
  echo "Requesting sudo permissions to install to ${INSTALL_DIR}..."
  SUDO="sudo"
fi

# Straight to the install path, never through /tmp (#2505). The EDR exclusions cover this path,
# and a freshly downloaded executable in /tmp is what gets quarantined before any move happens.
# The current binary is renamed aside first -- renaming a running binary is safe -- and put back
# if the download fails, so a failed install leaves the previous version exactly as it was.
OLD_PATH="${INSTALL_PATH}.old"
$SUDO rm -f "$OLD_PATH"
if [ -e "$INSTALL_PATH" ]; then
  $SUDO mv "$INSTALL_PATH" "$OLD_PATH"
fi

echo "Downloading lfr-tunnel for ${OS}-${ARCH}..."
if $SUDO curl -sSfL "$URL" -o "$INSTALL_PATH" && [ -s "$INSTALL_PATH" ]; then
  $SUDO chmod +x "$INSTALL_PATH"
  $SUDO rm -f "$OLD_PATH"
else
  $SUDO rm -f "$INSTALL_PATH"
  if [ -e "$OLD_PATH" ]; then
    $SUDO mv "$OLD_PATH" "$INSTALL_PATH"
    echo "Download failed; the previous lfr-tunnel at ${INSTALL_PATH} is unchanged." >&2
  else
    echo "Download failed; nothing was installed." >&2
  fi
  exit 1
fi
echo "lfr-tunnel installed to ${INSTALL_PATH}"

# The ~/.ldm/bin link is retired (#2579): LDM 2.25.0 and later find the client through
# LDM_LFR_TUNNEL_BIN below or the install path itself, and the link only cost a second EDR rule.
# The link an earlier install made is removed: a symlink naming this install, or naming nothing.
# Anything else there is the user's and is left alone.
LDM_LINK="${HOME}/.ldm/bin/lfr-tunnel"
if [ -L "$LDM_LINK" ]; then
  if [ "$(readlink "$LDM_LINK")" = "$INSTALL_PATH" ] || [ ! -e "$LDM_LINK" ]; then
    rm -f "$LDM_LINK" 2>/dev/null && echo "Removed the retired LDM link ${LDM_LINK}."
  fi
fi

# zsh is macOS's default shell, and a new Mac has no ~/.zshrc. Without one the loop below would
# write nothing anywhere zsh reads, and `lfr-tunnel` would be "command not found" (#2584).
if [ "$OS" = "darwin" ] && [ ! -e "${HOME}/.zshrc" ]; then
  touch "${HOME}/.zshrc" 2>/dev/null || true
fi

# Whether the install directory is already on this shell's PATH (set some other way).
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ON_PATH=1 ;;
  *) ON_PATH=0 ;;
esac

# Persist LDM_LFR_TUNNEL_BIN for LDM auto-discovery (#1311), and put the client on PATH (#2584).
# The PATH line is written in exactly this form so an upgrade that moves the client can find and
# re-point it (pkg/client/install_env.go). It is skipped when the directory is already on PATH, and
# never written twice.
PATH_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""
PATH_RCS=""
for rc in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.bash_profile"; do
  if [ -f "$rc" ] && [ -w "$rc" ]; then
    if ! grep -q "LDM_LFR_TUNNEL_BIN" "$rc" 2>/dev/null; then
      echo "export LDM_LFR_TUNNEL_BIN=\"$INSTALL_PATH\"" >> "$rc"
    fi
    if [ "$ON_PATH" = 0 ] && ! grep -qxF "$PATH_LINE" "$rc" 2>/dev/null; then
      echo "$PATH_LINE" >> "$rc"
      PATH_RCS="${PATH_RCS} ${rc}"
    fi
  fi
done
export LDM_LFR_TUNNEL_BIN="$INSTALL_PATH"

if [ "$ON_PATH" = 0 ]; then
  echo ""
  if [ -n "$PATH_RCS" ]; then
    echo "  Added ${INSTALL_DIR} to your PATH in:${PATH_RCS}"
    echo "  New terminals pick it up. To use lfr-tunnel in THIS terminal now, run:"
  else
    echo "  NOTE: ${INSTALL_DIR} is not yet in your PATH, and no shell profile could be updated."
    echo "  Add this line to your shell profile, and run it now:"
  fi
  echo ""
  echo "    ${PATH_LINE}"
  echo ""
fi

