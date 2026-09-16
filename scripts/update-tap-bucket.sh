#!/bin/bash
# update-tap-bucket.sh
#
# Called by .github/workflows/release.yml after a new tag is published.
# Pushes updated Formula and Scoop manifest to the companion tap/bucket repos.
#
# Usage: ./scripts/update-tap-bucket.sh <version-tag> <pat> <checksums-file> [--dry-run]
#   version-tag    e.g. v1.7.1
#   pat            GitHub PAT with contents:write on homebrew-tap and scoop-bucket
#   checksums-file path to the checksums.txt produced by the release build
#   --dry-run      authenticate and report what WOULD be written, push nothing (#1969)
#
# --dry-run exists so a credential can be tested in seconds instead of by cutting a release.
# TAP_BUCKET_PAT expired after v1.48.31 and this step failed on three consecutive releases
# (v1.48.32/33/34) before anyone noticed, because it runs AFTER the release is published: the
# tag, the release and the assets all exist, so every outward sign of success is already true.
# The tag cannot be re-pushed, so there was no way to retry without burning another version.

set -euo pipefail

VERSION="${1:?Version tag is required (e.g. v1.7.1)}"
TAP_BUCKET_PAT="${2:-}"
CHECKSUMS_FILE="${3:?Path to checksums.txt is required}"
DRY_RUN="false"
if [ "${4:-}" = "--dry-run" ]; then
  DRY_RUN="true"
fi

# An absent credential is a FAILURE, not a clean skip (#1969).
#
# This previously exited 0 with a "skipping cleanly" message, so an unset secret would publish
# release after release that silently never reached Homebrew or Scoop -- the same shape as
# #1923, #1938 and #1956, where a broken state is indistinguishable from a deliberate one.
# We were lucky: the real incident had an INVALID pat, which fails loudly at clone. An unset one
# would have been silent.
#
# ALLOW_MISSING_TAP_PAT=true restores the old behaviour for a fork or a private build that has
# no tap to update -- deliberate, named, and visible in the log rather than assumed.
if [ -z "${TAP_BUCKET_PAT}" ]; then
  if [ "${ALLOW_MISSING_TAP_PAT:-false}" = "true" ]; then
    echo "TAP_BUCKET_PAT is empty and ALLOW_MISSING_TAP_PAT=true. Skipping deliberately."
    exit 0
  fi
  echo "::error::TAP_BUCKET_PAT is empty or missing, so the Homebrew tap and Scoop bucket" >&2
  echo "::error::cannot be updated. Users installing by those routes will stay on the previous" >&2
  echo "::error::version with no indication anything is wrong. Set the secret, or set" >&2
  echo "::error::ALLOW_MISSING_TAP_PAT=true if this build genuinely has no tap to update." >&2
  exit 1
fi

VERSION_NUM="${VERSION#v}"   # e.g. 1.7.1

# ---------------------------------------------------------------------------
# Extract SHA-256 hashes from checksums.txt
# Format produced by sha256sum: "<hash>  <filename>"
# ---------------------------------------------------------------------------
extract_hash() {
  grep "${1}$" "${CHECKSUMS_FILE}" | awk '{print $1}'
}

HASH_DARWIN_AMD64=$(extract_hash "lfr-tunnel-darwin-amd64")
HASH_DARWIN_ARM64=$(extract_hash "lfr-tunnel-darwin-arm64")
HASH_LINUX_AMD64=$(extract_hash  "lfr-tunnel-linux-amd64")
HASH_LINUX_ARM64=$(extract_hash  "lfr-tunnel-linux-arm64")
HASH_WINDOWS_AMD64=$(extract_hash "lfr-tunnel-windows-amd64.exe")

echo "Updating Homebrew Tap and Scoop Bucket for ${VERSION}"
echo "  darwin/amd64:  ${HASH_DARWIN_AMD64}"
echo "  darwin/arm64:  ${HASH_DARWIN_ARM64}"
echo "  linux/amd64:   ${HASH_LINUX_AMD64}"
echo "  linux/arm64:   ${HASH_LINUX_ARM64}"
echo "  windows/amd64: ${HASH_WINDOWS_AMD64}"

# ---------------------------------------------------------------------------
# 1. Homebrew Tap  (peterrichards-lr/homebrew-tap)
# ---------------------------------------------------------------------------
git clone "https://x-access-token:${TAP_BUCKET_PAT}@github.com/peterrichards-lr/homebrew-tap.git" _tap-repo
cd _tap-repo
git config user.name  "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

# Write the formula. Shell expands ${VERSION}, ${HASH_*}, ${VERSION_NUM}.
# Ruby's #{} interpolation is written as-is — shell does NOT expand it because
# #{} contains no '$', so it passes through untouched into the .rb file.
cat > Formula/lfr-tunnel.rb << FORMULA
class LfrTunnel < Formula
  desc "Secure HTTPS tunnel client for Liferay Sales Engineering team"
  homepage "https://github.com/peterrichards-lr/lfr-tunnel"
  version "${VERSION_NUM}"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${VERSION}/lfr-tunnel-darwin-arm64"
      sha256 "${HASH_DARWIN_ARM64}"
    end
    on_intel do
      url "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${VERSION}/lfr-tunnel-darwin-amd64"
      sha256 "${HASH_DARWIN_AMD64}"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${VERSION}/lfr-tunnel-linux-arm64"
      sha256 "${HASH_LINUX_ARM64}"
    end
    on_intel do
      url "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${VERSION}/lfr-tunnel-linux-amd64"
      sha256 "${HASH_LINUX_AMD64}"
    end
  end

  def install
    os   = OS.mac? ? "darwin" : "linux"
    arch = Hardware::CPU.arm? ? "arm64" : "amd64"
    bin.install "lfr-tunnel-#{os}-#{arch}" => "lfr-tunnel"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/lfr-tunnel -version 2>&1")
  end
end
FORMULA

git add Formula/lfr-tunnel.rb
# Commit only if there are staged changes (idempotent on re-runs)
git diff --cached --quiet || git commit -m "chore: bump lfr-tunnel to ${VERSION}"
if [ "${DRY_RUN}" = "true" ]; then
  echo "DRY RUN: would push the Homebrew formula for ${VERSION}. Nothing was pushed."
  git --no-pager diff --stat HEAD~1 HEAD 2>/dev/null || echo "  (no change: the tap already has ${VERSION})"
else
  git push
fi
cd ..
rm -rf _tap-repo
echo "Homebrew Tap updated."

# ---------------------------------------------------------------------------
# 2. Scoop Bucket  (peterrichards-lr/scoop-bucket)
# ---------------------------------------------------------------------------
git clone "https://x-access-token:${TAP_BUCKET_PAT}@github.com/peterrichards-lr/scoop-bucket.git" _bucket-repo
cd _bucket-repo
git config user.name  "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

# jq builds the JSON cleanly. Note: "$version" inside jq string literals is
# NOT a jq variable reference — jq only interpolates \(.expr), so the literal
# string "$version" is written as-is, which is what Scoop's autoupdate expects.
jq -n \
  --arg version        "${VERSION_NUM}" \
  --arg url            "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/${VERSION}/lfr-tunnel-windows-amd64.exe#/lfr-tunnel.exe" \
  --arg hash           "${HASH_WINDOWS_AMD64}" \
  '{
    "version":     $version,
    "description": "Secure HTTPS tunnel client for Liferay Sales Engineering team",
    "homepage":    "https://github.com/peterrichards-lr/lfr-tunnel",
    "license":     "MIT",
    "architecture": {
      "64bit": {
        "url":  $url,
        "hash": $hash
      }
    },
    "bin": [["lfr-tunnel.exe", "lfr-tunnel"]],
    "checkver": {
      "github": "https://github.com/peterrichards-lr/lfr-tunnel"
    },
    "autoupdate": {
      "architecture": {
        "64bit": {
          "url": "https://github.com/peterrichards-lr/lfr-tunnel/releases/download/v$version/lfr-tunnel-windows-amd64.exe#/lfr-tunnel.exe",
          "hash": {
            "url":   "https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/checksums/checksums.txt",
            "regex": "([a-fA-F0-9]{64})\\s+lfr-tunnel-windows-amd64\\.exe"
          }
        }
      }
    }
  }' > bucket/lfr-tunnel.json

git add bucket/lfr-tunnel.json
git diff --cached --quiet || git commit -m "chore: bump lfr-tunnel to ${VERSION}"
if [ "${DRY_RUN}" = "true" ]; then
  echo "DRY RUN: would push the Scoop manifest for ${VERSION}. Nothing was pushed."
  git --no-pager diff --stat HEAD~1 HEAD 2>/dev/null || echo "  (no change: the bucket already has ${VERSION})"
else
  git push
fi
cd ..
rm -rf _bucket-repo
if [ "${DRY_RUN}" = "true" ]; then
  echo "DRY RUN complete: both repositories authenticated and the content was rendered."
  echo "Nothing was pushed. The credential is valid."
else
  echo "Scoop Bucket updated." 
fi
