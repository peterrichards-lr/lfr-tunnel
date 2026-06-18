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

1. Copy `.env.example` to `.env` and add your token:
   ```
   LFT_CLIENT_TOKEN=lfr_pat_your-token-here
   LFT_SUBDOMAIN=your-name-se
   ```

2. Run the wrapper for your OS:
   - **macOS/Linux**: `./lfr-tunnel.sh`
   - **Windows CMD**: `lfr-tunnel.bat`
   - **Windows PowerShell**: `.\lfr-tunnel.ps1`

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
