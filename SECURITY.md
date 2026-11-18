# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `lfr-tunnel`, please **do not open a public GitHub Issue**.

Report it privately via **[GitHub Private Security Advisories](https://github.com/peterrichards-lr/lfr-tunnel/security/advisories/new)**. This allows the maintainers to assess and patch the issue before any public disclosure.

Please include:
- A clear description of the vulnerability
- Steps to reproduce it
- The version of `lfr-tunnel` affected
- The potential impact

You can expect an initial response within **5 business days** and a patch within **30 days** for confirmed issues, depending on severity.

---

## Binary Signing & EDR Compatibility

### Why the Binary Is Unsigned

`lfr-tunnel` is distributed as a **pre-compiled, unsigned binary**. It does not carry an Apple Developer ID (macOS) or a Windows Authenticode signature (Windows).

This is a deliberate, cost-driven decision for an open-source, internal tooling project:

| Signing Type | Requirement | Status |
|---|---|---|
| **Apple Developer ID** (macOS `Type=Signed`) | $99/year Apple Developer Program membership. Apple is the sole issuer — no alternative CA can produce this certificate type. | Not held |
| **Windows Authenticode** | Commercial CA certificate stored on a hardware HSM. Recurring annual cost. | Not held |
| **Let's Encrypt / TLS certificates** | Free, domain-validated TLS certificates for HTTPS web servers only. | Wrong certificate type — cannot be used for executable signing |

**Neither an Apple Developer ID nor a Windows Authenticode certificate is required to use `lfr-tunnel`.** They are trust signals consumed by the OS loader and by Endpoint Detection and Response (EDR) engines. The absence of these certificates does not make the binary unsafe.

---

### What We Do to Establish Trust

#### Build Hardening Flags

All release binaries are built with:

| Flag | Purpose |
|---|---|
| `-ldflags="-s -w"` | Strips debug information and symbol tables from the binary, reducing its static analysis footprint |
| `-trimpath` | Removes local build-machine filesystem paths (e.g. `/home/runner/...`) that would otherwise be embedded in the binary |

These flags ensure that release binaries are as clean and reproducible as possible, with no unintended artefacts embedded.

#### Signed Release Checksums (minisign)

Every release publishes `checksums.txt` (the SHA-256 of each binary) and `checksums.txt.minisig`,
a [minisign](https://jedisct1.github.io/minisign/) signature over it produced by the release
workflow. `lfr-tunnel --upgrade` refuses a release without a valid signature, and checks the
downloaded binary against `checksums.txt` before installing it.

> [!IMPORTANT]
> This proves the binary is the one the project published. It is not OS-level code signing: the
> public release binaries carry no Apple or Authenticode signature, so EDR tools report them as
> unsigned. Releases do **not** carry GitHub Artifact Attestations.

#### SHA-256 Checksums

Download `checksums.txt` from the [Releases page](https://github.com/peterrichards-lr/lfr-tunnel/releases) and verify manually:

```bash
# macOS / Linux, in the directory holding the downloaded binaries
sha256sum -c --ignore-missing checksums.txt

# Or hash an installed binary and compare it with the matching line
sha256sum "$HOME/liferay/lfr-tunnel/lfr-tunnel"
```

#### Package Manager Distribution (Homebrew & Scoop)

`lfr-tunnel` is distributed through the following package managers, which add an independent layer of verification on top of the signed checksums:

| Package Manager | Platform | Install Command |
|---|---|---|
| **Homebrew** | macOS / Linux | `brew tap peterrichards-lr/tap && brew install lfr-tunnel` |
| **Scoop** | Windows | `scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket && scoop install lfr-tunnel` |

What package managers add that a direct download does not:

- **Independent SHA-256 verification** — the package manager checks the hash against the published formula/manifest before installing, acting as a separate verification step from our own `checksums.txt`
- **Known, auditable install source** — the formula and manifest are public, versioned files in GitHub repositories that security teams can inspect
- **macOS quarantine removal** — Homebrew automatically removes the `com.apple.quarantine` attribute, eliminating Gatekeeper pop-up dialogs for downloaded binaries
- **Enterprise policy alignment** — many organisations already have approved policies covering software installed via Homebrew or Scoop, making these routes easier to whitelist than a raw binary download

> [!NOTE]
> Package managers **do not sign the binary**. The binary remains `Type=Unsigned` from an EDR perspective. On a machine with an EDR, prefer the native installer: its path is the one the exclusion rules cover ([docs/infosec.md §3](docs/infosec.md#3-standard-execution-paths--edr-exclusions)). Liferay supports only the native installer on its EDR-managed machines.



### EDR & Endpoint Security Agent Compatibility

#### Why EDR Engines Flag `lfr-tunnel`

`lfr-tunnel` is a Go binary that uses WebSocket tunnelling (via the [Chisel](https://github.com/jpillora/chisel) library). EDR engines — including SentinelOne, CrowdStrike, and Microsoft Defender — may flag it for one or more of the following heuristic reasons:

1. **Unsigned binary** — No OS-level publisher certificate to establish identity
2. **Go runtime** — Go embeds its full runtime in every binary; some byte patterns in the Go runtime are shared with known malware samples
3. **WebSocket tunnelling behaviour** — Network tunnels are a common pattern in both legitimate tooling and in post-exploitation malware

All of these are **false positives** for `lfr-tunnel`. The tool is open-source, auditable, and performs only the tunnelling described in its documentation.

#### Install Locations and Recommended Exclusions

Install paths and EDR exclusion rules are kept in one place:
**[docs/infosec.md §3](docs/infosec.md#3-standard-execution-paths--edr-exclusions)**.

In short, the native installers put the client at `<home>/liferay/lfr-tunnel/lfr-tunnel` (`.exe`
on Windows) on every OS, plus an LDM link in `<home>/.ldm/bin/`, and those are the paths to exclude.
Homebrew and Scoop installs land where the package manager decides; on a machine with an EDR,
use the native installer, or exclude those paths yourself.

#### False Positive Submission

If your EDR vendor supports false positive submissions (SentinelOne, CrowdStrike, and Defender all do), please submit the official release binary. Official release hashes are available in `checksums.txt` on the [Releases page](https://github.com/peterrichards-lr/lfr-tunnel/releases). This allows the vendor to tune their AI model globally for this binary.

#### Docker Alternative

If a path exclusion cannot be applied in your environment, `lfr-tunnel` can be run inside a Docker container. See the [Running via Docker](README.md#running-via-docker-alternative--edr-bypass) section of the README.

---

## Supported Versions

Security patches are applied to the **latest release only**. We do not backport fixes to older releases.

| Version | Supported |
|---|---|
| Latest release | ✅ Yes |
| Older releases | ❌ No |

Always run `lfr-tunnel -upgrade` to ensure you are on the latest version.


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-18* | *Last Reviewed: 2026-11-18*
