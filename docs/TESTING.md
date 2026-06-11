# Liferay Tunnel - Manual Verification Protocol

The following test scenarios cover edge cases, UI states, and environmental configurations that are not fully covered by the automated integration tests. These should be verified manually against the live VPS instance (`https://tunnel.lfr-demo.se`).

## 1. Cloud User Portal & Magic Link Flow
**Goal**: Verify a user can seamlessly request access and log into the portal without a password.
- **Step 1**: Ensure your email is in the approved `users` table via the admin endpoints (or use your existing developer token profile).
- **Step 2**: Open a web browser and navigate to `https://tunnel.lfr-demo.se/portal`.
- **Step 3**: Enter your email address and click "Send Magic Link".
- **Step 4**: Check your inbox (or local SMTP capture). You should receive a modern HTML email containing your Client IP address and two links. 
- **Step 5**: Click the primary "Log In to Portal" link. You should instantly be authenticated and redirected to the dashboard UI. Verify the dark mode layout renders correctly.

## 2. Abuse Reporting Security
**Goal**: Verify the zero-touch reporting link immediately neutralizes the threat and records the audit log.
- **Step 1**: Go to the portal and request a *second* Magic Link.
- **Step 2**: Open the new email, but instead of logging in, click the secondary security link ("*click here to immediately invalidate the link and report it*").
- **Step 3**: You should see a green "Report Submitted ✅" HTML confirmation page.
- **Step 4**: Go back to the email and try clicking the primary "Log In" link. It should securely block you with an "Invalid or expired token" error.
- **Step 5**: If you inspect the server's backend audit log, you should see the `portal.magic_link_abuse_reported` event recorded with the exact originating Client IP.

## 3. Sliding Session Expiration (90-Minute Window)
**Goal**: Verify the custom VPS configuration forces session termination if idle, but keeps it alive if active.
- **Step 1**: Log into the portal using a valid Magic Link.
- **Step 2**: Refresh the dashboard page once (this proves the sliding window resets the 90-minute timer).
- **Step 3**: Wait 91 minutes without interacting with the portal page or refreshing.
- **Step 4**: After 91 minutes, attempt to refresh the dashboard page.
- **Step 5**: You should be automatically booted out to the login screen, as the sliding window has strictly expired on the server.

## 4. CLI Version Compatibility Checks
**Goal**: Verify the LDM compatibility boundaries successfully enforce both soft and hard blocks on the CLI wrapper.
- **Step 1 (Raw Output)**: Build the current CLI (`make build`) and run `./bin/lfr-tunnel -check-version`. Ensure it outputs pure JSON mapping to the server's state: `{"latest_version":"v1.3.0","min_version":"v1.0.0"}`.
- **Step 2 (Hard Block)**: Temporarily edit `pkg/config/version.go` locally and change `Version = "v0.9.0"`. Run `make build` and attempt to start the tunnel normally (`./bin/lfr-tunnel`). It should immediately crash and exit with the **Hard Blocker** message ("*Minimum required version is v1.0.0*").
- **Step 3 (Soft Warning)**: Edit `pkg/config/version.go` again and change `Version = "v1.2.0"`. Run `make build` and start the tunnel. It should successfully connect to the VPS but clearly print a yellow **Soft Warning** into the console recommending an upgrade to `v1.3.0`.
- **Step 4 (Cleanup)**: Revert `pkg/config/version.go` back to `v1.3.0` and run `make build`.
