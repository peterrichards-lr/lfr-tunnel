#!/bin/bash
# scripts/sign-release.sh
# Automates multi-platform client binary signing for macOS, Windows, and Linux.

set -e

# Get project root directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$PROJECT_ROOT/dist"

echo "=== Liferay Tunnel Client Signing Pipeline ==="

# 1. Build binaries if they don't exist
if [ ! -d "$BIN_DIR" ] || [ ! -f "$BIN_DIR/lfr-tunnel-darwin-arm64" ] || [ ! -f "$BIN_DIR/lfr-tunnel-windows-amd64.exe" ]; then
    echo "Client binaries not found under $BIN_DIR. Building binaries first..."
    make -C "$PROJECT_ROOT" build
fi

# 2. Setup/Prompt for macOS Certificate Identity
if [ -z "$LFT_MACOS_IDENTITY" ]; then
    echo "No LFT_MACOS_IDENTITY environment variable found."
    echo "Available codesigning identities in your login keychain:"
    security find-identity -v -p codesigning | grep -E "Developer ID Application|Temp Code Sign" || echo "  (None found)"
    echo ""
    read -p "Enter macOS Codesigning Identity (or leave empty to skip macOS signing): " LFT_MACOS_IDENTITY
fi

# 3. Setup/Prompt for Windows P12 Certificate
if [ -z "$LFT_SIGN_P12" ]; then
    if [ -f "$PROJECT_ROOT/temp_signing_key.p12" ]; then
        LFT_SIGN_P12="$PROJECT_ROOT/temp_signing_key.p12"
        echo "Found default temporary certificate: $LFT_SIGN_P12"
    else
        read -p "Enter path to Windows P12 certificate (or leave empty to skip Windows signing): " LFT_SIGN_P12
    fi
fi

# Prompt for Windows P12 password if path is set but password is empty
if [ -n "$LFT_SIGN_P12" ] && [ -z "$LFT_SIGN_PASS" ]; then
    read -s -p "Enter password for Windows P12 certificate: " LFT_SIGN_PASS
    echo ""
fi

# 4. Setup GPG Key for Linux
if [ -z "$LFT_GPG_KEY" ]; then
    read -p "Enter GPG key ID/email for Linux binary signing (or leave empty for default GPG key): " LFT_GPG_KEY
fi

echo ""
echo "=== Beginning Signing Process ==="

# macOS Signing
if [ -n "$LFT_MACOS_IDENTITY" ]; then
    echo "Signing macOS binaries..."
    codesign --force --options runtime --sign "$LFT_MACOS_IDENTITY" "$BIN_DIR/lfr-tunnel-darwin-arm64"
    codesign --force --options runtime --sign "$LFT_MACOS_IDENTITY" "$BIN_DIR/lfr-tunnel-darwin-amd64"
    echo "macOS binaries successfully signed!"
else
    echo "Skipping macOS codesigning (no identity provided)."
fi

# Windows Signing
if [ -n "$LFT_SIGN_P12" ] && [ -f "$LFT_SIGN_P12" ]; then
    if command -v osslsigncode &> /dev/null; then
        echo "Signing Windows binary..."
        # Create a temp signed file then overwrite to make it atomic
        osslsigncode sign \
          -pkcs12 "$LFT_SIGN_P12" \
          -pass "$LFT_SIGN_PASS" \
          -n "Liferay Tunnel" \
          -i "https://github.com/peterrichards-lr/lfr-tunnel" \
          -in "$BIN_DIR/lfr-tunnel-windows-amd64.exe" \
          -out "$BIN_DIR/lfr-tunnel-windows-amd64-signed.exe"
        mv "$BIN_DIR/lfr-tunnel-windows-amd64-signed.exe" "$BIN_DIR/lfr-tunnel-windows-amd64.exe"
        echo "Windows binary successfully signed!"
    else
        echo "WARNING: 'osslsigncode' tool not found. Please install it using 'brew install osslsigncode' to sign Windows binaries."
    fi
else
    echo "Skipping Windows signing (no certificate provided or found)."
fi

# Linux GPG Signing
if command -v gpg &> /dev/null; then
    echo "Generating Linux detached GPG signature..."
    rm -f "$BIN_DIR/lfr-tunnel-linux-amd64.asc"
    if [ -n "$LFT_GPG_KEY" ]; then
        gpg --yes --local-user "$LFT_GPG_KEY" --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64"
    else
        gpg --yes --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64"
    fi
    echo "Linux detached GPG signature successfully created!"
else
    echo "Skipping Linux GPG signing (gpg command not found)."
fi

# 5. Re-generate Checksums
echo "Updating checksums.txt..."
cd "$BIN_DIR"
# Exclude checksums.txt and signature files from checksum list itself
find . -type f ! -name "checksums.txt" ! -name "*.asc" | sed 's|^\./||' | sort | xargs shasum -a 256 > checksums.txt
echo "Checksums updated in $BIN_DIR/checksums.txt"

echo "=== Client Signing Complete! ==="
