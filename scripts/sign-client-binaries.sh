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

# 3. Setup/Prompt for Windows Certificate (P12, or separate KEY and CRT)
if [ -z "$LFT_SIGN_P12" ] && { [ -z "$LFT_SIGN_KEY" ] || [ -z "$LFT_SIGN_CRT" ]; }; then
    if [ -f "$PROJECT_ROOT/temp_signing_key.p12" ]; then
        LFT_SIGN_P12="$PROJECT_ROOT/temp_signing_key.p12"
        echo "Found default temporary certificate: $LFT_SIGN_P12"
    elif [ -t 0 ]; then
        echo "Windows signing configuration:"
        echo "You can either provide a single P12 certificate, or separate Private Key + Certificate files."
        read -p "Enter path/1Password name for Windows P12 certificate (leave empty to use separate KEY/CRT): " LFT_SIGN_P12
        if [ -z "$LFT_SIGN_P12" ]; then
            read -p "Enter path/1Password name for Windows Private Key (or leave empty to skip Windows signing): " LFT_SIGN_KEY
            if [ -n "$LFT_SIGN_KEY" ]; then
                read -p "Enter path/1Password name for Windows Certificate: " LFT_SIGN_CRT
            fi
        fi
    else
        echo "No Windows signing credentials specified (LFT_SIGN_P12 or LFT_SIGN_KEY/LFT_SIGN_CRT) and stdin is not a TTY. Skipping prompt."
    fi
fi

# Prompt for password if credentials are set but password is empty
if { [ -n "$LFT_SIGN_P12" ] && [ "$LFT_SIGN_P12" != "skip" ]; } || { [ -n "$LFT_SIGN_KEY" ] && [ "$LFT_SIGN_KEY" != "skip" ]; }; then
    if [ -z "$LFT_SIGN_PASS" ]; then
        if [ -t 0 ]; then
            read -s -p "Enter password for Windows P12 certificate/Private Key: " LFT_SIGN_PASS
            echo ""
        else
            echo "Windows signing credentials specified but password is empty and stdin is not a TTY. Skipping password prompt."
        fi
    fi
fi

# 4. Setup GPG Key for Linux
if [ "$LFT_SKIP_GPG" != "true" ] && [ -z "$LFT_GPG_KEY" ]; then
    if [ -t 0 ]; then
        read -p "Enter path/1Password name for GPG Private Key (or leave empty to skip GPG setup): " LFT_GPG_KEY
    else
        echo "No GPG key specified and stdin is not a TTY. Skipping Linux GPG key prompt."
    fi
fi

# Prompt for GPG key passphrase if needed and stdin is a TTY (and no fallback password is set)
if [ "$LFT_SKIP_GPG" != "true" ] && [ -n "$LFT_GPG_KEY" ] && [ "$LFT_GPG_KEY" != "skip" ] && [ -z "$LFT_GPG_PASS" ] && [ -z "$LFT_SIGN_PASS" ]; then
    if [ -t 0 ]; then
        read -s -p "Enter passphrase for GPG Private Key: " LFT_GPG_PASS
        echo ""
    fi
fi

# Track files to delete on exit
TEMP_FILES=()
cleanup() {
    if [ ${#TEMP_FILES[@]} -gt 0 ]; then
        echo "Cleaning up temporary signing files..."
        rm -f "${TEMP_FILES[@]}"
    fi
    if [ -n "$GNUPGHOME" ] && [[ "$GNUPGHOME" == /tmp/lfr-tunnel-gpghome-* ]]; then
        echo "Cleaning up temporary GPG home directory..."
        rm -rf "$GNUPGHOME"
    fi
}
trap cleanup EXIT

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

if [[ "$LFT_GPG_PASS" == op://* ]]; then
    if ! command -v op &> /dev/null; then
        echo "ERROR: 1Password CLI (op) not found but LFT_GPG_PASS op:// reference was provided." >&2
        exit 2
    fi
    echo "Fetching GPG passphrase from 1Password..."
    LFT_GPG_PASS=$(op read "$LFT_GPG_PASS") || {
        echo "ERROR: Failed to retrieve GPG passphrase from 1Password using reference: $LFT_GPG_PASS" >&2
        exit 3
    }
fi

# Fallback to LFT_SIGN_PASS for GPG signing if LFT_GPG_PASS is not set
if [ -z "$LFT_GPG_PASS" ] && [ -n "$LFT_SIGN_PASS" ]; then
    LFT_GPG_PASS="$LFT_SIGN_PASS"
fi

if [ -n "$LFT_SIGN_P12" ] && [ "$LFT_SIGN_P12" != "skip" ] && [ ! -f "$LFT_SIGN_P12" ]; then
    if ! command -v op &> /dev/null; then
        echo "ERROR: File '$LFT_SIGN_P12' not found locally, and 1Password CLI (op) is not installed." >&2
        exit 2
    fi
    echo "Fetching certificate document from 1Password..."
    TEMP_P12="/tmp/lfr-tunnel-windows-$(date +%s).p12"
    if [[ "$LFT_SIGN_P12" == op://* ]]; then
        if ! op read "$LFT_SIGN_P12" > "$TEMP_P12" 2>/dev/null && ! op document get "$LFT_SIGN_P12" --output "$TEMP_P12" &>/dev/null; then
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
    TEMP_FILES+=("$TEMP_P12")
fi

# Reconstruct P12 from Key + Certificate if both are specified
if [ -n "$LFT_SIGN_KEY" ] && [ "$LFT_SIGN_KEY" != "skip" ] && [ -n "$LFT_SIGN_CRT" ] && [ "$LFT_SIGN_CRT" != "skip" ]; then
    # 1. Fetch private key
    TEMP_KEY=""
    if [ -f "$LFT_SIGN_KEY" ]; then
        TEMP_KEY="$LFT_SIGN_KEY"
    else
        if ! command -v op &> /dev/null; then
            echo "ERROR: Private key '$LFT_SIGN_KEY' not found locally, and 1Password CLI (op) is not installed." >&2
            exit 2
        fi
        echo "Fetching private key from 1Password..."
        TEMP_KEY="/tmp/lfr-tunnel-signing-key-$(date +%s).key"
        if [[ "$LFT_SIGN_KEY" == op://* ]]; then
            if ! op read "$LFT_SIGN_KEY" > "$TEMP_KEY" 2>/dev/null && ! op document get "$LFT_SIGN_KEY" --output "$TEMP_KEY" &>/dev/null; then
                echo "ERROR: Failed to retrieve private key from 1Password using reference: $LFT_SIGN_KEY" >&2
                exit 3
            fi
        else
            if ! op document get "$LFT_SIGN_KEY" --out-file "$TEMP_KEY" &>/dev/null; then
                echo "ERROR: Failed to retrieve 1Password document named: $LFT_SIGN_KEY" >&2
                exit 3
            fi
        fi
        TEMP_FILES+=("$TEMP_KEY")
    fi

    # 2. Fetch public certificate
    TEMP_CRT=""
    if [ -f "$LFT_SIGN_CRT" ]; then
        TEMP_CRT="$LFT_SIGN_CRT"
    else
        if ! command -v op &> /dev/null; then
            echo "ERROR: Certificate '$LFT_SIGN_CRT' not found locally, and 1Password CLI (op) is not installed." >&2
            exit 2
        fi
        echo "Fetching public certificate from 1Password..."
        TEMP_CRT="/tmp/lfr-tunnel-signing-cert-$(date +%s).crt"
        if [[ "$LFT_SIGN_CRT" == op://* ]]; then
            if ! op read "$LFT_SIGN_CRT" > "$TEMP_CRT" 2>/dev/null && ! op document get "$LFT_SIGN_CRT" --output "$TEMP_CRT" &>/dev/null; then
                echo "ERROR: Failed to retrieve certificate from 1Password using reference: $LFT_SIGN_CRT" >&2
                exit 3
            fi
        else
            if ! op document get "$LFT_SIGN_CRT" --out-file "$TEMP_CRT" &>/dev/null; then
                echo "ERROR: Failed to retrieve 1Password document named: $LFT_SIGN_CRT" >&2
                exit 3
            fi
        fi
        TEMP_FILES+=("$TEMP_CRT")
    fi

    # 3. Generate PKCS12 file using openssl
    echo "Assembling PKCS12 (.p12) signing archive using openssl..."
    TEMP_P12="/tmp/lfr-tunnel-windows-$(date +%s).p12"
    if [ -n "$LFT_SIGN_PASS" ]; then
        openssl pkcs12 -export \
          -out "$TEMP_P12" \
          -inkey "$TEMP_KEY" \
          -passin "pass:$LFT_SIGN_PASS" \
          -in "$TEMP_CRT" \
          -passout "pass:$LFT_SIGN_PASS" || {
              echo "ERROR: Failed to assemble PKCS12 (.p12) file using openssl." >&2
              exit 4
          }
    else
        openssl pkcs12 -export \
          -out "$TEMP_P12" \
          -inkey "$TEMP_KEY" \
          -in "$TEMP_CRT" \
          -nodes || {
              echo "ERROR: Failed to assemble PKCS12 (.p12) file using openssl (no password)." >&2
              exit 4
          }
    fi
    LFT_SIGN_P12="$TEMP_P12"
    TEMP_FILES+=("$TEMP_P12")
fi

# GPG Key Retrieval and Setup GPGHOME
if [ "$LFT_SKIP_GPG" != "true" ] && [ -n "$LFT_GPG_KEY" ] && [ "$LFT_GPG_KEY" != "skip" ]; then
    TEMP_GPG_KEY=""
    if [ -f "$LFT_GPG_KEY" ]; then
        TEMP_GPG_KEY="$LFT_GPG_KEY"
    else
        if ! command -v op &> /dev/null; then
            echo "ERROR: GPG key '$LFT_GPG_KEY' not found locally, and 1Password CLI (op) is not installed." >&2
            exit 2
        fi
        echo "Fetching GPG private key from 1Password..."
        TEMP_GPG_KEY="/tmp/lfr-tunnel-gpgkey-$(date +%s).asc"
        if [[ "$LFT_GPG_KEY" == op://* ]]; then
            if ! op read "$LFT_GPG_KEY" > "$TEMP_GPG_KEY" 2>/dev/null && ! op document get "$LFT_GPG_KEY" --output "$TEMP_GPG_KEY" &>/dev/null; then
                echo "ERROR: Failed to retrieve GPG private key from 1Password using reference: $LFT_GPG_KEY" >&2
                exit 3
            fi
        else
            if ! op document get "$LFT_GPG_KEY" --out-file "$TEMP_GPG_KEY" &>/dev/null; then
                echo "ERROR: Failed to retrieve 1Password GPG document named: $LFT_GPG_KEY" >&2
                exit 3
            fi
        fi
        TEMP_FILES+=("$TEMP_GPG_KEY")
    fi

    # Set up temporary isolated GPG Home Directory
    export GNUPGHOME="/tmp/lfr-tunnel-gpghome-$(date +%s)"
    echo "Creating isolated temporary GPG keyring in $GNUPGHOME..."
    mkdir -p -m 700 "$GNUPGHOME"

    # Import GPG key
    if [ -n "$LFT_GPG_PASS" ]; then
        gpg --batch --import --pinentry-mode loopback --passphrase "$LFT_GPG_PASS" "$TEMP_GPG_KEY" || {
            echo "ERROR: Failed to import GPG key with passphrase." >&2
            exit 3
        }
    else
        gpg --batch --import "$TEMP_GPG_KEY" || {
            echo "ERROR: Failed to import GPG key." >&2
            exit 3
        }
    fi
    
    # Extract the key ID of the imported key so we can reference it exactly for signing
    IMPORTED_KEY_ID=$(gpg --list-secret-keys --with-colons | grep '^sec:' | cut -d: -f5 | head -n1)
    if [ -n "$IMPORTED_KEY_ID" ]; then
        echo "Successfully imported GPG private key: $IMPORTED_KEY_ID"
        LFT_GPG_KEY="$IMPORTED_KEY_ID"
    fi
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
        if [ -n "$LFT_GPG_PASS" ]; then
            gpg --batch --yes --pinentry-mode loopback --passphrase "$LFT_GPG_PASS" \
              --local-user "$LFT_GPG_KEY" --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64" || {
                  echo "ERROR: Linux GPG signing failed." >&2
                  exit 4
              }
        elif [ -n "$LFT_GPG_KEY" ]; then
            gpg --batch --yes --local-user "$LFT_GPG_KEY" --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64" || {
                echo "ERROR: Linux GPG signing failed." >&2
                exit 4
            }
        else
            gpg --batch --yes --armor --detach-sign "$BIN_DIR/lfr-tunnel-linux-amd64" || {
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

# Generate Minisign signature for checksums.txt
if [ -f "$PROJECT_ROOT/scripts/minisign_helper.go" ]; then
    echo "Generating Minisign signature for checksums.txt..."
    go run "$PROJECT_ROOT/scripts/minisign_helper.go" "$BIN_DIR/checksums.txt" "$BIN_DIR/checksums.txt.minisig" || {
        echo "ERROR: Minisign signature generation failed." >&2
        exit 5
    }
fi

echo "=== Client Signing Complete! ==="
