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
curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.sh | sh
```

#### Windows (PowerShell)
```powershell
iwr https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/scripts/install.ps1 | iex
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

---

## Need Help?

* **Common Errors:**
  * `[Error] Unauthorized`: Your token may be invalid or revoked. Check `~/.lfr-tunnel/token` and verify your token is copied correctly.
  * `[Error] Subdomain already registered`: Another active user is currently using the requested subdomain prefix. Try a different `-subdomain` flag.
* **Detailed Guides:**
  * For advanced setup and Liferay virtual host configurations, see the [Liferay SE Guide](liferay-se-guide.md).
  * For details on self-hosting your own gateway, see the [Server Setup Guide](setup_guide.md).
