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

## 4. Host-Your-Own (Sovereign) Policy Customisation

Liferay Tunnel is fully self-hostable. If you run your own private instance of `lfr-tunneld`:
* You are the sole Data Controller of your database. No data is ever transmitted to Liferay, Peter Richards, or any external third-party server.
* The gateway's default built-in web portal serves these standardized, generic disclosures at `/privacy` and `/cookies` automatically.
* You can easily override these legal footers to point to your company's own custom disclosures using the `privacy_policy_url` and `cookie_policy_url` fields inside your `/etc/lfr-tunneld/server-config.yaml`.
