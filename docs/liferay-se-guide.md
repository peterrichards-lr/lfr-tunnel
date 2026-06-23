# Liferay SE Team — Quick-Start Guide

This guide is for **Liferay Sales Engineering team members** connecting to the shared, Liferay-operated `lfr-tunnel` gateway. It covers registration, credential setup, and usage examples with the real team domains.

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

### macOS — Homebrew (Recommended)

```bash
brew tap peterrichards-lr/tap
brew trust peterrichards-lr/tap
brew install lfr-tunnel
```

Homebrew independently verifies the SHA-256 checksum and removes the macOS quarantine flag automatically.

### Windows — Scoop (Recommended)

```powershell
scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket
scoop install lfr-tunnel
```

### macOS / Linux — Direct Download (Fallback)

Use this on Linux, or on macOS if Homebrew is not available:

```bash
curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.sh | sh
```

### Windows — Direct Download (Fallback)

Use this if Scoop is not available:

```powershell
iwr https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.ps1 | iex
```

Verify the installation:
```bash
lfr-tunnel -version
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

---

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

## Step 4: Run the Tunnel

### LDM / Liferay Workspace (Zero-Config)

Navigate to your Liferay Workspace root and run:
```bash
lfr-tunnel -subdomain alpha-se
```

The client automatically scans `client-extension.yaml` files, detects all ports, and prints your live URLs:
```
[Client] Registration successful! Your public tunnel URLs are:
  https://alpha-se.lfr-demo.se      -> local port 8080
  https://alpha-se.lfr-demo.online  -> local port 8080
```

### Standalone Tomcat Bundle

```bash
lfr-tunnel -subdomain dev-tomcat -ports 8080
```

Then configure Liferay Virtual Host:
1. Log into `http://localhost:8080`
2. **Control Panel → Instance Settings → Virtual Hosts**
3. Set Virtual Host to: `dev-tomcat.lfr-demo.se`

### Multi-Port (Portal + Client Extension)

```bash
lfr-tunnel -subdomain alpha-se -ports 8080,3001
```

This yields:
- `https://alpha-se.lfr-demo.se` → port `8080` (Liferay Portal)
- `https://alpha-se-3001.lfr-demo.se` → port `3001` (Client Extension assets)

---

## Step 5: Check Available Subdomains

Before picking a subdomain, you can check availability:

```bash
curl -s -H "Authorization: Bearer lfr_pat_your-token-here" \
  "https://tunnel.lfr-demo.se/api/check-subdomain?subdomain=your-name-se&domain=lfr-demo.se"
```

If the subdomain is taken, the server will suggest alternatives.

---

## Configuration File (Recommended)

Create a `~/.lfr-tunnel/client-config.yaml` to avoid typing the server URL every time:

```yaml
server_url: "https://tunnel.lfr-demo.se"
subdomain: "your-name-se"
```

Then simply run:
```bash
lfr-tunnel
```

---

## Targeting Local Domain Names (LDM & Proxy virtual hosts)

If your local setup resolves virtual hosts (e.g. LDM's local Nginx proxy configured for `my-project.local`), you can route tunnel traffic directly through that proxy instead of Tomcat (`8080`).

By setting a target host, `lfr-tunnel` automatically routes traffic to it and rewrites the incoming HTTP `Host` header to match:

* **CLI flag**:
  ```bash
  lfr-tunnel -ports 80 -target-host my-project.local
  ```
* **Configuration File (`~/.lfr-tunnel/client-config.yaml`)**:
  ```yaml
  server_url: "https://tunnel.lfr-demo.se"
  subdomain: "your-name-se"
  target_host: "my-project.local"
  ports:
    - 80
  ```
* **Environment Variable**: Set `LFT_TARGET_HOST=my-project.local` before execution.

---

## Running in the Background

```bash
# Start in background
lfr-tunnel -background

# Check status
lfr-tunnel -status

# Stop cleanly
lfr-tunnel -stop
```

---

## Using the Docker Wrapper (EDR Bypass)

If your machine is protected by security agents (SentinelOne, CrowdStrike, etc.) that flag Go binaries:

### Option A: Direct Docker Hub Command (Zero-Install / Zero-Build Method)

You can run our official pre-built, multi-architecture client container directly from Docker Hub! This requires absolutely **no local repository cloning, zero building, and is 100% immune to SentinelOne/EDR host-level alerts**:

```bash
docker run -d --name lfr-tunnel \
  -e LFT_CLIENT_TOKEN="YOUR_PERSONAL_ACCESS_TOKEN" \
  -p 8080:8080 \
  peterrichards/lfr-tunnel:latest \
  -server https://tunnel.lfr-demo.se \
  -subdomain peter-dev \
  -ports 8080
```

*   **`-d`**: Runs the tunnel in the background as an isolated daemon.
*   **`-e LFT_CLIENT_TOKEN`**: Passes your secure developer PAT.
*   **`-p 8080:8080`**: Exposes the port.
*   **`peterrichards/lfr-tunnel:latest`**: Pulls the official, pre-scanned, and optimized image dynamically from Docker Hub (supporting both Apple Silicon M1/M2/M3 and Intel natively!).
*   **`-server`**: Points to your production server gateway.
*   **`-ports 8080`**: Explicitly routes the incoming wildcard traffic from the gateway back out of the tunnel container to your host Liferay port `8080`.

### Option B: Local Repository Scripts (Clone & Build)

1. Copy `.env.example` to `.env` and add your token:
   ```
   LFT_CLIENT_TOKEN=lfr_pat_your-token-here
   LFT_SUBDOMAIN=your-name-se
   ```

2. Run the wrapper for your OS:
   - **macOS/Linux**: `./lfr-tunnel.sh`
   - **Windows CMD**: `lfr-tunnel.bat`
   - **Windows PowerShell**: `.\lfr-tunnel.ps1`

### Docker Container Environment Variable Contract
When running `lfr-tunnel` directly as a Docker container (e.g. orchestrated by LDM or in a custom docker-compose file), the following environment variable contract is supported:

| Environment Variable | Canonical | Fallbacks | Description | Example |
| :--- | :--- | :--- | :--- | :--- |
| **Server URL** | `LFT_CLIENT_SERVER` | `LFT_SERVER_URL`, `LFT_SERVER` | Gateway server endpoint | `https://tunnel.lfr-demo.se` |
| **Auth Token** | `LFT_CLIENT_TOKEN` | `LFT_TOKEN` | Gateway developer PAT | `lfr_pat_...` |
| **Subdomain** | `LFT_CLIENT_SUBDOMAIN` | `LFT_SUBDOMAIN` | Custom subdomain prefix | `pjrtest` |
| **Target Host** | `LFT_TARGET_HOST` | — | Backend hostname or IP to route to | `liferay` (or `http://liferay:8080`) |
| **Ports** | `LFT_CLIENT_PORTS` | — | Comma-separated list of ports | `8080,3000` |
| **Inspector Bind** | `LFT_INSPECTOR_BIND` | — | Binding address for local inspector dashboard | `0.0.0.0` or `127.0.0.1` |

> [!NOTE]
> The target host parser is URL-aware: if a full URL with scheme/port (e.g. `http://liferay:8080`) is passed into `LFT_TARGET_HOST`, the client automatically cleans it to the raw hostname (`liferay`) to ensure correct routing.
>
> Inside container/Docker environments, the inspector automatically binds to `0.0.0.0` instead of `127.0.0.1` by default to ensure port-forwarded traffic from the host machine is able to access the dashboard.

### Advanced Target Routing (Docker Containers & Native Bundles)

Depending on your local development setup, you can configure the tunnel to route to different environments:

#### 1. Routing to a Liferay Docker Container (Compose Integration)
If your Liferay instance runs inside Docker, you can run the tunnel client in the same Docker network. Setting `LFT_TARGET_HOST` to the Liferay container's service name routes traffic directly over the internal Docker bridge network without exposing host ports:

```yaml
version: "3.8"
services:
  liferay:
    image: liferay/portal:7.4.3.112-ga112
    # No ports mapping required if you only route through the tunnel!

  tunnel:
    image: peterjrichards/lfr-tunnel:latest
    environment:
      - LFT_CLIENT_SERVER=https://tunnel.lfr-demo.se
      - LFT_CLIENT_TOKEN=lfr_pat_your-token
      - LFT_CLIENT_SUBDOMAIN=my-liferay-demo
      - LFT_TARGET_HOST=liferay
      - LFT_CLIENT_PORTS=8080
```

#### 2. Routing to a Local Tomcat Bundle (Host Machine)
If you run a native Tomcat bundle directly on your macOS or Windows host machine, the tunnel container can access your host's loopback interface (`localhost`) using **`host.docker.internal`**:

* **macOS / Windows (Docker Desktop):**
  ```bash
  docker run -d --name lfr-tunnel \
    -e LFT_CLIENT_TOKEN="lfr_pat_your-token" \
    -e LFT_TARGET_HOST="host.docker.internal" \
    peterjrichards/lfr-tunnel:latest \
    -server https://tunnel.lfr-demo.se \
    -subdomain my-local-bundle \
    -ports 8080
  ```

* **Linux:**
  Include the `--add-host` flag to resolve the host loopback gate:
  ```bash
  docker run -d --name lfr-tunnel \
    --add-host=host.docker.internal:host-gateway \
    -e LFT_CLIENT_TOKEN="lfr_pat_your-token" \
    -e LFT_TARGET_HOST="host.docker.internal" \
    peterjrichards/lfr-tunnel:latest \
    -server https://tunnel.lfr-demo.se \
    -subdomain my-local-bundle \
    -ports 8080
  ```

---

## Keeping the Client Up to Date

```bash
lfr-tunnel -upgrade
```

This fetches the latest release from GitHub, verifies the SHA256 checksum, and replaces the binary in place.

---

## Getting Help

- **Architecture & self-hosting**: [Architecture Guide](architecture.md)
- **GitHub Issues**: [github.com/peterrichards-lr/lfr-tunnel/issues](https://github.com/peterrichards-lr/lfr-tunnel/issues)
- **Slack**: Reach out in the Liferay SE Slack channel for token issues or gateway access problems.
