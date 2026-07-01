# Liferay SE Team — Quick-Start Guide

[![Better Stack Badge](https://uptime.betterstack.com/status-badges/v2/monitor/2q3sj.svg)](https://uptime.betterstack.com/?utm_source=status_badge)

This guide is for **Liferay Sales Engineering team members** connecting to the shared, Liferay-operated `lfr-tunnel` gateway. It covers installation, authentication, and usage examples with the real team domains.

For general architecture, self-hosting instructions, and configuration reference, see the main [README](../README.md) and [Architecture Guide](architecture.md).

---

## Hosted Gateway Details

| Property | Value |
|---|---|
| **Gateway URL** | `https://tunnel.lfr-demo.se` |
| **Primary Domain** | `lfr-demo.se` |
| **Secondary Domain** | `lfr-demo.online` |
| **Registration** | Self-service with admin approval (see below) |

Your tunnel will be reachable at:
- `https://<your-subdomain>.lfr-demo.se`
- `https://<your-subdomain>.lfr-demo.online`

---

## Step 1: Install the Client

> [!WARNING]
> **EDR Whitelist Constraint (SentinelOne):** Do NOT install the client using package managers like Homebrew or Scoop. These managers install binaries outside of whitelisted execution paths (e.g. in `/opt/homebrew/bin` or custom scoop buckets), which will trigger local EDR alarms and kill your terminal. 
> 
> You must use either **Method A (Native Installer)** or **Method B (Docker Container)** to run the client safely.

### ~~Method A: Native Installer Script (Recommended Native)~~

> [!IMPORTANT]
> **Status Note:** We are currently talking with the **Liferay Security Teams** to get this native execution option fully approved and whitelisted. In the meantime, this option is temporarily disabled. **Please use Method B (Docker Container) below for all client operations.**

~~This script downloads the officially signed client binary directly into the EDR-whitelisted canonical installation directory (`~/runningpoc/bin/lfr-tunnel` or `C:\Users\<username>\runningpoc\bin\lfr-tunnel.exe`):~~

* ~~**macOS / Linux:** `curl -sSfL https://tunnel.lfr-demo.se/install.sh | sh`~~
* ~~**Windows (PowerShell):** `iwr https://tunnel.lfr-demo.se/install.ps1 | iex`~~

~~To verify your installation, open a new terminal window and run: `lfr-tunnel -version`~~

### Method B: Standalone Docker Container (EDR Immune)
For environments where local native binary execution is completely restricted, run our pre-built, multi-architecture client container directly from Docker Hub. This requires **zero local installation, zero compilation, and is 100% immune to EDR host-level blocks**:

```bash
docker run -d --name lfr-tunnel \
  -e LFT_CLIENT_TOKEN="YOUR_PERSONAL_ACCESS_TOKEN" \
  peterjrichards/lfr-tunnel:latest \
  -server https://tunnel.lfr-demo.se \
  -subdomain your-name-se \
  -ports 8080
```

---

## Step 2: Register for Access

Access to the shared gateway is controlled by a **Personal Access Token (PAT)**. To get one:

1. **Submit a registration request** to the gateway:
   ```bash
   curl -s -X POST \
     -H "Content-Type: application/json" \
     -d '{"email": "your.name@liferay.com", "requested_subdomain": "your-name-se"}' \
     https://tunnel.lfr-demo.se/api/register-request
   ```
   You will receive a confirmation that your request is pending admin approval.

2. **Wait for an approval email** sent to your `@liferay.com` address. The Liferay SE gateway administrator will review and approve your request.

3. **Claim your Personal Access Token** using the link in the approval email, or run:
   ```bash
   curl -s "https://tunnel.lfr-demo.se/api/claim?token=<claim-token-from-email>"
   ```
   This returns a PAT in the format `lfr_pat_...`. **Copy it now** — it is only shown once.

---

## Step 3: Authenticate and Store Your Token

Save your PAT securely so the client loads it automatically on every run without needing any `-token` flags. There are two ways to do this:

### Option A: Automatic Browser Login (Highly Recommended)
The client includes an interactive **Magic Handoff** flow that automatically completes token generation and saves it to your configuration directory with zero manual copying:

1. In your terminal, run the login command:
   ```bash
   lfr-tunnel login
   ```
2. Your default web browser will open to the gateway's **User Portal**.
3. Authenticate on the portal (using your `@liferay.com` email and magic link).
4. Upon logging in, the portal will securely hand off a newly generated token back to your local client terminal session and save it automatically:
   ```
   ✅ Successfully authenticated! Your token has been saved securely to ~/.lfr-tunnel/token
   ```

### Option B: Manual Clipboard Configuration
If you prefer to save your claimed token manually:

```bash
mkdir -p ~/.lfr-tunnel
echo "lfr_pat_your-token-here" > ~/.lfr-tunnel/token
chmod 600 ~/.lfr-tunnel/token
```

The client will now authenticate with this token automatically.

> [!CAUTION]
> **Never commit your PAT to source control.** The `.env` file (used by the Docker wrapper) is git-ignored, and `~/.lfr-tunnel/token` is outside your workspace. Keep it there.

---

## Step 4: Check Available Subdomains

Before picking a subdomain, you can check availability:

```bash
curl -s -H "Authorization: Bearer lfr_pat_your-token-here" \
  "https://tunnel.lfr-demo.se/api/check-subdomain?subdomain=your-name-se&domain=lfr-demo.se"
```

If the subdomain is taken, the server will suggest alternatives.

---

## Step 5: Advanced Usage & Integration Runtimes

Depending on how you run Liferay and client extensions locally, use the appropriate examples below for the **Native Client** or the **Docker Client**.

### 1. LDM (Liferay Development Manager) & Workspace Client Extensions
If you are developing Liferay Client Extensions (CX) in a Liferay Workspace, the tunnel automatically scans `client-extension.yaml` files, maps ports (e.g., `8080` for portal and `3001` for assets), and prints clickable public URLs.

* **Using the Native Client:**
  Navigate to your Liferay Workspace root and run:
  ```bash
  lfr-tunnel -subdomain your-name-se
  ```
* **Using the Docker Client:**
  Configure LDM to invoke the Docker client container by setting the runtime variable:
  ```bash
  LDM_TUNNEL_RUNTIME=docker ldm tunnel -subdomain your-name-se
  ```
  *(Note: LDM coordinates the container bindings automatically, scanning your workspace files and mounting paths as needed).*

#### Client Extension Naming & Routing Convention (For LDM Configuration)
When client extensions are exposed via the tunnel, the gateway assigns them URLs using a **hyphenated prefix format**:
`https://[tunnel-subdomain]-[extension-id].[domain]` 

*(Example: `https://your-name-se-ai-commerce.lfr-demo.online`)*

**Why this format?**
1. **Wildcard SSL Constraints:** The gateway uses standard Wildcard SSL certificates (`*.lfr-demo.online`). These certificates strictly protect only *one level* of subdomains. If we used nested domains like `extension.pjrtest.domain.com`, the browser would block the connection with an SSL/TLS security error. 
2. **Alphabetical Grouping:** By placing the `[tunnel-subdomain]` first, all your Liferay environments and associated client extensions are cleanly grouped together in dashboards, DNS logs, and browser auto-complete history.
3. **Collision Clarity:** If multiple developers run an extension with the same ID, the prefix makes ownership immediately obvious (`pjrtest-ai-commerce` vs `jdoe-ai-commerce`).

**LDM Prompt Helper:**
If you use an AI assistant to configure your LDM workspace or client extensions, you can provide this prompt to align its configuration:
> "The `lfr-tunnel` public gateway uses a specific routing convention to support Wildcard SSL certificates. When generating or expecting public URLs for client extensions, the tunnel flattens the subdomain using a hyphen instead of nesting it. It uses the format `[tunnel-subdomain]-[extension-id].domain.com` (e.g., `https://pjrtest-my-extension.lfr-demo.online`). Please ensure your local CORS configurations and LDM properties expect this hyphenated format for all client extension public URLs."

### 2. Standalone Liferay Tomcat Bundle (Running on Host)
Use these configurations to expose a standard Liferay bundle unzipped and running natively on your host machine (e.g. at `http://localhost:8080`).

* **Using the Native Client:**
  Directly target your Tomcat port:
  ```bash
  lfr-tunnel -subdomain dev-tomcat -ports 8080
  ```
  *Then, configure Liferay Virtual Host: Log in to `http://localhost:8080` → **Control Panel → Instance Settings → Virtual Hosts** and set Virtual Host to: `dev-tomcat.lfr-demo.se`.*

* **Using the Docker Client:**
  To route traffic from the Docker container back out to the host loopback port `8080`, utilize **`host.docker.internal`**:
  * **macOS / Windows:**
    ```bash
    docker run -d --name lfr-tunnel \
      -e LFT_CLIENT_TOKEN="lfr_pat_your-token" \
      -e LFT_TARGET_HOST="host.docker.internal" \
      peterjrichards/lfr-tunnel:latest \
      -server https://tunnel.lfr-demo.se \
      -subdomain dev-tomcat \
      -ports 8080
    ```
  * **Linux:**
    ```bash
    docker run -d --name lfr-tunnel \
      --add-host=host.docker.internal:host-gateway \
      -e LFT_CLIENT_TOKEN="lfr_pat_your-token" \
      -e LFT_TARGET_HOST="host.docker.internal" \
      peterjrichards/lfr-tunnel:latest \
      -server https://tunnel.lfr-demo.se \
      -subdomain dev-tomcat \
      -ports 8080
    ```

### 3. Exposing a Liferay Docker Container or Custom Image
Use these configurations if Liferay itself is running as a Docker container.

* **Using the Native Client:**
  If your Liferay container has port `8080` published on the host (`-p 8080:8080`), you can target it natively:
  ```bash
  lfr-tunnel -subdomain dev-docker -ports 8080
  ```
  If it runs behind a local domain name or Nginx reverse proxy (e.g., `my-project.local` on port `80`), specify the target host:
  ```bash
  lfr-tunnel -ports 80 -target-host my-project.local
  ```

* **Using the Docker Client (Docker Compose Integration):**
  Integrate the tunnel container directly into your Liferay `docker-compose.yml` to route traffic over the internal Docker network using container service names (no host ports need to be published):
  ```yaml
  version: "3.8"
  services:
    # Your Liferay service container
    liferay:
      image: liferay/portal:7.4.3.112-ga112
      # Ports do not need to be published on the host!

    # Tunnel client service container
    tunnel:
      image: peterjrichards/lfr-tunnel:latest
      environment:
        - LFT_CLIENT_SERVER=https://tunnel.lfr-demo.se
        - LFT_CLIENT_TOKEN=lfr_pat_your-token
        - LFT_CLIENT_SUBDOMAIN=my-liferay-demo
        - LFT_TARGET_HOST=liferay  # Resolves to the liferay service container
        - LFT_CLIENT_PORTS=8080
  ```

---

## Running in the Background (Native Client Only)

```bash
# Start in the background
lfr-tunnel -background -subdomain your-name-se

# Check connection status
lfr-tunnel -status

# Stop cleanly
lfr-tunnel -stop
```

---

## Keeping the Client Up to Date

```bash
lfr-tunnel -upgrade
```
This fetches the latest release from GitHub, verifies the SHA256 checksum, and replaces the binary in place.

---

## Alternative Sharing Providers (ngrok, cloudflared)

If you need to share your local environment using an alternative provider, Liferay SEs have a choice of using **ngrok** or **Cloudflare Tunnels (cloudflared)** alongside `lfr-tunnel`.

Below are usage examples for both options:

### 1. Using Cloudflare Tunnels (cloudflared)
Cloudflare Tunnels are free, highly reliable, and generally EDR-compliant since the `cloudflared` binaries are officially signed by Cloudflare.

* **Using the Native Client:**
  1. Install `cloudflared` on your system.
  2. Start a tunnel pointing directly to Liferay (port 8080):
     ```bash
     cloudflared tunnel --url http://localhost:8080
     ```
  3. Copy the random public `.trycloudflare.com` URL generated in your terminal to share your demo.

* **Using the Docker Client:**
  Run the official Cloudflare tunnel container mapping to your host's Liferay:
  * **macOS / Windows:**
    ```bash
    docker run -it --name cloudflare-tunnel \
      cloudflare/cloudflared:latest tunnel --url http://host.docker.internal:8080
    ```
  * **Linux:**
    ```bash
    docker run -it --name cloudflare-tunnel \
      --add-host=host.docker.internal:host-gateway \
      cloudflare/cloudflared:latest tunnel --url http://host.docker.internal:8080
    ```
  * **Docker Compose Network Integration:**
    Add it directly to your Liferay `docker-compose.yml` to route over the internal network using service names:
    ```yaml
    services:
      liferay:
        image: liferay/portal:7.4.3.112-ga112
      tunnel:
        image: cloudflare/cloudflared:latest
        command: tunnel --url http://liferay:8080
    ```

---

### 2. Using Ngrok
Ngrok is a popular commercial tunnel provider. Note that running raw unsigned ngrok binaries may occasionally trigger local EDR warnings depending on your machine's configuration.

* **Using the Native Client:**
  1. Install `ngrok` and authenticate with your personal authtoken (`ngrok config add-authtoken <token>`).
  2. Expose your Liferay instance:
     ```bash
     ngrok http 8080
     ```
* **Using the Docker Client:**
  Expose your host's Liferay using the official ngrok container:
  * **macOS / Windows:**
    ```bash
    docker run -it -e NGROK_AUTHTOKEN="YOUR_NGROK_TOKEN" \
      ngrok/ngrok:latest http host.docker.internal:8080
    ```
  * **Linux:**
    ```bash
    docker run -it --add-host=host.docker.internal:host-gateway \
      -e NGROK_AUTHTOKEN="YOUR_NGROK_TOKEN" \
      ngrok/ngrok:latest http host.docker.internal:8080
    ```
  * **Docker Compose Network Integration:**
    Add it directly to your Liferay `docker-compose.yml`:
    ```yaml
    services:
      liferay:
        image: liferay/portal:7.4.3.112-ga112
      tunnel:
        image: ngrok/ngrok:latest
        environment:
          - NGROK_AUTHTOKEN=YOUR_NGROK_TOKEN
        command: http liferay:8080
    ```

---

## Getting Help

- **Architecture & self-hosting**: [Architecture Guide](architecture.md)
- **GitHub Issues**: [github.com/peterrichards-lr/lfr-tunnel/issues](https://github.com/peterrichards-lr/lfr-tunnel/issues)
- **Slack**: Reach out in the Liferay SE Slack channel for token issues or gateway access problems.
