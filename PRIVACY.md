# Privacy Policy & Cookie Disclosure

This Privacy Policy describes how Liferay Tunnel (`lfr-tunnel` / `lfr-tunneld`) processes and secures your data.

As an open-source, developer-first tool, Liferay Tunnel is designed with data minimization in mind. It collects and processes only the absolute minimum amount of data required to establish secure tunnels, prevent abuse, and allow administrators to manage active sessions.

---

## 1. Information Collected & Processed

`lfr-tunneld` (the server gateway) processes the following categories of data:

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

### E. Anonymous Geographic Distribution (Operator-Enabled, Off by Default)

A gateway operator may configure a geo-IP database (`geolite2_db_path`) so that administrators can see, on the admin analytics page, **how many distinct users registered from each country during the current ISO week**. No geo-IP database is shipped with the gateway and none can be — every vendor forbids redistribution — so this is **off unless an operator has deliberately obtained and deployed one**, and off is the default and a fully supported state.

Where it is enabled, this is a **further purpose for the IP address already described in §1.A**, not a new collection. What it does and does not do:

* **What is derived**: at the moment you register a tunnel, your public IP address is looked up in the operator's local database and reduced to a **two-letter ISO country code**. Only the country field of the record is read; city, subdivision and latitude/longitude are never decoded, even when the operator's database contains them.
* **The address is discarded**: the lookup happens in memory and the address is dropped immediately afterwards. Your address and your country are never written together, never logged together, and there is no route, report or export that can return the pair. This feature stores nothing about your address that §1.A does not already describe.
* **What is stored**: one row per country per ISO week, holding only the country code and a **count**. The table has no user column, and no lookup is performed against a third-party service — the database is a local file.
* **How "distinct users" is counted without keeping who**: within the current week the gateway keeps, in memory only, a set of digests of the account identifiers already counted for each country, purely so you are not counted twice. That set is never written to disk, never returned by any part of the code, and is discarded when the week rolls over and when the gateway process stops.
* **A minimum of 5 users per country**: a country is only recorded at all once **at least 5 distinct users** have been seen there in that week. Below that it is folded into a single `OTHER` bucket, and `OTHER` itself is recorded only once it too reaches 5. The threshold is applied **before the count is written**, not when the page is drawn, so a below-threshold country does not exist in the database to be read by anyone holding the file.
* **Addresses that cannot be placed are dropped, not guessed**: private ranges, carrier-grade NAT and addresses absent from the database are discarded rather than counted under a catch-all, so an unknown location never becomes a geographic claim.
* **Who can see it**: the resulting counts are visible only to gateway administrators.
* **Retention**: these weekly counts are aggregate figures that contain no identifier of any kind, so they are kept as historical metrics and are not deleted on a schedule. For the same reason, deleting your account (§4) neither removes nor needs to remove them — there is nothing in a row that refers to you.
* **Purpose**: helping an administrator understand, in aggregate, where the gateway is being used from — for example when deciding where to place an edge node.

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
*Last Updated: 2026-09-16* | *Last Reviewed: 2026-09-16*
