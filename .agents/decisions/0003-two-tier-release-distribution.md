STATUS: Accepted 2026-08-24

# 0003. Release distribution is two-tier, and only the central tier carries an OS code signature

## Decision

Two tiers, signed differently rather than signed-versus-unsigned:

- **Public tier** (GitHub Releases, Homebrew, Scoop) carries a **minisign signature over
  `checksums.txt`**, produced in CI. `--upgrade` refuses to install from a release without it.
- **Central tier** (the binaries the server distributes itself, with its install scripts) carries
  the **OS-level** signatures as well: macOS codesign, Windows Authenticode, Linux GPG. That is the
  Liferay-specific EDR-verification path and is not pushed onto other users.

## Cost

A community user installing from the public tier can verify *what they downloaded* against a
signed checksum, but gets no OS-level code signature — so macOS Gatekeeper and SmartScreen still
treat the binary as unidentified. Accepted for the reason the release workflow itself gives: OS
codesigning needs an Apple Developer ID or a Windows certificate, which are **per-distributor** and
not something CI should hold on anyone's behalf.

## Enforced by

`.github/workflows/release.yml` — the `Sign Checksums (minisign)` step and the `Publish Release`
step that uploads `dist/*`, so the `.minisig` ships with the release; `scripts/update-tap-bucket.sh`
carries the same checksums to Homebrew and Scoop. The OS-signing half is
`.agents/skills/lfr-tunnel-ops/SKILL.md` §4, run locally rather than in CI.

`make verify-release` (`scripts/verify-release-assets.sh`) is **not** a signature check — it
compares built artefact names against the published release's asset list, i.e. completeness of the
public tier, and inspects no signature at all.

## Revisit when

A distributor identity becomes available to CI without being held on someone's behalf, or a
channel starts requiring an OS-level signature.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-28* | *Last Reviewed: 2026-09-28*
