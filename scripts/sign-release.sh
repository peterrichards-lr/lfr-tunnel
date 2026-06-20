#!/bin/bash
# scripts/sign-release.sh
# Automates multi-platform client binary signing for macOS, Windows, and Linux.

set -e

# Get project root directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$PROJECT_ROOT/dist"

# Exit codes definition:
# 0: Success
# 1: General setup / build error
# 2: Required tool missing (e.g. codesign, osslsigncode, gpg, or op)
# 3: 1Password CLI retrieval failure
# 4: Binary signing execution failure
# 5: Checksum post-processing failure

# 1. Build binaries if they don't exist
if [ ! -d "$BIN_DIR" ] || [ ! -f "$BIN_DIR/lfr-tunnel-darwin-arm64" ] || [ ! -f "$BIN_DIR/lfr-tunnel-windows-amd64.exe" ]; then
    echo "Client binaries not found under $BIN_DIR. Building binaries first..."
    make -C "$PROJECT_ROOT" build || {
        echo "ERROR: Failed to build client binaries using make." >&2
        exit 1
    }
fi

# 2. Setup/Prompt for macOS Certificate Identity
if [ -z "$LFT_MACOS_IDENTITY" ]; then
    if [ -t 0 ]; then
        echo "No LFT_MACOS_IDENTITY environment variable found."
        echo "Available codesigning identities in your login keychain:"
        security find-identity -v -p codesigning | grep -E "Developer ID Application|Temp Code Sign" || echo "  (None found)"
        echo ""
        read -p "Enter macOS Codesigning Identity (or leave empty to skip macOS signing): " LFT_MACOS_IDENTITY
    else
        echo "No LFT_MACOS_IDENTITY environment variable found and stdin is not a TTY. Skipping macOS identity prompt."
    fi
fi

# 3. Setup/Prompt for Windows P12 Certificate
if [ -z "$LFT_SIGN_P12" ]; then
    if [ -f "$PROJECT_ROOT/temp_signing_key.p12" ]; then
        LFT_SIGN_P12="$PROJECT_ROOT/temp_signing_key.p12"
        echo "Found default temporary certificate: $LFT_SIGN_P12"
    elif [ -t 0 ]; then
        read -p "Enter path to Windows P12 certificate (or leave empty to skip Windows signing): " LFT_SIGN_P12
    else
        echo "No Windows P12 certificate path found and stdin is not a TTY. Skipping Windows path prompt."
    fi
fi

# Prompt for Windows P12 password if path is set but password is empty
if [ -n "$LFT_SIGN_P12" ] && [ "$LFT_SIGN_P12" != "skip" ] && [ -z "$LFT_SIGN_PASS" ]; then
    if [ -t 0 ]; then
        read -s -p "Enter password for Windows P12 certificate: " LFT_SIGN_PASS
        echo ""
    else
        echo "Windows P12 certificate path is specified but password is empty and stdin is not a TTY. Skipping Windows password prompt."
    fi
fi

# 4. Setup GPG Key for Linux
if [ -z "$LFT_GPG_KEY" ]; then
    if [ -t 0 ]; then
        read -p "Enter GPG key ID/email for Linux binary signing (or leave empty for default GPG key): " LFT_GPG_KEY
    else
        echo "No GPG key ID provided and stdin is not a TTY. Skipping Linux GPG key prompt."
    fi
fi

# Retrieve credentials from 1Password if references are provided
if [[ "$LFT_SIGN_PASS" == op://* ]]; then
    if ! command -v op &> /dev/null; then
        echo "ERROR: 1Password CLI (op) not found but LFT_SIGN_PASS op:// reference was provided." >&2
        exit 2
    fi
    echo "Fetching password from 1Password..."
    LFT_SIGN_PASS=$(op read "$LFT_SIGN_PASS") || {
        echo "ERROR: Failed to retrieve password from 1Password using reference: $LFT_SIGN_PASS" >&2
        exit 3
    }
fi

if [ -n "$LFT_SIGN_P12" ] && [ "$LFT_SIGN_P12" != "skip" ] && [ ! -f "$LFT_SIGN_P12" ]; then
    if ! command -v op &> /dev/null; then
        echo "ERROR: File '$LFT_SIGN_P12' not found locally, and 1Password CLI (op) is not installed." >&2
        exit 2
    fi
    echo "Fetching certificate document from 1Password..."
    TEMP_P12="/tmp/lfr-tunnel-windows-$(date +%s).p12"
    if [[ "$LFT_SIGN_P12" == op://* ]]; then
        if ! op read "$LFT_SIGN_P12" --out-file "$TEMP_P12" &>/dev/null && ! op document get "$LFT_SIGN_P12" --output "$TEMP_P12" &>/dev/null; then
            echo "ERROR: Failed to retrieve certificate document from 1Password using reference: $LFT_SIGN_P12" >&2
            exit 3
        fi
    else
        # Try treating it as a document name directly
        if ! op document get "$LFT_SIGN_P12" --out-file "$TEMP_P12" &>/dev/null; then
            echo "ERROR: Failed to retrieve 1Password document named: $LFT_SIGN_P12" >&2
            exit 3
        fi
    fi
    LFT_SIGN_P12="$TEMP_P12"
    # Ensure temporary file is cleaned up on exit
    trap 'echo "Cleaning up temporary certificate file..."; rm -f "$TEMP_P12"' EXIT
fi


echo ""
echo "=== Beginning Signing Process ==="

# macOS Signing
if [ -n "$LFT_MACOS_IDENTITY" ] && [ "$LFT_MACOS_IDENTITY" != "skip" ]; then
    echo "Signing macOS binaries..."
    codesign --force --options runtime --sign "$LFT_MACOS_IDENTITY" "$BIN_DIR/lfr-tunnel-darwin-arm64" || {
        echo "ERROR: macOS arm64 codesign failed." >&2
        exit 4
    }
    codesign --force --options runtime --sign "$LFT_MACOS_IDENTITY" "$BIN_DIR/lfr-tunnel-darwin-amd64" || {
        echo "ERROR: macOS amd64 codesign failed." >&2
        exit 4
    }
    echo "macOS binaries successfully signed!"
else
    echo "Skipping macOS codesigning (no identity provided or skipped)."
fi

# Windows Signing
if [ -n "$LFT_SIGN_P12" ] && [ "$LFT_SIGN_P12" != "skip" ] && [ -f "$LFT_SIGN_P12" ]; then
    if command -v osslsigncode &> /dev/null; then
        echo "Signing Windows binary..."
        # Create a temp signed file then overwrite to make it atomic
        osslsigncode sign \
          -pkcs12 "$LFT_SIGN_P12" \
          -pass "$LFT_SIGN_PASS" \
          -n "Liferay Tunnel" \
          -i "https://github.com/peterrichards-lr/lfr-tunnel" \
          -in "$BIN_DIR/lfr-tunnel-windows-amd64.exe" \
          -out "$BIN_DIR/lfr-tunnel-windows-amd64-signed.exe" || {
              echo "ERROR: Windows binary signing failed." >&2
              exit 4
          }
        mv "$BIN_DIR/lfr-tunnel-windows-amd64-signed.exe" "$BIN_DIR/lfr-tunnel-windows-amd64.exe"
        echo "Windows binary successfully signed!"
    else
        echo "ERROR: 'osslsigncode' tool not found but Windows signing was requested." >&2
        exit 2
    fi
else
    echo "Skipping Windows signing (no certificate provided/found or skipped)."
fi

# Linux GPG Signing
if [ "$LFT_SKIP_GPG" != "true" ] && [ "$LFT_GPG_KEY" != "skip" ]; then
    if command -v gpg &> /dev/null; then
        echo "Generating Linux detached GPG signature..."
        rm -f "$BIN_DIR/lfr-tunnel-linux-amd64.asc"
        if [ -n "$LFT_GPG_KEY" ]; then
            gpg --yes --local-user "$LFT_GPG_KEY" --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64" || {
                echo "ERROR: Linux GPG signing failed." >&2
                exit 4
            }
        else
            gpg --yes --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64" || {
                echo "ERROR: Linux GPG signing failed with default key." >&2
                exit 4
            }
        fi
        echo "Linux detached GPG signature successfully created!"
    else
        echo "WARNING: gpg command not found but Linux GPG signing was requested. Skipping." >&2
    fi
else
    echo "Skipping Linux GPG signing."
fi

# 5. Re-generate Checksums
echo "Updating checksums.txt..."
cd "$BIN_DIR"
# Exclude checksums.txt and signature files from checksum list itself
find . -type f ! -name "checksums.txt" ! -name "*.asc" | sed 's|^\./||' | sort | xargs shasum -a 256 > checksums.txt || {
    echo "ERROR: Failed to generate checksums." >&2
    exit 5
}
echo "Checksums updated in $BIN_DIR/checksums.txt"

echo "=== Client Signing Complete! ==="
