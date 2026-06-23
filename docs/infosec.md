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
    To satisfy strict corporate compliance and prevent bypass channels, the gateway supports a `disable_email_login` configuration flag. When active, it disables email-based magic link logins and registration requests globally at both the API and interface levels. This locks the application down so that federated OIDC corporate SSO is the **exclusive** method of entry.
* **Single-Session Concurrency:**
    Lease mapping enforces strict single-session concurrency per token or subdomain. A token or reserved subdomain cannot be used to multiplex simultaneous connections across multiple machines, preventing credentials from being shared or hijacked.

---

## 2. Code Signing & Trust Verification Metadata

To support **Exclusion by Digital Subject (Publisher)** in EDR consoles (such as SentinelOne, Microsoft Defender, and CrowdStrike), release binaries are cryptographically signed.

> [!IMPORTANT]
> **Key Management & Secrets Security:** All private keys, certificates, and passphrases used during the build and codesigning cycles are stored securely inside a corporate **1Password vault**. The release scripts retrieve these secrets dynamically at runtime via the 1Password CLI (`op`), ensuring no credentials or key files are hardcoded in the codebase or stored in plain text on build systems.

### macOS (Apple Developer ID)

* **Publisher Common Name (CN):** `Developer ID Application: <Your Developer Name> (TEAMID)` *(Note: Admin should verify the exact CN generated during local build signing)*
* **Apple Team ID:** `[Insert Apple Developer Team ID here]`
* **Verification Command:**

    ```bash
    codesign -dv --verbose=4 ~/runningpoc/bin/lfr-tunnel
    ```

### Windows (Authenticode Certificate)

* **Publisher Common Name (CN):** `CN=[Insert Organization/Publisher Name here]`
* **Certificate Thumbprint (SHA-256):** `[Insert Certificate Thumbprint here]`
* **Windows Publisher CA:** Enterprise CA (or [SignPath Foundation](https://signpath.org) for open-source releases).

### Linux (GPG Detached Signatures)

* **GPG Public Key ID:** `[Insert GPG Key ID here]`
* **Signature Verification:** Linux binaries are distributed alongside detached GPG signatures (`.asc` files) generated during the release process:

    ```bash
    gpg --verify lfr-tunnel-linux-amd64.asc lfr-tunnel-linux-amd64
    ```

### Supply-Chain Attestation (Sigstore & GitHub OIDC)

Every release binary is covered by a **GitHub Artifact Attestation**—a tamper-proof record in the Rekor transparency log. This confirms that the binary was built directly in the official GitHub repository (`<your-github-org>/lfr-tunnel`) from audited source commits.

* **Verification Command (requires GitHub CLI):**

    ```bash
    gh attestation verify ~/runningpoc/bin/lfr-tunnel --repo <your-github-org>/lfr-tunnel
    ```

---

## 3. Standard Execution Paths & EDR Exclusions

To simplify EDR administration and avoid wide path exclusions, the project enforces **standardized canonical installation paths** rather than random temp paths or multiple package-manager paths:

| Platform | Standard Canonical Path | EDR Console Path Exclusions (Recommended) |
|---|---|---|
| **macOS** | `/Users/<username>/runningpoc/bin/lfr-tunnel` | `/Users/*/runningpoc/bin/lfr-tunnel` |
| **Windows** | `C:\Users\<username>\runningpoc\bin\lfr-tunnel.exe` | `C:\Users\*\runningpoc\bin\lfr-tunnel.exe` |
| **Linux** | `/home/<username>/runningpoc/bin/lfr-tunnel` | `/home/*/runningpoc/bin/lfr-tunnel` |

> [!NOTE]
> Standardizing client downloads on the gateway ensures that developers use these specific directories instead of homebrew/scoop paths, making a single wildcard rule sufficient.

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

## 6. Recommended Action Plan for InfoSec Review

To authorize the tool with minimal impact on local endpoint alerts, we recommend the following steps:

1. **Verify Binary Authenticity:** Verify the checksum and GitHub OIDC attestation for the downloaded executables using the `gh attestation verify` command.
2. **Apply Code Signing Exceptions:** Add the Apple Team ID / Developer ID CN and Windows Certificate CN to your EDR's trusted publisher list.
3. **Apply Wildcard Path Exclusions:** Add the standardized installation path exclusions (`/Users/*/runningpoc/bin/lfr-tunnel` or `C:\Users\*\runningpoc\bin\lfr-tunnel.exe`) to the EDR profile.
4. **Configure Docker Sandbox Fallback:** For strict environments where local execution is banned, utilize the Docker wrapper script (`lfr-tunnel.sh` or `lfr-tunnel.ps1`) to run the tunnel client in an isolated container sandbox using the audited public image **`your-docker-hub-user/lfr-tunnel`** (or `peterjrichards/lfr-tunnel` as a template) hosted on Docker Hub.
