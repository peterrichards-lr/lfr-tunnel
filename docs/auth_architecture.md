# lfr-tunnel Token Lifecycle & OAuth2 SSO Integration Architecture

This document describes the technical architecture, database schema, API endpoints, and sequence flows required to migrate `lfr-tunnel` from a single shared authentication token to a secure, multi-tenant system with **OAuth2 Liferay SSO**, **per-user Personal Access Tokens (PATs)**, and **Role-Based Access Control (RBAC)**.

---

## 1. Core Architecture Overview

```mermaid
graph TD
    subgraph DevMachine ["Developer Machine"]
        CLI["lfr-tunnel CLI"]
        Browser["System Browser"]
    end

    subgraph GWServer ["Gateway Server (lfr-tunneld)"]
        API["Gateway Web Server"]
        DB["SQLite / PostgreSQL"]
        Chisel["Embedded Chisel Server"]
    end

    subgraph IdP ["Identity Provider"]
        SSO["Liferay Portal SSO / OAuth2"]
    end

    CLI -->|"1. lfr-tunnel login"| API
    API -->|"2. Redirect"| Browser
    Browser -->|"3. Authenticate"| SSO
    SSO -->|"4. Auth Code"| API
    API -->|"5. Exchange Code & Sync User"| SSO
    API -->|"6. Write User & Token"| DB
    API -->|"7. Return PAT"| CLI
    CLI -->|"8. Register Tunnel (with PAT)"| API
    API -->|"9. Validate PAT"| DB
    API -->|"10. Authorize Session"| Chisel
```

---

## 2. Database Schema (User, Roles, and Tokens)

To support this multi-user capability, the server gateway utilizes a lightweight persistent relational database (such as **SQLite** for zero-config deployments, or **PostgreSQL** for scalable production systems).

### SQL Table Schema

```sql
-- Users table storing profile data and registration states
CREATE TABLE users (
    id VARCHAR(64) PRIMARY KEY,          -- Unique user ID (e.g. Liferay user uuid or email)
    email VARCHAR(255) UNIQUE NOT NULL,
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    role VARCHAR(20) NOT NULL DEFAULT 'user', -- 'admin' or 'user'
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- 'pending', 'approved', 'revoked'
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Personal Access Tokens (PATs) table for client connections
CREATE TABLE personal_access_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id VARCHAR(64) NOT NULL,
    token_hash VARCHAR(64) UNIQUE NOT NULL, -- SHA-256 hash of the generated token string
    token_prefix VARCHAR(10) NOT NULL,       -- Visible prefix (e.g., lfr_pat_abcd) for display in Admin UI
    name VARCHAR(100) NOT NULL,              -- Friendly label (e.g., "Macbook Pro", "Jenkins Agent")
    expires_at TIMESTAMP NULL,               -- Optional token expiration date
    revoked_at TIMESTAMP NULL,               -- Revocation timestamp (null if active)
    last_used_at TIMESTAMP NULL,             -- Audit tracking for last active connection
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Audit log of active and historical tunnel leases
CREATE TABLE tunnel_audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id VARCHAR(64) NOT NULL,
    subdomain_prefix VARCHAR(100) NOT NULL,
    ports TEXT NOT NULL,                     -- Comma-separated list of mapped ports
    remote_ip VARCHAR(45) NOT NULL,
    connected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disconnected_at TIMESTAMP NULL,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
);
```

---

## 3. Developer Self-Registration & Admin Approval Flow (Pre-SSO)

Before Liferay SSO is fully integrated, developers can request access directly via the gateway. To prevent unauthorized use, all registration requests must go through an email-based administrative approval flow.

### Sequence Flow Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Dev as "Developer"
    participant GW as "Gateway (lfr-tunneld)"
    actor Admin as "Gateway Administrator"
    
    Dev->>GW: "Visits /register (Enters Email, Name, Subdomain request)"
    Note over GW: Creates User in DB with status='pending'
    GW->>Admin: "Email Notification: New registration request (Contains approval token links)"
    
    Admin->>GW: "Clicks Approve Link (GET /admin/approve?user=dev&token=xyz)"
    Note over GW: Validates approval token<br/>Updates status='approved'<br/>Generates Personal Access Token (PAT)
    
    GW->>Dev: "Email Notification: Registration Approved! (Contains link to claim PAT)"
    Dev->>GW: "Visits /claim?token=abc to download PAT"
    Note over Dev: Configures PAT in local ~/.lfr-tunnel/config.yaml
```

### 3.1. Key Steps in the Approval Flow
1.  **Request Submission**: The developer visits the public landing page `/register` and submits their details.
2.  **Admin Alert Email**: The server fires a transactional email to the configured administrator's address. The email contains:
    *   Developer Name and Email.
    *   Requested subdomain prefix.
    *   A secure approval link: `https://tunnel.lfr-demo.se/admin/approve?user=developer@liferay.com&token=[SecureRandomApprovalToken]`
3.  **Approval Validation**: When the admin clicks the link, the server verifies the approval token against the database. If it matches, the user is transitioned to `approved` status, and a unique PAT is generated.
4.  **Developer Delivery Email**: The server emails the developer containing a link to download their token securely or complete their CLI setup.

---

## 4. OAuth2 Authorization Code Flow with PKCE (Future Phase)

Once Liferay SSO is available, this flow will replace the manual approval process. Developers will authenticate directly using Liferay Portal.

### Login Flow Sequence

```mermaid
sequenceDiagram
    autonumber
    actor Dev as "Developer"
    participant CLI as "lfr-tunnel CLI"
    participant Browser as "Default Browser"
    participant GW as "Gateway (lfr-tunneld)"
    participant SSO as "Liferay SSO (Auth Server)"

    Dev->>CLI: "lfr-tunnel login"
    Note over CLI: CLI starts local server on http://localhost:4444/callback<br/>Generates PKCE Code Verifier & Challenge
    CLI->>Browser: "Open system browser to Gateway SSO Portal"
    Browser->>GW: "GET https://tunnel.lfr-demo.se/auth/login?challenge=xxx"
    GW->>SSO: "Redirect: /o/oauth2/authorize?client_id=...&code_challenge=xxx"
    Browser->>SSO: "User logs in & approves scopes"
    SSO-->>Browser: "Redirect back to Gateway: https://tunnel.lfr-demo.se/auth/callback?code=yyy"
    GW->>SSO: "POST /o/oauth2/token (code=yyy, client_secret)"
    SSO-->>GW: "Access Token & ID Token (User profile info)"
    
    Note over GW: Resolves user email.<br/>If first user or marked in config -> Set Role = 'admin'<br/>Saves/updates User in SQLite database
    
    Note over GW: Generate Personal Access Token (PAT)<br/>Format: lfr_pat_[SecureRandomBytes]<br/>Saves SHA-256 hash of token to DB
    
    GW-->>Browser: "Redirect to: http://localhost:4444/callback?token=lfr_pat_..."
    Browser->>CLI: "Delivers PAT to local HTTP Listener"
    Note over CLI: CLI saves token to ~/.lfr-tunnel/config.yaml<br/>CLI shuts down local HTTP server
    CLI-->>Dev: "Print 'Login Successful! Token saved to config.'"
```

---

## 5. Outbound Email Configuration (Local Postfix vs. External SMTP Relay)

To support sending transactional emails (such as request notifications to the admin and approval emails to the developers) securely, the server connects to an outbound mail relay.

The server supports two configuration modes:
1.  **Local MTA (Null Client)**: Connecting to `127.0.0.1:25` where a local Postfix server is configured to deliver mail.
2.  **External SMTP Relay**: Connecting to an external email provider (such as Gmail, AWS SES, or Liferay's Google Workspace SMTP) using TLS.

### Server SMTP Configuration (`server-config.yaml`)

```yaml
smtp_host: "localhost"              # SMTP Server address (e.g. localhost or smtp.gmail.com)
smtp_port: 25                       # SMTP Port (e.g. 25, 587 for STARTTLS, or 465 for SSL)
smtp_username: ""                   # SMTP Username (leave empty for local Postfix)
smtp_password: ""                   # SMTP Password
smtp_from_address: "Liferay Tunnel <noreply@lfr-demo.se>"
admin_notification_email: "admin@lfr-demo.se"
```

---

## 6. API Specification & Integration Points

### Control Plane REST API

The gateway server exposes the following endpoints:

#### 1. Registration (`POST /api/register`)
Exchanges a PAT for a dynamic Chisel tunnel lease.
*   **Request Payload**:
    ```json
    {
      "subdomain_prefix": "alpha-se",
      "ports": [
        { "local_port": 8080, "name_suffix": "" },
        { "local_port": 3001, "name_suffix": "react" }
      ],
      "personal_access_token": "lfr_pat_dev_8a7d9f2e4b6c8d0e"
    }
    ```
*   **Server Logic**:
    1. Hashes incoming token: `sha256("lfr_pat_dev_8a7d9f2e4b6c8d0e")`.
    2. Queries database: `SELECT * FROM personal_access_tokens WHERE token_hash = ?`.
    3. Validates that the associated user's status is `approved` and the token is not expired/revoked.
    4. Registers the tunnel lease on Chisel.

---

### Administrative Control Plane API (Admins Only)

These endpoints require an administrative session or an administrative token.

#### 1. List Users (`GET /api/admin/users`)
*   **Response**:
    ```json
    [
      {
        "id": "admin",
        "email": "admin@lfr-demo.se",
        "role": "admin",
        "status": "approved"
      }
    ]
    ```

#### 2. Modify User Role / Status (`POST /api/admin/users/:id`)
*   **Request Payload**:
    ```json
    {
      "role": "admin",
      "status": "revoked"
    }
    ```
*   **Logic**: Updates the user status. If status is set to `revoked`, immediately closes all corresponding active WebSocket connections.

---

## 7. Security & Isolation Measures

1.  **Token Hashing (At Rest Security)**:
    Only SHA-256 hashes of generated tokens (`token_hash`) are stored in configurations or databases. If the database/configuration files on the server are compromised, attackers cannot reconstruct the tokens.
2.  **Active Connection Termination**:
    When an admin revokes a token or deactivates a user, the gateway server sweeps all active Chisel sessions and immediately terminates any corresponding WebSockets.
3.  **Bootstrap Admin Role**:
    An environment variable `LFT_BOOTSTRAP_ADMIN` can be set. When this email logs in for the first time via Liferay SSO, the system automatically marks them as `admin`.

---

## 8. Implementation & Transition Status

1.  **[x] Database Integration**: Completed SQLite database system containing users (with registration states), audit logs, and tokens.
2.  **[x] SMTP Integration**: Completed outbound mail sender logic (`pkg/mail/mail.go`) to handle registration notifications, magic links, and approval emails.
3.  **[x] Registration and Approval API**: Completed `/register-request` flow, verification links, approval validation endpoints, and token claiming.
4.  **[x] SSO Endpoints**: Completed fully configuration-driven OpenID Connect (OIDC) login and callback routing with PKCE to support Google, Keycloak, or Liferay OAuth2.

---

## 9. Email Domain Whitelisting & Secure Registration Filters

To prevent open-relay abuse, spam registration, and credential stuffing on public-facing gateways, `lfr-tunneld` implements strict, multi-layered email validation using domain whitelists and denylists.

### Configuration (`server-config.yaml`)
```yaml
allowed_email_domains:
  - "liferay.com"
  - "lfr-demo.se"
  - "lfr-demo.online"
```

### Enforcement Logic & Sequence
When a user attempts to self-register via `/api/register-request` or log in through an OIDC Single Sign-On (SSO) provider:

1. **Domain Extraction**: The gateway extracts the domain portion of the email address (e.g., `user@domain.com` ──► `domain.com`).
2. **Whitelist Validation**: The gateway checks the domain against the `allowed_email_domains` slice.
3. **Strict Rejection**: If the domain is not present in the whitelist:
   * **Self-Registration**: The request is instantly blocked with a `400 Bad Request`.
   * **OIDC SSO Provider**: If a user successfully logs in via Google/Keycloak but their email domain is not whitelisted, the gateway denies session creation and returns an unauthorized error.
4. **Anti-Enumeration Protections**: To prevent malicious actors from guessing whitelisted user accounts or email lists, the gateway fails silently or returns identical neutral responses for verification/login attempts on un-whitelisted domains, preventing user enumeration attacks.

---

## 10. Passwordless Magic Link Authentication

The Cloud User Portal and the Administration Dashboard discard static passwords entirely, instead utilizing a state-of-the-art **Passwordless Magic Link** authentication model. This eliminates password-guessing attacks, brute-force vectors, and database credential-leak risks.

### The Magic Link Lifecycle

```
[ User Portal Login ]
        │
        │ 1. Enter email address (e.g. user@liferay.com)
        ▼
[ Gateway (lfr-tunneld) ]
        │
        │ 2. Verify email domain is whitelisted
        │ 3. Generate secure, cryptographically random login token
        │ 4. Persist token hash to DB with short-lived expiration (e.g. 10m)
        ▼
[ Outbound Email (SMTP) ] ──► Transactional Email delivered to Inbox
                                  │
                                  ├─► Click "Verify & Log In" Link
                                  │
                                  └─► Click "Report Abuse" Link
```

### Key Security & Concurrency Properties

* **Single-Use Invalidation**: A magic link token can only be consumed once. The moment a user clicks the login link and a session is validated, the token is instantly flagged as consumed and deleted or invalidated in the database.
* **Single-Session Concurrency**: To prevent token sharing and protect administrative control channels, logging into the portal from a new browser/device automatically revokes all other active sessions for that specific user.
* **Anti-Abuse Reporting (Zero-Day Protection)**:
  * Every magic link transactional email includes a prominent **"Report Abuse"** link at the footer.
  * If a user receives a magic link they did not request (indicating someone is attempting to brute-force or guess their account), clicking the link immediately:
    1. Revokes the associated login token instantly.
    2. Writes a critical `portal.magic_link_abuse_reported` event to the server's persistent audit log.
    3. Triggers administrative alerts with the origin IP address to blacklist or ban the attacker.
* **Sliding Session Expiration**: Active portal sessions are validated against a sliding window (e.g., 30 minutes). If inactive, the session expires naturally, requiring a fresh login.
