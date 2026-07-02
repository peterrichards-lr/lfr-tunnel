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
| **Windows Authenticode** | Commercial CA certificate stored on a hardware HSM. Recurring annual cost. | Not held (see [SignPath](#windows-authenticode--signpath-foundation) below) |
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

#### GitHub Artifact Attestations (Sigstore)

Every release binary is covered by a **GitHub Artifact Attestation** — a free, keyless, OIDC-backed cryptographic provenance record. This is powered by [Sigstore](https://www.sigstore.dev/) and uses GitHub's own OIDC identity to create a tamper-proof, publicly verifiable record in the [Rekor](https://rekor.sigstore.dev/) transparency log.

The attestation proves:
- **Which repository** produced the binary (`peterrichards-lr/lfr-tunnel`)
- **Which commit SHA** was checked out
- **Which workflow file** ran the build
- **That the binary has not been modified** since the workflow produced it

> [!IMPORTANT]
> This is **supply-chain provenance**, not OS-level signing. The binary will still appear as `Type=Unsigned` in SentinelOne and similar EDR tools. The attestation does not embed a signature inside the binary — it is a separate, verifiable record.

**Verify any downloaded binary using the GitHub CLI:**
```bash
gh attestation verify ~/runningpoc/bin/lfr-tunnel --repo peterrichards-lr/lfr-tunnel
```

#### SHA-256 Checksums

A `checksums.txt` file is generated for every release and is itself covered by the Artifact Attestation. Download it from the [Releases page](https://github.com/peterrichards-lr/lfr-tunnel/releases) and verify manually:

```bash
# macOS / Linux
sha256sum -c checksums.txt

# Or verify a single binary
sha256sum ~/runningpoc/bin/lfr-tunnel
```

#### Package Manager Distribution (Homebrew & Scoop)

`lfr-tunnel` is distributed through the following package managers, which add an independent layer of verification on top of the GitHub Artifact Attestations:

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
> Package managers **do not sign the binary**. The binary remains `Type=Unsigned` from an EDR perspective. However, the consistent, auditable install process and predictable install paths give security teams a stronger basis for applying a policy exclusion.

---

### Windows Authenticode — SignPath Foundation

The [SignPath Foundation](https://signpath.org) offers **free Windows Authenticode code signing** for qualifying open-source projects. `lfr-tunnel` meets the eligibility criteria (MIT license, public repository, actively maintained). We intend to apply for this programme. When approved, Windows release binaries will carry an Authenticode signature and will no longer appear as unsigned on Windows.

> [!NOTE]
> SignPath covers **Windows only**. macOS signing requires Apple's proprietary Developer ID certificate, which SignPath cannot provide.

---

### EDR & Endpoint Security Agent Compatibility

#### Why EDR Engines Flag `lfr-tunnel`

`lfr-tunnel` is a Go binary that uses WebSocket tunnelling (via the [Chisel](https://github.com/jpillora/chisel) library). EDR engines — including SentinelOne, CrowdStrike, and Microsoft Defender — may flag it for one or more of the following heuristic reasons:

1. **Unsigned binary** — No OS-level publisher certificate to establish identity
2. **Go runtime** — Go embeds its full runtime in every binary; some byte patterns in the Go runtime are shared with known malware samples
3. **WebSocket tunnelling behaviour** — Network tunnels are a common pattern in both legitimate tooling and in post-exploitation malware

All of these are **false positives** for `lfr-tunnel`. The tool is open-source, auditable, and performs only the tunnelling described in its documentation.

#### Canonical Install Locations

`lfr-tunnel` is always installed to a predictable location. The exact path depends on the install method:

| Method / Integration | Platform | Path |
|---|---|---|
| Homebrew | macOS (Apple Silicon) | `/opt/homebrew/bin/lfr-tunnel` |
| Homebrew | macOS (Intel) | `/usr/local/bin/lfr-tunnel` |
| Homebrew | Linux | `/home/linuxbrew/.linuxbrew/bin/lfr-tunnel` |
| Scoop | Windows | `C:\Users\<username>\scoop\shims\lfr-tunnel.exe` |
| Direct install script | macOS / Linux | `~/runningpoc/bin/lfr-tunnel` |
| Direct install script | Windows | `C:\Users\<username>\runningpoc\bin\lfr-tunnel.exe` |
| LDM execution loop | macOS / Linux | `~/.ldm/bin/lfr-tunnel` |
| LDM execution loop | Windows | `C:\Users\<username>\.ldm\bin\lfr-tunnel.exe` |

#### Recommended Exclusion (for Security Admins)

Add **wildcard path exclusions** in your EDR console covering package manager paths, direct install paths, and LDM integration paths:

**macOS:**
```
/opt/homebrew/bin/lfr-tunnel
/usr/local/bin/lfr-tunnel
/Users/*/runningpoc/bin/lfr-tunnel
/Users/*/.ldm/bin/lfr-tunnel
```

**Linux:**
```
/home/linuxbrew/.linuxbrew/bin/lfr-tunnel
/home/*/runningpoc/bin/lfr-tunnel
/home/*/.ldm/bin/lfr-tunnel
```

**Windows:**
```
C:\Users\*\scoop\shims\lfr-tunnel.exe
C:\Users\*\runningpoc\bin\lfr-tunnel.exe
C:\Users\*\.ldm\bin\lfr-tunnel.exe
```

Because all install methods produce a binary at a predictable, fixed path, these wildcard rules cover the entire team and survive binary upgrades without needing to be updated per version or per user.

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
*Last Updated: 2026-07-02* | *Last Reviewed: 2026-07-02*
