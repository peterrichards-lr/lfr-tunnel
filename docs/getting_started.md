# Getting Started Guide

`lfr-tunnel` is a client-server utility. The client CLI (`lfr-tunnel`) runs on your local machine and establishes a secure tunnel to the gateway server (`lfr-tunneld`) running on a public VPS.

> [!IMPORTANT]
> **The client CLI binary is of no use on its own.** It cannot establish a tunnel without connecting to a running gateway server, and it requires a valid **Personal Access Token (PAT)** to authenticate.

This guide walks you through installing the client, registering for access, claiming your token, and running your first tunnel.

---

## Overview of the Flow

```
[ Developer Laptop ]                                         [ Gateway Server ]
   (lfr-tunnel CLI)                                            (lfr-tunneld)
         │                                                           │
         │ 1. Submit Registration ───────────────────────────────────►
         │    (Provides email & requested subdomain)                 │
         │                                                           │
         │                        2. Admin Approves                  │
         │                           (Validates request)             │
         │                                                           │
         │ 3. Claim PAT Token ◄──────────────────────────────────────┘
         │    (Via approval email link)
         │
         │ 4. Store Token
         │    (Saved in ~/.lfr-tunnel/token)
         │
         │ 5. Connect Tunnel ────────────────────────────────────────► Exposes local ports
```

---

## Step 1: Install the Client

Before registering, install the `lfr-tunnel` client for your operating system.

### Recommended: Package Managers

Using a package manager ensures you get automated integrity validation (SHA-256 checks) and clean path management.

#### macOS / Linux (Homebrew)
```bash
brew tap peterrichards-lr/tap
brew trust peterrichards-lr/tap
brew install lfr-tunnel
```

#### Windows (Scoop)
```powershell
scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket
scoop install lfr-tunnel
```

### Direct Script Fallback

If package managers are not available on your system, use the direct installation scripts.

#### macOS / Linux
```bash
curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.sh | sh
```

#### Windows (PowerShell)
```powershell
iwr https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.ps1 | iex
```

Verify your installation:
```bash
lfr-tunnel -version
```

---

## Step 2: Register for Access

Access to the gateway server is authenticated via a Personal Access Token (PAT) associated with your user account. 

### 1. Submit a Registration Request
To request access, send a registration request to the gateway server. 

* **For Liferay Sales Engineering Team (connecting to the hosted SE gateway):**
  Submit a request using the hosted server:
  ```bash
  curl -s -X POST \
    -H "Content-Type: application/json" \
    -d '{"email": "your.name@liferay.com", "requested_subdomain": "your-name-se"}' \
    https://tunnel.lfr-demo.se/api/register-request
  ```
  *(Replace `your.name@liferay.com` with your official email, and `your-name-se` with your desired default subdomain).*

* **For Self-Hosted Gateways:**
  Replace `https://tunnel.lfr-demo.se` with your own gateway's URL:
  ```bash
  curl -s -X POST \
    -H "Content-Type: application/json" \
    -d '{"email": "admin@example.com", "requested_subdomain": "my-subdomain"}' \
    https://tunnel.yourdomain.com/api/register-request
  ```

You will receive a terminal output confirming your request has been successfully submitted and is pending admin approval.

### 2. Verify Your Email & Wait for Approval
1. Check your inbox for a **Verification Email** from the gateway. Click the link inside to verify that you own the email address.
2. Once verified, the gateway administrator receives a notification.
3. Once the administrator approves your request, you will receive an **Approval Email** containing a link to claim your token.

### 3. Claim Your Token
Click the link in your approval email, or run the following `curl` command using the claim token found in the email:

```bash
curl -s "https://tunnel.lfr-demo.se/api/claim?token=<claim-token-from-email>"
```

The gateway will respond with your **Personal Access Token (PAT)** (e.g., `lfr_pat_abc123...`). 

> [!WARNING]
> **This token is shown only once.** Copy it immediately and store it securely.

---

## Step 3: Authenticate and Store Your Token

To make using `lfr-tunnel` seamless, the client CLI looks for a stored PAT in your home directory (`~/.lfr-tunnel/token` or `%USERPROFILE%\.lfr-tunnel\token`). Once saved, the client will automatically load it on every run without needing any `-token` flags.

There are two ways to generate, claim, and save your token:

### Option A: Automatic Browser Login (Highly Recommended)

The client includes an interactive **Magic Handoff** flow that automatically completes token generation and saves it to your configuration directory with zero manual copying:

1. In your terminal, run the login command:
   ```bash
   lfr-tunnel login
   ```
2. Your default web browser will open to the gateway's **User Portal**.
3. Authenticate on the portal (using your approved email and magic link).
4. Upon logging in, the portal will securely hand off a newly generated token back to your local client terminal session.
5. The CLI saves the token automatically:
   ```
   ✅ Successfully authenticated! Your token has been saved securely to ~/.lfr-tunnel/token
   ```

---

### Option B: Manual Clipboard Configuration

If you claimed your token manually via `curl` or generated one in the User Portal web interface, you can save it to the default path yourself:

#### macOS / Linux
```bash
mkdir -p ~/.lfr-tunnel
echo "lfr_pat_your-token-here" > ~/.lfr-tunnel/token
chmod 600 ~/.lfr-tunnel/token
```

#### Windows (PowerShell)
```powershell
New-Item -ItemType Directory -Force -Path "$Home\.lfr-tunnel"
Set-Content -Path "$Home\.lfr-tunnel\token" -Value "lfr_pat_your-token-here"
```

> [!CAUTION]
> **Never commit your PAT to source control.** Storing the token in `~/.lfr-tunnel/token` ensures it is kept completely outside your development workspace.

---

### Option C: Restricted Secrets File (Advanced & Secure)

This matches the security practices taken in LDM. Instead of storing the token raw in `~/.lfr-tunnel/token`, you store it in a restricted variables file which you source in your shell profile.

#### On macOS / Linux (Bash or Zsh)
1. Create the restricted folder and secrets file:
   ```bash
   mkdir -p ~/.config/lfr
   touch ~/.config/lfr/secrets
   chmod 600 ~/.config/lfr/secrets
   ```
2. Add your token variable to the file:
   ```bash
   echo 'export LFT_CLIENT_TOKEN="your_actual_token_here"' >> ~/.config/lfr/secrets
   ```
3. Source the file in your profile by adding this to the bottom of your `~/.zshrc` or `~/.bashrc`:
   ```bash
   [ -f ~/.config/lfr/secrets ] && source ~/.config/lfr/secrets
   ```

#### On Windows (PowerShell)
1. Run these commands in PowerShell to create the secrets folder/file and restrict permissions to only your explicit user account:
   ```powershell
   New-Item -ItemType Directory -Path "$HOME\.config\lfr" -Force
   $SecretFile = New-Item -ItemType File -Path "$HOME\.config\lfr\secrets.ps1" -Force

   # Restrict permissions so ONLY you can access it
   $Acl = Get-Acl $SecretFile.FullName
   $Acl.SetAccessRuleProtection($true, $false)
   $User = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
   $Rule = New-Object System.Security.AccessControl.FileSystemAccessRule($User, "FullControl", "Allow")
   $Acl.AddAccessRule($Rule)
   Set-Acl $SecretFile.FullName $Acl
   ```
2. Add the token to the file:
   ```powershell
   Set-Content -Path "$HOME\.config\lfr\secrets.ps1" -Value '$env:LFT_CLIENT_TOKEN="your_actual_token_here"'
   ```
3. Load it automatically on shell startup. Open your PowerShell profile (`notepad $PROFILE`) and add:
   ```powershell
   if (Test-Path "$HOME\.config\lfr\secrets.ps1") { . "$HOME\.config\lfr\secrets.ps1" }
   ```

The client CLI (`lfr-tunnel`) will automatically load your token from these files if it is not configured via other mechanisms.

---

## Step 4: Run Your First Tunnel

Once your token is saved, you can run the client. By default, `lfr-tunnel` targets the primary Liferay port `8080` and scans for client extensions.

### Zero-Config Workspace Mode (LDM/Workspaces)
Navigate to your Liferay Workspace root directory and run:

```bash
lfr-tunnel -subdomain your-name-se
```

The client will:
1. Scan for active client extensions and detect their development ports automatically.
2. Authenticate with the stored PAT.
3. Print the live public HTTPS URLs where your local server and assets are now accessible.

### Port-Specific Standalone Mode (Tomcat/Docker)
If you are running a standalone Tomcat bundle on port `8080` without a Liferay Workspace:

```bash
lfr-tunnel -subdomain your-name-se -ports 8080
```

### Choosing Which Gateway to Use

Most people need none of this: the client ships knowing its gateway, fetches the list of
available ones, and picks whichever answers fastest.

If you do need to name one, **the flag you choose changes the behaviour**:

| How you supply it | Picks the closest | Fails over |
|---|---|---|
| `-gateway <url>` | ✅ | ✅ |
| `server_url:` in your client config file | ✅ | ✅ |
| `-server <url>` | ❌ pinned to that gateway | ❌ |
| `LFT_SERVER_URL` / `LFT_CLIENT_SERVER` / `LFT_SERVER` | ❌ pinned | ❌ |

```bash
# Start from this gateway, but still pick the closest and fail over:
lfr-tunnel -gateway https://your-gateway.example.com -subdomain your-name-se

# Or persist it, with the same effect:
#   server_url: "https://your-gateway.example.com"
```

`-server` pins deliberately -- use it when you want one specific gateway and nothing else. But
if you pin a gateway on another continent you stay there however close an edge is, and your
tunnel drops for the whole window if that gateway is scheduled to stop.

To prefer a region while keeping failover, use `-region <name>`. To re-run the latency probe
after a gateway has come back, add `-refresh-region` once -- the election is otherwise cached for
24 hours.

> [!TIP]
> `server_url:` above goes in the client config file, `~/.lfr-tunnel/config.yaml`. It is worth
> knowing that file exists: it holds every setting you would otherwise retype -- subdomain,
> ports, target host, access controls -- and it is the only place a gateway can be named
> without pinning. See the **[Client Configuration File](client_configuration.md)** reference
> for every key, its default, and how it interacts with flags and environment variables.

---

## Running in the Background & Start on Login

If you want the tunnel client to run silently or initialize automatically when you log into your machine, you can configure background execution and autostart configurations.

### 1. Headless Background Execution (CLI)

You can launch and manage the client in the background directly from your terminal using process control flags:

* **Start in Background**: Runs the tunnel as a detached daemon process:
  ```bash
  lfr-tunnel -background -subdomain your-name-se
  ```
* **Check Status**: Verifies if the daemon is active and prints the running PID and leased public URLs:
  ```bash
  lfr-tunnel -status
  ```
* **Stop Daemon**: Terminates the background process cleanly:
  ```bash
  lfr-tunnel -stop
  ```

### 2. Autostart on Login

You can install startup items that launch the client automatically when your user logs in.

#### Headless CLI Client
To configure the headless background tunnel daemon to launch on login:
* **macOS / Linux / Windows**: Run the subcommand:
  ```bash
  lfr-tunnel install-service
  ```

* **System Details**:
  * **macOS**: Creates a LaunchAgent plist at `~/Library/LaunchAgents/com.liferay.tunnel.plist`.
  * **Windows**: Installs a hidden script at `~\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\lfr-tunnel.vbs`.
  * **Linux**: Creates and registers a systemd user service at `~/.config/systemd/user/lfr-tunnel.service`. You can monitor it using:
    ```bash
    systemctl --user status lfr-tunnel.service
    ```

#### System Tray GUI Client
To configure the System Tray / Menu Bar utility to launch on login:
* **Tray Toggle**: Simply open the system tray menu and click **Launch on Login** (displays a checkmark `✓` when enabled).
* **CLI Command**: Alternatively, register the autostart items using subcommands:
  * **Enable GUI autostart**: `lfr-tunnel install-gui-service`
  * **Disable GUI autostart**: `lfr-tunnel uninstall-gui-service`

* **System Details**:
  * **macOS**: Registers `~/Library/LaunchAgents/com.liferay.tunnel.gui.plist`.
  * **Windows**: Installs `~\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\lfr-tunnel-gui.vbs`.
  * **Linux**: Creates a desktop autostart entry at `~/.config/autostart/lfr-tunnel-gui.desktop`.

---

## Client Lifecycle Hooks & Failover Automation

`lfr-tunnel` runs user-configured shell commands when the tunnel moves between gateways --
when a gateway announces it is stopping, and around the failover or failback that follows.
They exist so the things that care about the tunnel's public URL (a Liferay virtual host, a
webhook registration, a DNS record) can be updated without watching the logs.

### Configuration (`~/.lfr-tunnel/config.yaml`)

Add script paths or commands under the `hooks` section. Anything you leave out is simply not
run. Every other key in this file is described in the [Client Configuration
File](client_configuration.md) reference:

```yaml
hooks:
  warning_received: "/usr/local/bin/on-gateway-warning.sh" # The gateway has announced it is stopping
  stopping: "/usr/local/bin/on-gateway-stopping.sh"        # Right before the old tunnel is torn down
  stopped: "/usr/local/bin/on-gateway-stopped.sh"          # The old tunnel has closed
  starting: "/usr/local/bin/on-gateway-starting.sh"        # Before the client re-registers elsewhere
  started: "/usr/local/bin/on-gateway-started.sh"          # A session is live again on the new gateway
```

Each value is a command line, not a file path with special handling -- it is passed to
`/bin/sh -c` (`cmd.exe /c` on Windows), so pipes, arguments and shell syntax all work.

### When each hook fires

The four session hooks fire around a **move**, not around the process. A move is a failover
after a fault, a failback to the primary region, or a planned migration ahead of an announced
gateway stop.

| Event | Fires |
| --- | --- |
| `warning_received` | The connected gateway announces it is going down. Once per announcement, not once per heartbeat -- the warning repeats for the whole countdown. |
| `stopping` | The session is ending and the client is about to move, while the old tunnel is still standing. |
| `stopped` | The old tunnel has closed and its session has been cancelled. |
| `starting` | Before the client begins re-registering. The destination region is not chosen yet. |
| `started` | A session has been re-established and the new endpoint recorded. Fires for both a failback and a failover. |

In a planned migration the full sequence is
`warning_received` → `stopping` → `stopped` → `starting` → `started`. An unannounced failure
produces the same sequence without `warning_received`. If every candidate region is exhausted,
`started` does not fire -- there is no new session to report.

Three cases where nothing fires, deliberately:

* **The first connection and a normal shutdown.** `starting`/`started` are about moving to a
  different gateway; the initial connect prints its URLs and `stopping`/`stopped` on Ctrl+C
  would delay the exit for a hook whose work is already done.
* **A client pinned with `-server`.** A pinned client never fails over ([#1275](https://github.com/peterrichards-lr/lfr-tunnel/issues/1275)),
  so it has no moves to report.
* **A hook you have not configured.** No shell is spawned.

### Contextual environment variables

Every hook receives all five, always, plus the environment the client itself was started
with. Values that are not knowable for that event are empty (or `0`):

| Variable | Value |
| --- | --- |
| `LFT_EVENT` | The event name: `warning_received`, `stopping`, `stopped`, `starting`, `started`. |
| `LFT_NODE_ID` | The gateway that announced a stop. Empty when the move was not triggered by an announcement. It keeps naming the gateway being *left* for the rest of the sequence. |
| `LFT_SECONDS_REMAINING` | Seconds until the announced stop, counting down as the move proceeds. `0` when no stop has been announced. |
| `LFT_FAILOVER_REGION` | The region now serving the tunnel. Set on `started`; empty on the others, because the destination is not elected until the move is underway. |
| `LFT_SUBDOMAIN` | The leased subdomain prefix. |

### What a hook can and cannot do

* **A hook cannot veto a transition.** Its exit status is logged and discarded. The move has
  already been decided by the time a hook runs, and letting a script refuse one would leave
  the tunnel down with no way to recover it. A failing hook does not stop the next one.
* **A hook is bounded at 15 seconds.** After that it is killed and the client carries on. The
  bound covers a hook that backgrounds a child as well, so a stray `&` cannot wedge the
  client.
* **The four session hooks run in order, one at a time**, so `stopped` has finished before
  `starting` begins. The cost of that guarantee is that a slow hook delays the move by up to
  its 15-second bound. `warning_received` is the exception: it runs concurrently, because the
  point of the warning is to move *sooner*, and blocking the heartbeat to run a script would
  work against that.
* **Failures are visible.** Every run is logged, with the hook's combined output on failure.

---

## Client Logs & Diagnostics

The client keeps persistent logs under `~/.lfr-tunnel/logs/`, for both foreground and background runs. They are the first place to look when a tunnel misbehaved and the terminal output is gone.

| File | Contents |
| --- | --- |
| `traffic-<subdomain>.log` | One JSON object per proxied HTTP request: timestamp, method, path, status, duration, target port and the region serving it. |
| `error-<subdomain>.log` | Structured diagnostic events — failover, failback, lease eviction, exhausted regions — with the fields explaining each. |
| `client-<subdomain>.log` | Console output from a `-background` run. |

All three are JSON Lines and are rotated rather than overwritten: the previous run is kept as `.1`, up to three generations, each capped at 8 MiB. A client that exits unexpectedly leaves its log behind instead of erasing it on the next start.

Because they are JSON Lines, they can be filtered directly:

```bash
# Every request that failed
jq 'select(.status >= 400)' ~/.lfr-tunnel/logs/traffic-your-name-se.log

# What happened during the last region switch
jq 'select(.event | startswith("failover"))' ~/.lfr-tunnel/logs/error-your-name-se.log

# Slowest requests first
jq -s 'sort_by(-.dur_ms) | .[0:10]' ~/.lfr-tunnel/logs/traffic-your-name-se.log
```

### Recording request and response bodies

Bodies are **not** written by default. They routinely carry OAuth tokens, session cookies and customer data, and these files persist on disk. When you need them — debugging an incoming webhook payload, for instance — enable them explicitly:

```bash
lfr-tunnel -subdomain your-name-se -log-bodies
```

Bodies are capped at 10 KB each. Prefer the Inspector at `http://localhost:4040` for casual payload inspection: it holds the last 100 requests in memory only, so nothing reaches disk.

---

## Need Help?

* **Common Errors:**
  * `[Error] Unauthorized`: Your token may be invalid or revoked. Check `~/.lfr-tunnel/token` and verify your token is copied correctly.
  * `[Error] Subdomain already registered`: Another active user is currently using the requested subdomain prefix. Try a different `-subdomain` flag.
* **Detailed Guides:**
  * For advanced setup and Liferay virtual host configurations, see the [Liferay SE Guide](liferay-se-guide.md).
  * For details on self-hosting your own gateway, see the [Server Setup Guide](server/setup_guide.md).


<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-09-03* | *Last Reviewed: 2026-09-03*
