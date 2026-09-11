# Privacy Policy & Cookie Disclosure

This Privacy Policy describes how Liferay Tunnel (`lfr-tunnel` / `lfr-tunneld`) processes and secures your data.

As an open-source, developer-first tool, Liferay Tunnel is designed with data minimization in mind. It collects and processes only the absolute minimum amount of data required to establish secure tunnels, prevent abuse, and allow administrators to manage active sessions.

---

## 1. Information Collected & Processed

`lfr-tunneld` (the server gateway) processes three categories of data:

### A. Network & Tunnel Data (Data Plane)
* **IP Addresses**: The server processes the public IP address of the connecting client CLI and any visitor requesting a tunnel subdomain.
  * *Purpose*: This is strictly necessary to route TCP/HTTP packets, enforce rate limiting (DDOS protection), and log security events.
* **Bandwidth & Metrics**: The gateway tracks bytes-in and bytes-out per active tunnel.
  * *Purpose*: Real-time resource monitoring and usage analytics for administrators.
* **Tunnel Port Mappings**: The local development ports (e.g., `8080`, `3000`) being exposed.
  * *Purpose*: Multiplexing connection handshakes.

### B. User Authentication Data (Control Plane)
* **Email Addresses**: Required when registering for access on a gateway or logging in.
  * *Purpose*: Validating account status and sending passwordless Magic Link login emails.
* **Profile Names**: Optional first name, last name, and preferred name.
  * *Purpose*: Branded personalization inside the Admin Dashboard.
* **OIDC SSO Claims**: If the gateway is configured to use Single Sign-On (OIDC/SSO via Google, Keycloak, or Liferay), the server stores the standard profile claims (email, given name, family name) returned by your identity provider.
  * *Purpose*: Zero-friction profile auto-provisioning.

### C. Administrative Audit Logs
* **Security Events**: Actions such as user registration, login attempts, token creations, and administrative status changes are recorded in a local database.
  * *Purpose*: Compliance, auditing, security reviews, and identifying unauthorized connection attempts.

### D. Client Diagnostic Logs (Opt-In Only)
The `lfr-tunnel` client keeps its own logs on **your** machine, in `~/.lfr-tunnel/logs`. They never leave your machine unless you explicitly allow it.

* **What they contain**:
  * `traffic-<subdomain>.log` — one line per request your tunnel proxies: the time, HTTP method, URL path (the query string is **not** recorded), response status, how long it took, the local port it was sent to, and the edge region it arrived from. Request and response bodies are recorded **only** if you start the client with `-log-bodies`, which is off by default.
  * `error-<subdomain>.log` — connection and error events, which can include local hostnames and IP addresses.
  * `client-<subdomain>.log` — the client's own console output when it runs in the background.
* **Sharing them with an administrator**: an administrator can ask for these logs to help diagnose a problem, and they are shared **only if you have turned diagnostic log sharing on**. It is off by default, was never enabled for any existing account, and you can turn it on or off at any time from **Account Settings**. Your setting is checked when the request is made, again before the request is delivered to your client, and once more when the logs arrive — so withdrawing consent takes effect immediately, including for a client that is already running.
* **What is actually sent**: the current copy of each of the three logs above, with secrets removed — personal access tokens, `Authorization` values, credentials in a URL, and anything in a query string that names a token or password. **Request and response bodies are never sent at all**, even if you run the client with `-log-bodies`: they are removed before upload rather than filtered, because no automatic filter can be trusted over your application's own data. What remains of the traffic log is the method, path, status, duration, local port and region of each request.
* **Where they are kept and for how long**: on the gateway, in its database, for **at most 30 days**. They are deleted **immediately** if you withdraw consent — not just "no new logs are collected", the ones already collected are destroyed — and immediately if your account is deleted. Every collection, every upload, and **every time an administrator opens one** is recorded in the administrative audit log.
* **What deletion does not cover**: deleting a bundle removes it from the gateway's live database. The gateway's routine backups, taken before the deletion, still contain it until those backups age out on their own schedule. We are stating this rather than implying deletion is total.
* **Purpose**: Diagnosing routing, connectivity and proxying problems that cannot be reproduced on the gateway.
* **Auditing**: every request for a user's diagnostic logs is recorded in the administrative audit log, including who asked, when, about whom, and requests that were **refused** because consent was absent.

---

## 2. Personal Access Tokens (PATs) & Security
* Tunnels are authenticated using cryptographically secure, random **Personal Access Tokens (PATs)**.
* **On the Server**: PATs are hashed using SHA-256 before being stored in the database. A compromised server database does not expose usable developer PATs.
* **On the Client**: Your PAT is stored locally on your machine (e.g., inside `~/.lfr-tunnel/token`). It is never committed to source control and is only transmitted over secure, TLS-encrypted WebSocket connections (`wss://`).

---

## 3. Cookie Disclosure (Strictly Necessary Cookies)

The Liferay Tunnel Cloud User Portal utilizes **exactly one cookie**:

* **Cookie Name**: `lfr_session`
* **Type**: Session Cookie (Strictly Necessary)
* **Lifetime**: Expires automatically according to the server's configured portal session duration.
* **Security Flags**: Configured with `HttpOnly`, `Secure` (when served over HTTPS), and `SameSite=Lax`.
* **Purpose**: This cookie is strictly necessary to identify and maintain your authenticated session as you navigate the Admin Dashboard or Cloud User Portal. 
* **GDPR Compliance**: Under GDPR and the EU Cookie Directive (ePrivacy), **this cookie is exempt from cookie consent banner prompts** because it is strictly necessary to provide the service requested by the user.

---

## 4. GDPR Right to Be Forgotten & Anonymisation

In accordance with General Data Protection Regulation (GDPR) standards, Liferay Tunnel supports an immediate, auditable, and fully automated **Right to Be Forgotten**:

*   **Self-Initiated Deletion**: Users can permanently delete their accounts at any time from the **Danger Zone** inside their Account Settings tab on the dashboard.
*   **Admin-Initiated Deletion**: Administrators can execute complete account deletions on behalf of developers directly from the administrative panel.
*   **The Purge & Anonymisation Protocol**:
    1.  All Personal Access Tokens (PATs) and active session cookies are permanently revoked and deleted from disk.
    2.  Any active WebSocket tunnel connections are forcefully closed and disconnected.
    3.  The user's actual profile record (First Name, Last Name, email, and preferences) is permanently deleted from the database.
    4.  To preserve historical system metrics and auditing trails without violating privacy, any associated logs and bandwidth metrics in `tunnel_metrics`, `tunnel_audit_logs`, and `admin_audit_log` are **permanently obfuscated and anonymised** using a secure SHA-256 hash of their email address (e.g., `gdpr-deleted-user-hash123`).
    5.  The diagnostic-log-sharing preference is part of the profile record and is deleted with it. No diagnostic log content is stored on the gateway.

---

## 5. Host-Your-Own (Sovereign) Policy Customisation

Liferay Tunnel is fully self-hostable. If you run your own private instance of `lfr-tunneld`:
* You are the sole Data Controller of your database. No data is ever transmitted to Liferay, Peter Richards, or any external third-party server.
* The gateway's default built-in web portal serves these standardized, generic disclosures at `/privacy` and `/cookies` automatically.
* You can easily override these legal footers to point to your company's own custom disclosures using the `privacy_policy_url` and `cookie_policy_url` fields inside your `/etc/lfr-tunneld/server-config.yaml`.


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-11* | *Last Reviewed: 2026-09-11*
