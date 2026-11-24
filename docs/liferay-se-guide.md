# Liferay Employee Quick-Start Guide

[![Better Stack Badge](https://uptime.betterstack.com/status-badges/v2/monitor/2q3sj.svg)](https://uptime.betterstack.com/?utm_source=status_badge)

This guide is for **Liferay employees** using the shared, Liferay-operated `lfr-tunnel` gateway:
Sales Engineering, consultants, developers, or anyone with an `@liferay.com` account who needs
to share a local Liferay environment. It covers installation, signing in, and examples using the
gateway's real domains.

> [!TIP]
> **Sign in with Liferay SSO. No registration or approval.** Install the client, run
> `lfr-tunnel login`, sign in with your `@liferay.com` account through Okta, and start a tunnel.
> It takes minutes.

For general architecture, self-hosting instructions, and configuration reference, see the main [README](https://github.com/peterrichards-lr/lfr-tunnel#readme) and [Architecture Guide](architecture.md).

---

## Hosted Gateway Details

| Property | Value |
|---|---|
| **Gateway URL** | `https://tunnel.lfr-demo.se` |
| **Primary Domain** | `lfr-demo.se` |
| **Secondary Domain** | `lfr-demo.online` |
| **Sign-in** | Liferay SSO (Okta), `@liferay.com` accounts only. Your account is created on first sign-in, with no registration or approval. |

Your tunnel will be reachable at:
- `https://<your-subdomain>.lfr-demo.se`
- `https://<your-subdomain>.lfr-demo.online`

---

## Step 1: Install the Client

> [!WARNING]
> **EDR Whitelist Constraint (SentinelOne):** Do NOT install the client using package managers like Homebrew or Scoop. These managers install binaries outside of whitelisted execution paths (e.g. in `/opt/homebrew/bin` or custom scoop buckets), which will trigger local EDR alarms and kill your terminal. 
> 
> You must use either **Method A (Native Installer)** or **Method B (Docker Container)** to run the client safely.

### Method A: Native Installer Script (Recommended Native)

This script downloads the signed client binary straight into the install directory the EDR exclusions cover (`$HOME/liferay/lfr-tunnel/lfr-tunnel`, or `%USERPROFILE%\liferay\lfr-tunnel\lfr-tunnel.exe` on Windows). The paths and exclusion rules are listed in [infosec.md §3](infosec.md#3-standard-execution-paths--edr-exclusions).

* **macOS / Linux:** `curl -fsSL https://tunnel.lfr-demo.se/install.sh | sh`
* **Windows (PowerShell):** `irm https://tunnel.lfr-demo.se/install.ps1 | iex`

**Which exclusions apply.** InfoSec applies the exclusions through a per-OS endpoint tag
(`macOS-global`, `Linux-global` or `Windows-global`). The tag applies the Alerts-based exclusions
plus the designated file path, and nothing else. The older tags that allowed earlier tests and PoCs
are being removed. Whether your machine carries the new tag yet is for InfoSec to confirm. If the
client alerts or is quarantined after installing this way, tell InfoSec (ticket LRINFOSEC-7091) and
include the path the alert names ([infosec.md §3](infosec.md#exclusion-type-alerts)).

**Do not set `LFT_INSTALL_DIR` or the `LFR_TUNNEL_*_INSTALL_DIR` variables.** They move the client out of the excluded directory, and `--upgrade` keeps it there. If one is in your shell profile, remove it and reinstall.

To verify your installation, open a new terminal window and run: `lfr-tunnel -version`

### Method B: Standalone Docker Container (EDR Immune)
For environments where local native binary execution is completely restricted, run our pre-built, multi-architecture client container directly from Docker Hub. This requires **zero local installation, zero compilation, and is 100% immune to EDR host-level blocks**:

```bash
docker run -d --name lfr-tunnel \
  -e LFT_CLIENT_TOKEN="YOUR_PERSONAL_ACCESS_TOKEN" \
  peterjrichards/lfr-tunnel:latest \
  -bootstrap https://tunnel.lfr-demo.se \
  -subdomain your-name-se \
  -ports 8080
```

---

## Step 2: Sign In with Liferay SSO

The client authenticates with a **Personal Access Token (PAT)**. On the hosted gateway you get one
by signing in; there is nothing to request and nobody to wait for.

1. In your terminal, run:
   ```bash
   lfr-tunnel login
   ```
2. Your browser opens the gateway's portal. **Sign in with Liferay SSO** using your
   `@liferay.com` account (Okta). Your account is created the first time you sign in.
3. Click **Generate Token** in the dialog the portal opens. The token is handed back to your
   terminal and saved to `~/.lfr-tunnel/token`, and the client loads it automatically from then
   on.

You are ready to run a tunnel: pick a subdomain (Step 4) and use one of the examples in Step 5,
for instance `lfr-tunnel -subdomain your-name-se -ports 8080`.

What the handoff checks, what happens when the gateway cannot verify the token, how long `login`
waits, and what you can paste at its prompt are the same on every gateway, and are described in
[Getting Started, Step 3 Option A](getting_started.md#option-a-automatic-browser-login-highly-recommended).

---

## Step 3: Using the Token Elsewhere

`lfr-tunnel login` covers the native client. For anything else, sign in to the portal at
`https://tunnel.lfr-demo.se` with Liferay SSO and generate a token there:

* **Docker (Method B, and the Docker examples below):** pass the token as `LFT_CLIENT_TOKEN`.
* **Storing it somewhere other than `~/.lfr-tunnel/token`** (a secrets file, a password-manager
  mount, `token_file:` in your config): see
  [Getting Started, Step 3](getting_started.md#step-3-authenticate-and-store-your-token).

> [!CAUTION]
> **Never commit your PAT to source control.** The `.env` file (used by the Docker wrapper) is git-ignored, and `~/.lfr-tunnel/token` is outside your workspace. Keep it there.

If something goes wrong (`401 unauthorized`, `no access token configured`, an expiry warning), see
[Getting Started, Need Help?](getting_started.md#need-help).

---

## Step 4: Check Available Subdomains

Before picking a subdomain, check it with the client. It uses the token you just saved, so
nothing secret goes on the command line:

```bash
lfr-tunnel check-subdomain your-name-se
```

Each of the gateway's domains is listed as `available` or `unavailable`, with suggestions for any
domain where the name is taken. The exit status is `0` if the name is free on at least one domain,
`1` if it is free on none, and `2` if the check itself failed. See
[Checking a subdomain before you use it](client_configuration.md#checking-a-subdomain-before-you-use-it).

---

## Step 5: Advanced Usage & Integration Runtimes

Depending on how you run Liferay and client extensions locally, use the appropriate examples below for the **Native Client** or the **Docker Client**.

### 1. LDM (Liferay Development Manager) & Workspace Client Extensions
If you are developing Liferay Client Extensions (CX) in a Liferay Workspace, the tunnel automatically scans `client-extension.yaml` files, maps ports (e.g., `8080` for portal and `3001` for assets), and prints clickable public URLs.

* **Without LDM:** from your Liferay Workspace root, run:
  ```bash
  lfr-tunnel -subdomain your-name-se
  ```
* **With LDM, using the native client** (the default): LDM runs your installed client for the
  project.
  ```bash
  ldm share start my-project --subdomain your-name-se
  # or start the project and share it in one step:
  ldm run my-project --share --share-subdomain your-name-se
  ```
  LDM does the Liferay setup for you: it adds the tunnel's domains to
  `virtual.hosts.valid.hosts` and passes `-preserve-host`, so links and sign-in use the public
  address with no manual configuration. LDM does not install the client; install it first
  ([Step 1](#step-1-install-the-client)), and LDM finds it at the approved install path.
* **With LDM, using the Docker client:** LDM runs the client as a sidecar container next to the
  project instead.
  ```bash
  ldm share start my-project --provider lfr-tunnel-docker --subdomain your-name-se
  # with ldm run, the flag is --share-provider lfr-tunnel-docker
  ```

  See LDM's
  [Sharing tunnels](https://github.com/peterrichards-lr/liferay-docker-manager/blob/master/docs/how-to/sharing_tunnels.md)
  guide for its other options.

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
      -bootstrap https://tunnel.lfr-demo.se \
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
      -bootstrap https://tunnel.lfr-demo.se \
      -subdomain dev-tomcat \
      -ports 8080
    ```

### 2b. Why Liferay generates `localhost` URLs, and how to stop it

If your pages load through the tunnel but every generated link points back at
`http://localhost:8080` — or you land in a redirect loop after signing in — this is why.

**Liferay decides what it is called from the `Host` header.** By default the client rewrites
`Host` to your local target before forwarding, so Liferay is told it is `localhost:8080` and
generates its links accordingly. The public hostname *is* sent, on `X-Forwarded-Host`, but
Liferay does not use it for link generation.

Measured against **Liferay DXP 2026.Q1.12 LTS** behind this tunnel, counting the generated links
on the portal home page:

| client setting | what Liferay is told it is | generated links |
|---|---|---|
| default (`preserve_host` off) | the local target | 33 x `http://<target>:8080` |
| **`-preserve-host`** | the public hostname | 33 x `http://<subdomain>.lfr-demo.se` |

**The client turns this on for you when it finds Liferay.** Liferay names itself in a
`Liferay-Portal` response header, so the client asks each local port and turns `preserve_host` on
for the one that answers with it. That covers a Tomcat bundle, a container, a workspace and ports
you name with `-ports`. Client-extension ports published alongside it are unaffected. If Liferay is
still starting, the client keeps asking for up to 15 minutes and turns it on when the portal comes
up. You'll see `[Client] Liferay identified on port 8080 ... preserve_host is now on for it`.

If you've switched the header off (`http.header.version.verbosity=off`), pass the flag yourself:

```bash
lfr-tunnel -subdomain dev-tomcat -ports 8080 -preserve-host
```

or, permanently, set `preserve_host: true` in your client config file. To stop the client turning it
on by itself, set `preserve_host_auto: false`. A `preserve_host: false` line, which older Settings
saves wrote, no longer switches detection off. See
[`preserve_host` and Liferay](client_configuration.md#preserve_host-and-liferay).

**Liferay must also accept the hostname.** Until it does, it refuses the request with HTTP 500.
That is not a dead page: the 500 page refreshes to `/c`, which fails the same way and sends the
visitor's browser to **`http://localhost/web/guest`**, their own machine. So the first thing a
customer sees from a misconfigured demo link is their browser trying to reach `localhost`. The
page says nothing about the cause; it appears only in Liferay's log, as
`java.lang.RuntimeException: Invalid host name <host>`. (Measured on Liferay DXP 2026.Q1,
2026-11-23.)

Either of these fixes it:

* **Recommended:** add the gateway's domains to `virtual.hosts.valid.hosts` in
  `portal-ext.properties`. The property accepts wildcards, so one entry per gateway domain covers
  every subdomain, and keeps working when your tunnel's subdomain changes:

  ```properties
  virtual.hosts.valid.hosts=localhost,127.0.0.1,*.lfr-demo.se,*.lfr-demo.online
  ```

  In a Liferay container, set it with the environment variable
  `LIFERAY_VIRTUAL_PERIOD_HOSTS_PERIOD_VALID_PERIOD_HOSTS`.
* set the Virtual Host as in the step above (Control Panel -> Instance Settings -> Virtual
  Hosts). This covers **exactly one hostname**, so it has to be redone whenever the tunnel's
  subdomain changes.

The client watches for this. When the Liferay port answers 500 for the public hostname but not for
the local one, it prints the fix once per hostname. To check before a demo instead of during one,
run `lfr-tunnel doctor`. It reports your token, install path, `preserve_host`, and whether
Liferay accepts the tunnel hostname, and changes nothing. See
[Checking your setup: `doctor`](client_configuration.md#checking-your-setup-doctor).

**That is the whole configuration.** `preserve_host` on for the Liferay port, plus the hostname
accepted by Liferay.
Nothing else was required to get correct links and a working sign-in.

#### Properties you do not need

`web.server.host`, `web.server.http.port`, `redirect.url.security.mode` and
`redirect.url.domains.allowed` are commonly suggested for this symptom and were **not** needed in
the configuration above. They pin Liferay to one hostname, which a tunnel subdomain may not keep
across restarts. Do not add them speculatively.

#### One thing this guide does not yet tell you

Whether Liferay generates `https://` rather than `http://` links depends on it acting on
`X-Forwarded-Proto`, which the gateway does send. **That leg was not tested**, because the test
harness fronts with plain HTTP -- so `web.server.https.port` and
`company.security.auth.requires.https` are deliberately absent from this guide rather than
recommended on reasoning. If you hit a redirect loop specifically on sign-in over HTTPS, that is
the pair to look at, and please say so on the tracker so this section can be finished from a
measurement rather than a guess.

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
        - LFT_CLIENT_BOOTSTRAP=https://tunnel.lfr-demo.se
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

If you need to share your local environment using an alternative provider, Liferay employees can also use **ngrok** or **Cloudflare Tunnels (cloudflared)** alongside `lfr-tunnel`.

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


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-11-24* | *Last Reviewed: 2026-11-24*
