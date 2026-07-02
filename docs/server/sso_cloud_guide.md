# lfr-tunnel Cloud SSO & OIDC Setup Guide

This guide provides step-by-step instructions to configure **Microsoft Entra ID (Azure AD)**, **Google Cloud Identity (OAuth 2.0)**, and **AWS IAM Identity Center (AWS SSO)** as Identity Providers (IdPs) for `lfr-tunneld`. It also covers how to create configuration files to test these integrations locally.

---

## 1. Microsoft Entra ID (Azure AD)

### A. Entra ID App Registration Setup
1. Log in to the [Azure Portal](https://portal.azure.com/) and navigate to **Microsoft Entra ID**.
2. Select **App registrations** from the left panel, and click **New registration**.
3. Fill in the following details:
   - **Name**: `Liferay Tunnel Gateway` (or similar)
   - **Supported account types**: Select your preference (e.g., *Accounts in this organizational directory only* to restrict to your company domain).
   - **Redirect URI**: Select **Web** and enter:
     `https://<your-tunnel-gateway-host>/api/auth/callback?provider=entra-id`
     *(For local testing, use: `http://localhost:8000/api/auth/callback?provider=entra-id`)*
4. Click **Register**.
5. Copy the **Application (client) ID** and **Directory (tenant) ID** from the Overview page.
6. Navigate to **Certificates & secrets** -> **Client secrets** -> **New client secret**.
7. Create a secret, copy the value immediately (it will be masked later).

### B. YAML Configuration Block
Add the following to your `server-config.yaml`:
```yaml
allowed_email_domains:
  - yourcompany.com  # Enforce login only to corporate emails

sso_providers:
  - id: "entra-id"
    name: "Microsoft Entra ID"
    client_id: "YOUR_APPLICATION_CLIENT_ID"
    client_secret: "YOUR_CLIENT_SECRET_VALUE"
    issuer_url: "https://login.microsoftonline.com/YOUR_DIRECTORY_TENANT_ID/v2.0"
    icon: "microsoft"
```

---

## 2. Google Cloud Identity / Google Workspace

### A. Google Cloud Console Setup
1. Log in to the [Google Cloud Console](https://console.cloud.google.com/).
2. Create or select a project.
3. Navigate to **APIs & Services** -> **OAuth consent screen**:
   - Set **User Type** to **Internal** (to restrict logins exclusively to members of your Google Workspace domain) or **External** (open to any Google account).
   - Complete required fields (App name, support email, developer contact).
4. Navigate to **Credentials** -> **Create Credentials** -> **OAuth client ID**.
5. Select **Application type**: **Web application**.
6. Set the following details:
   - **Name**: `Liferay Tunnel Gateway`
   - **Authorized redirect URIs**:
     `https://<your-tunnel-gateway-host>/api/auth/callback?provider=google`
     *(For local testing, use: `http://localhost:8000/api/auth/callback?provider=google`)*
7. Click **Create** and copy the **Client ID** and **Client Secret**.

### B. YAML Configuration Block
Add the following to your `server-config.yaml`:
```yaml
allowed_email_domains:
  - yourworkspace-domain.com

sso_providers:
  - id: "google"
    name: "Google Workspace"
    client_id: "YOUR_GOOGLE_CLIENT_ID.apps.googleusercontent.com"
    client_secret: "YOUR_GOOGLE_CLIENT_SECRET"
    issuer_url: "https://accounts.google.com"
    icon: "google"
```

---

## 3. AWS IAM Identity Center (successor to AWS SSO)

### A. AWS IAM Identity Center Setup
1. Log in to the [AWS Management Console](https://console.aws.amazon.com/) and navigate to **IAM Identity Center**.
2. Select **Applications** -> **Add application**.
3. Under *Application type*, choose **Add custom SAML 2.0 application** or **Add custom OAuth 2.0 / OIDC application**. Select **OAuth 2.0 / OIDC client**.
4. Configure the application settings:
   - **Application Name**: `Liferay Tunnel Gateway`
   - **Redirect URLs**:
     `https://<your-tunnel-gateway-host>/api/auth/callback?provider=aws-idc`
     *(For local testing, use: `http://localhost:8000/api/auth/callback?provider=aws-idc`)*
5. Under *Scopes*, ensure the following scopes are enabled:
   - `openid`
   - `profile`
   - `email`
6. Save and extract the **Client ID**, **Client Secret**, and the **OIDC Issuer URL** (typically of the format `https://oidc.<aws-region>.amazonaws.com/` or `https://identitycenter.amazonaws.com/sso/oidc`).

### B. YAML Configuration Block
Add the following to your `server-config.yaml`:
```yaml
allowed_email_domains:
  - your-aws-org-domain.com

sso_providers:
  - id: "aws-idc"
    name: "AWS Identity Center"
    client_id: "YOUR_AWS_CLIENT_ID"
    client_secret: "YOUR_AWS_CLIENT_SECRET"
    issuer_url: "https://oidc.YOUR_AWS_REGION.amazonaws.com"
    icon: "aws"
```

---

## 4. Local Testing Files & Simulation

To test your SSO integration locally without deploying active infrastructure, you can choose between two methods:

### Option A: Local Keycloak SSO (Recommended)
This uses our existing E2E Keycloak Docker setup to run a local OIDC Identity Provider.

1. **Start the SSO Environment**:
   Run the following command at the repository root:
   ```bash
   make e2e-sso
   ```
   *Note: This spins up a complete Keycloak instance on `http://localhost:8088` pre-configured with the client ID `lfr-tunnel`.*

2. **Add Local Config**:
   Save a file named `server-config-local-sso.yaml` with the following contents:
   ```yaml
   domains:
     - "lfr-demo.local"
   http_bind_addr: "0.0.0.0:8080"
   db_path: "./lfr-tunnel-local-sso.db"
   
   sso_providers:
     - id: "keycloak-local"
       name: "Local Keycloak"
       client_id: "lfr-tunnel"
       client_secret: "secret"
       issuer_url: "http://localhost:8088/realms/liferay"
       icon: "key"
       skip_issuer_check: true
   ```
3. Run the gateway server with this config:
   ```bash
   go run ./cmd/lfr-tunneld -config server-config-local-sso.yaml
   ```

### Option B: Local Mock OIDC Server (Dex)
[Dex](https://github.com/dexidp/dex) is a lightweight open-source OIDC provider that acts as a portal to other user databases. You can run Dex locally using Docker.

1. Create a `dex-config.yaml`:
   ```yaml
   issuer: http://127.0.0.1:5556/dex
   storage:
     type: memory
   web:
     http: 0.0.0.0:5556
   staticClients:
     - id: lfr-tunnel-dex
       redirectURIs:
         - 'http://localhost:8080/api/auth/callback?provider=dex'
       name: 'Liferay Tunnel'
       secret: dex-secret-key
   enablePasswordDB: true
   staticPasswords:
     - email: "developer@lfr-demo.local"
       hash: "$2a$10$w2Og7HzbetZGEs5GHAW5eeIHUIVX9Uqiyz1bT5D.S528B8g1yV.eq" # password: password
       username: "developer"
       userID: "12345"
   ```
2. Start Dex container:
   ```bash
   docker run -d --name dex-mock -p 5556:5556 -v $(pwd)/dex-config.yaml:/etc/dex/config.docker.yaml dexidp/dex:v2.38.0
   ```
3. Configure `server-config.yaml`:
   ```yaml
   sso_providers:
     - id: "dex"
       name: "Dex Mock OIDC"
       client_id: "lfr-tunnel-dex"
       client_secret: "dex-secret-key"
       issuer_url: "http://localhost:5556/dex"
       icon: "key"
   ```

---
*Last Updated: 2026-07-02*  
*Last Reviewed: 2026-07-02*
