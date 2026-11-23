# Liferay Tunnel (lfr-tunnel) - InfoSec & EDR Security Briefing

This briefing document details the security architecture, risk-mitigation features, code-signing identities, and recommended EDR exclusion policies for `lfr-tunnel` (Liferay Tunnel). It is designed to provide Information Security (InfoSec) and Endpoint Detection & Response (EDR) administration teams with the necessary data to evaluate and authorize the tool for developer workstations.

---

## Executive Summary: Purpose, Business Case & Value

### 1. Intended Purpose

`lfr-tunnel` is an internal utility designed specifically for the **Liferay Sales Engineering (SE)** and development teams. It enables developers to securely route traffic from wildcard subdomains on a corporate VPS to local ports on their local workstations (e.g., Liferay Tomcat running on port `8080` or frontend asset compilers running on port `3000`).
This tool is used to:

* Perform live client-facing demos of integrations and Liferay Client Extensions (CX).
* Test and debug webhooks, mobile application integrations, and OIDC/SSO federations that require public HTTPS return URLs.
* Present work-in-progress implementations to remote stakeholders without deploying to staging environments.

### 2. Business Case

* **Data Sovereignty & Security Compliance:** Public third-party tunneling solutions (e.g., ngrok, LocalTunnel) route corporate data and client configurations through external SaaS networks. `lfr-tunnel` is self-hosted entirely on company-controlled infrastructure, ensuring that sensitive data, source code, and demo payloads never exit corporate security boundaries.
* **Cost Efficiency:** Provides standard administrative capabilities for the entire global team without incurring the monthly subscription licensing costs of enterprise-level commercial proxy services.
* **Optimization for Liferay:** The gateway is pre-configured to inject Liferay-specific headers (e.g., `X-Forwarded-Host`, `X-Forwarded-Proto`) natively and handles offline client environments gracefully with a themed fallback landing page.

### 3. Business Value

* **Accelerated Sales Cycles:** SE teams can rapidly build, test, and present bespoke integrations and client extensions, drastically decreasing prototype turnaround times for deals.
* **Developer Productivity:** Bypasses manual firewall configuration, router configuration, and deployment loops, allowing engineers to iterate and debug in real-time.
* **Corporate Oversight:** Rather than developers downloading arbitrary, unmonitored tunneling tools, `lfr-tunnel` unifies tunneling utility under single sign-on (SSO) authentication, centralized audit logging, and active lease management.

### 4. Proof of Concept (POC) Deployment & Hosting Info

For the initial Proof of Concept (POC), the gateway is hosted on a dedicated Virtual Private Server (VPS) located in a secure datacentre (e.g., in the UK/EU), leased through a secure hosting provider. Hosting on this infrastructure ensures:

* **GDPR & Regional Data Protection Alignment:** The routing gateway resides in a compliant jurisdiction, maintaining alignment with strict regional data sovereignty and privacy policies.
* **Isolated Proofing Environment:** Provides the development/SE team with a sandboxed environment to validate security compliance, latency, and reliability prior to any broader corporate migration.

### 5. DNS Domains, Transport Security & SMTP Defenses

* **Dedicated Domains & DNS Isolation:** The POC operates under dedicated wildcard domains (e.g., `your-tunnel-domain.com`), keeping development traffic isolated from core corporate domains.
* **SSL/TLS Termination via Nginx:** The public VPS runs Nginx as a reverse proxy. Traffic is encrypted using **Let's Encrypt** certificates, with all HTTP traffic redirected to secure HTTPS (`TLS v1.3`).
* **Secure SMTP Transactional Mail:** Outbound transactional emails are processed using an on-server **Postfix** configuration. It is protected by:
  * **Native STARTTLS:** Presenting Let's Encrypt certificates to encrypt mail in transit.
  * **Strict SPF Validations:** Enforcing dual-stack SPF validation to prevent domain spoofing.
  * **Relaying Restrictions:** Whitelisting only the local VPS IP (`mynetworks`) to block external relay access.

---

## 1. Architectural Security & Attack Vector Minimization

Unlike generic reverse proxy utilities, `lfr-tunnel` has been engineered with strict enterprise controls.

* **Outbound-Only Connectivity (Zero Listening Ports on Endpoints):**
    `lfr-tunnel` operates via outbound WebSockets (`wss://`) over TLS to a designated central public gateway (`lfr-tunneld`). It establishes a reverse port forwarding link. As a result, developer workstations **never open inbound ports** on the local host firewall, preventing exposure to corporate intranet scanning or external probes.
* **OIDC/SSO Authenticated Client Handshake:**
    Client connections require authentication. Static shared passwords are not supported for developers. Authentication is integrated with corporate Identity Providers (IdP) such as Keycloak, Okta, or Azure AD using OpenID Connect (OIDC) protocols to align with your Liferay SSO process. The login handshake (`lfr-tunnel login`) generates short-lived, revocable developer tokens.
* **Passwordless Magic Links & MFA Safeguards:**
    For administrative dashboard actions, local static user passwords are removed. Authentication is conducted using short-lived, secure, single-use **Magic Links** delivered to verified user mailboxes. Multi-Factor Authentication (MFA/TOTP) acts as a mandatory secondary gate on top of magic links.
* **SSO-Only Lockdown (No-Backdoor Policy):**
    To satisfy strict corporate compliance and prevent bypass channels, the gateway supports a `disable_email_login` configuration flag. When active, it disables email-based magic link logins, registration requests and admin invites globally at both the API and interface levels, and the gateway refuses to redeem any magic link, including one issued before the switch. This locks the application down so that federated OIDC corporate SSO is the **exclusive** method of entry.
* **Single-Session Concurrency:**
    Lease mapping enforces strict single-session concurrency per token or subdomain. A token or reserved subdomain cannot be used to multiplex simultaneous connections across multiple machines, preventing credentials from being shared or hijacked.
* **Secure Webhook Alerts (Outgoing-Only, Tokenized URLs):**
    Administrative notification dispatches (e.g. registration requests, abuse reports, rate limiter bans) to Slack and Microsoft Teams are outgoing-only HTTP POST requests initiated by the central gateway. They do not open any inbound listener ports or expose administrative APIs. The integrations utilize platform-generated URLs with embedded cryptographic tokens (e.g., Slack's secret path keys) to authorize payloads, avoiding basic auth or OAuth token refresh management. These webhook URLs are stored strictly on the private server gateway (`server-config.yaml`) and are never sent to client daemons or developers, eliminating the risk of credential exposure.

---

## 2. Code Signing & Trust Verification Metadata

Release binaries are signed, but **the exclusions this project asks for are path exclusions
(§3), not signer exclusions.** None of the signing identities below is issued by a public or
corporate CA, so an "Exclusion by Digital Subject (Publisher)" rule would trust a project-held
key rather than an organisation. The signatures exist so a binary can be checked against the one
the project published.

> [!IMPORTANT]
> **Key Management & Secrets Security:** All private keys, certificates, and passphrases used during the build and codesigning cycles are stored securely inside a corporate **1Password vault**. The release scripts retrieve these secrets dynamically at runtime via the 1Password CLI (`op`), ensuring no credentials or key files are hardcoded in the codebase or stored in plain text on build systems.

### macOS (self-signed code-signing identity)

* **Signing identity:** `Temp Code Sign`, a self-signed certificate. There is **no Apple
  Developer ID**, so `codesign` reports `TeamIdentifier=not set` and Gatekeeper treats the
  binary as unidentified.
* **Verification Command:**

    ```bash
    codesign -dv --verbose=4 "$HOME/liferay/lfr-tunnel/lfr-tunnel"
    ```

### Windows (self-signed Authenticode certificate)

* **Publisher Common Name (CN):** `CN=Lfr-Tunnel Windows Code Signing`, self-signed.
* **Verification:** `osslsigncode verify lfr-tunnel-windows-amd64.exe` reports
  `self-signed certificate` because there is no chain to a trusted root. The signature is good
  when the *Current* and *Calculated message digest* lines match.

### Linux (GPG Detached Signatures)

* **GPG key:** `Liferay Tunnel Developer <developer@lfr-demo.se>`, fingerprint
  `E8E407B5D8F557B3F0749EE676219C6F0B96C87B`.
* **Signature Verification:** Linux binaries are distributed alongside detached GPG signatures (`.asc` files) generated during the release process:

    ```bash
    gpg --verify lfr-tunnel-linux-amd64.asc lfr-tunnel-linux-amd64
    ```

### Release checksums (minisign)

Every release publishes `checksums.txt` and a minisign signature over it,
`checksums.txt.minisig`, produced by the release workflow. `lfr-tunnel --upgrade` refuses a
release without it, and checks the downloaded binary's SHA-256 against that file before
installing it. There are **no** GitHub Artifact Attestations: the release workflow does not
create them. (Decision record: `.agents/decisions/0003-two-tier-release-distribution.md`.)

---

## 3. Standard Execution Paths & EDR Exclusions

**This section is the single source for install paths and exclusions.** `SECURITY.md`, the SE
guide and the installers point here rather than restating paths, and
`tests/hooks/test-install-paths.sh` fails if any of them disagree with it.

### Where the client is installed

One executable name, one path and one install mechanism on every OS (#2508):

| | macOS | Linux | Windows |
|---|---|---|---|
| Executable | `~/liferay/lfr-tunnel/lfr-tunnel` | `~/liferay/lfr-tunnel/lfr-tunnel` | `%USERPROFILE%\liferay\lfr-tunnel\lfr-tunnel.exe` |
| LDM link | `~/.ldm/bin/lfr-tunnel` (symlink) | `~/.ldm/bin/lfr-tunnel` (symlink) | `%USERPROFILE%\.ldm\bin\lfr-tunnel.exe` (hardlink) |
| Install and upgrade | rename to `.old`, download in place | same | same |
| Autostart runs | the executable above | the executable above | the executable above |

Install and upgrade write the binary straight to that path: the current binary is renamed to
`lfr-tunnel.old` (or `lfr-tunnel.exe.old`) and restored if the download fails. Nothing is
staged in `/tmp`, `/private/tmp` or `%TEMP%` (#2505). Autostart runs the executable itself on
every OS, never a copy or a renamed link (#2504).

The LDM link exists so LDM finds this client instead of downloading an unexcluded copy of its own
(#1311). Every upgrade re-points it at the upgraded binary (#2553). On Windows that matters even
when the binary stays where it is: a hardlink is a second name for a file's content, so after an
upgrade replaced the file it kept naming the previous version.

When the gateway's `install_dir` moves the client, an upgrade:
1. writes the new binary at the new path;
2. re-points the LDM link;
3. re-registers the autostart entries the user has (the background service and the tray), and
   restarts the processes it stopped from the new binary;
4. updates what the installer wrote naming the old path: `LDM_LFR_TUNNEL_BIN`, and on Windows the
   user PATH entry;
5. removes the old binary last. On Windows the running upgrader *is* the old binary, which Windows
   will not delete, so the user is told to delete it afterwards.

If any step fails, the old binary is kept, because something still names it. It stays at the old
path, which is still excluded, so autostart and LDM still find a client there. The ordered
procedure for changing the path is in the ops skill, `.agents/skills/lfr-tunnel-ops/SKILL.md` §6d.

> [!NOTE]
> **To confirm in the test round (#2547):** on Windows, re-creating the hardlink while the old
> binary is running, and what SentinelOne does when a binary is written to a new path. Both are
> built from the documented Windows behaviour and have not been run on a real machine.

### EDR exclusion rules (Liferay)

On Liferay's EDR-managed machines the native installer is the only supported install route
(`.agents/decisions/0006-liferay-installs-natively-homebrew-and-scoop-are-for-everyone-else.md`),
so these are the only paths to exclude. **Two rules per platform**: InfoSec confirmed that the
behavioural and machine-learning engines were not satisfied by a binary exclusion alone, and
the LDM link had to be excluded as well.

#### Exclusion type: Alerts

**Every rule below is requested as an Alerts exclusion** (LRINFOSEC-7091). That is the exclusion
type InfoSec uses for detections. Two other types are not substitutes:

- **Performance Focus** is not for detections, and it costs InfoSec visibility. It is not requested.
- **Interoperability** is requested only where testing shows a functional problem from a path.
  It is never the only rule for a path: InfoSec confirmed an Interoperability-only rule is not
  enough (#2545).

**How the rules are applied.** InfoSec applies these through per-OS endpoint tags,
`macOS-global`, `Linux-global` and `Windows-global`. Each tag applies the Alerts-based exclusions
plus the designated file path, and nothing else. The older tags that allowed earlier tests and
PoCs are being removed, so an endpoint that relied on one of them is covered only through the new
tag (#2546). **Which endpoints carry the new tags, and the rollout scope beyond the test
endpoints, are open with InfoSec** (LRINFOSEC-7091).

#### Rules by platform

**macOS** (requested, Alerts):

```
/Users/*/liferay/lfr-tunnel/lfr-tunnel
/Users/*/.ldm/bin/lfr-tunnel
```

**Linux** (requested, Alerts):

```
/home/*/liferay/lfr-tunnel/lfr-tunnel
/home/*/.ldm/bin/lfr-tunnel
```

Linux previously had rules of another type only, which InfoSec says are not enough. The Alerts
rules are requested *in addition*. The earlier rules are pending removal: they stay until InfoSec
confirms the test results, and then go (#2545).

**Windows** (requested, Alerts):

```
C:\Users\*\liferay\lfr-tunnel\lfr-tunnel.exe
C:\Users\*\.ldm\bin\lfr-tunnel.exe
```

**The LDM hardlink needs its own Alerts rule** (#2544). On Windows, `.ldm\bin\lfr-tunnel.exe` is a
hardlink: a second path to the same file, not a pointer to the first. The EDR matches the path an
image executes from, so the binary's rule does not cover a run from the hardlink. It is a separate
Alerts exclusion, the same as the binary. Interoperability is added for it only if testing shows a
functional problem from that path. InfoSec found no EDR documentation on hardlinks, so this is
still to be confirmed in the test round. On macOS and Linux the LDM link is a symlink; its rule
stays for the reason given above, that the engines needed the link excluded as well.

The only wildcard is in the username position (`/Users/*/`, `/home/*/`, `C:\Users\*\`); everything
after it is matched literally, so the last two directory names and the executable name are all
load-bearing. On Windows that `*` may span more than one folder level: InfoSec confirmed
`C:\Users\*\...` is accepted (SentinelOne recommends `*` to cover all users), but the literal suffix
may then sit deeper than the user folder, for example under `C:\Users\<name>\Downloads\`. The rule
is tighter than `*\liferay\...`, not strictly limited to the username level (LRINFOSEC-7091).
No rule starts with a wildcard: the EDR console rejects exclusion paths that do, and on Windows a
leading `*\` would also span the drive and `Users`.

**Every rule is lowercase after its root** (`liferay`, not `Liferay`), because that is the case the
installers ask for. InfoSec could not confirm whether the EDR matches macOS paths case-sensitively
(LRINFOSEC-7091), so a rule spelled `Liferay` might match nothing there.
`tests/hooks/test-install-paths.sh` fails on an uppercase letter after the root.

> [!WARNING]
> **A pre-existing `~/Liferay` folder defeats this.** macOS and Windows file systems are
> case-insensitive by default, so if `~/Liferay` (any casing other than `liferay`) already exists,
> the installer reuses it and the client is stored as `~/Liferay/lfr-tunnel/lfr-tunnel`. If the
> EDR matches case-sensitively, the lowercase rule then misses it. On an EDR-managed machine, rename
> such a folder to `liferay` before installing, or check where `which lfr-tunnel` resolves.

**Package-manager rules are not requested.** An earlier version of this section listed
`/opt/homebrew/Cellar/lfr-tunnel/*/bin/lfr-tunnel` and `/opt/*/bin/lfr-tunnel`. Homebrew and Scoop
are not supported on EDR-managed machines, so those rules can be withdrawn.

### Homebrew and Scoop (outside Liferay)

The Homebrew tap and Scoop bucket remain a supported delivery route for everyone else, built from
the same release. They install where the package manager decides, for example
`$(brew --prefix)/Cellar/lfr-tunnel/<version>/bin/lfr-tunnel` (linked into `$(brew --prefix)/bin`)
and `%USERPROFILE%\scoop\apps\lfr-tunnel\current\lfr-tunnel.exe`. A machine with an EDR that
installs this way needs its own exclusions for those paths.

> [!IMPORTANT]
> **Installing anywhere else defeats both rules, including the LDM one.** The installers
> honour `LFT_INSTALL_DIR` (and the per-platform `LFR_TUNNEL_*_INSTALL_DIR` variables), so a
> developer who sets one installs outside the exclusion — and the `.ldm/bin` link then *resolves*
> to that unexcluded target, so being on a covered path does not help it. **Do not set these on
> an EDR-managed machine.**
>
> Observed in practice: a workstation with `LFT_INSTALL_DIR="$HOME/runningpoc/bin"` in its shell
> profile had every install and every `--upgrade` redirected there for months. `--upgrade` replaces
> the binary in place, so it stayed current *and* stayed unexcluded. If a machine is alerting,
> check `which lfr-tunnel` before anything else.

> [!NOTE]
> **Why Local Native Execution is Preferred Over Docker:** While a Docker sandbox fallback is provided for environments with strict local execution policies, running the tunnel natively is the recommended default. Running via Docker introduces substantial containerization overhead (increased CPU/memory footprint on developer laptops) and operational complexity (requiring Docker Desktop, managing container network bindings to map local Tomcat ports, and volume mapping config directories).

---

## 4. Gateway Governance & Data Plane Risk Controls

The central gateway service (`lfr-tunneld`) manages traffic and mitigates security threats at the boundary:

* **Subdomain Namespace Reservation:**
    Before a developer can bind a subdomain (e.g., `alpha-se`), it must be reserved in the gateway database. This prevents namespace hijacking, where an unauthorized developer intercepts traffic intended for another project.
* **Mandatory Subdomain Quarantine:**
    Released or deleted subdomains enter a mandatory **3-day quarantine period** during which they cannot be re-registered. This prevents DNS cache poisoning and host header hijack vulnerabilities.
* **Admin-Initiated Lease Drops (Kick Capability):**
    Through the portal dashboard, administrators can view all active tunnels and instantly terminate (kick) any active connection, immediately closing the reverse proxy socket on the server.
* **Auto-Ban & WAF Defense:**
    The gateway includes an active Web Application Firewall (WAF) to block SQLi, XSS, and path traversal payloads on forwarded traffic. If an IP exceeds 50 rate-limiting violations, it is **automatically blacklisted** and an administrative notification alert is sent.
* **GDPR-Compliant Auditing:**
    All administrative events are logged in an immutable audit trail. Deleted user data is cryptographically hashed using SHA-256 (Pill/GDPR compliant) to retain system analytics without storing personally identifiable information.
* **Region Latency Reporting:**
    The client measures round-trip time to each advertised gateway at startup in order to choose one, and reports those measurements so edge placement can be judged on latency people actually experience rather than on geography. What is sent is a region name and a round trip in milliseconds — no IP address, no location, and nothing derived from either — and it is aggregated per user per day, so the figures describe people rather than reconnections. Suppress it entirely with `disable_latency_report: true` in the client config, or `LFT_DISABLE_LATENCY_REPORT=1`.

---

## 5. False Positive Context for Security Admins

EDR heuristic warnings are triggered by standard characteristics common to Go runtime compilation:

1. **Statically Linked Go Runtime:** Go compiles its runtime and all dependencies (including raw TCP libraries, WebSockets, and SSH multiplexers) directly into a single "fat" binary. Static signatures for WebSocket multiplexing can trigger heuristic alarms because they mimic the behavior of generic command-and-control (C2) agents.
2. **Chisel Library Overhead:** The core tunneling layer imports `github.com/jpillora/chisel`. While Chisel is a legitimate port-forwarding developer tool, threat intelligence systems often flag generic chisel binaries because bad actors occasionally deploy raw chisel files in post-exploitation scenarios.

**Why `lfr-tunnel` is safe:**
`lfr-tunnel` wraps this engine in an enterprise management plane. It cannot be used as an arbitrary backdoor because:

* Connections must route through your self-hosted corporate gateway domain.
* It requires SSO token validation to establish any link.
* It is restricted to local target ports (like Liferay Tomcat at `8080` or development assets at `3000`).
* It does not allow executing arbitrary terminal commands or remote system shells.

---

### 6. Environment Variable Namespacing & Isolation Standards

To guarantee namespace safety, prevent global shell environment collisions, and maintain total auditing clarity across developer workstations and CI/CD pipelines, all configuration keys and shell installation placeholders are strictly namespaced with the `LFR_TUNNEL_` or `LFT_` prefixes:

| Category | Environment Variable Pattern | Description / Purpose |
|---|---|---|
| **Target Install Paths** | `LFT_INSTALL_DIR` / `LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR` | Installation target directory overrides in `.zshrc` / `.bashrc`. |
| **Authentication & Tokens** | `LFT_TOKEN_FILE` / `LFT_TOKEN` | Developer OIDC / PAT session token storage location. |
| **Target Host Override** | `LFT_TARGET_HOST` | Local target host address (e.g., `127.0.0.1`, `host.docker.internal`). |
| **Build & Signing Ops** | `LFT_MACOS_IDENTITY` / `LFT_SIGN_KEY` | Codesigning identities and certificate parameters for release builds. |

This namespacing guarantees that environment variables exported in shell profiles (`~/.zshrc`, `~/.bashrc`) or pipeline scripts never conflict with system-level variables or third-party CLI tools.

---

## 6. Recommended Action Plan for InfoSec Review

To authorize the tool with minimal impact on local endpoint alerts, we recommend the following steps:

1. **Verify Binary Authenticity:** Check the downloaded executable against the release's `checksums.txt`, and that file against its minisign signature (section 2).
2. **Apply Wildcard Path Exclusions as Alerts exclusions:** Add **both** rules per platform from section 3: the installed binary and the `.ldm/bin` link. A binary-only exclusion is not sufficient: the behavioural and ML engines required the link as well. Use the Alerts type, not Performance Focus; add Interoperability only where testing shows a functional problem, and never as a path's only rule. On Windows the `.ldm\bin` link is a hardlink and needs its own Alerts rule. No package-manager rule is needed.
3. **Do not add signer exclusions.** The signing identities in section 2 are self-signed or project-held keys, not a CA-issued publisher, so trusting them would trust whoever holds the key rather than an organisation.
4. **Configure Docker Sandbox Fallback:** For strict environments where local execution is banned, utilize the Docker wrapper script (`lfr-tunnel.sh` or `lfr-tunnel.ps1`) to run the tunnel client in an isolated container sandbox using the public image **`peterjrichards/lfr-tunnel`** hosted on Docker Hub.


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-23* | *Last Reviewed: 2026-11-23*
