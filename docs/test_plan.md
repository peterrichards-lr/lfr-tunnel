# Portal Management Test Plan

Here is a comprehensive checklist of the management and security flows you should test to verify the portal is working exactly as intended.

## Hard Reset (Developer Only)

If you ever need to completely wipe the system to test from scratch, SSH into the VPS and execute this exact sequence to ensure memory caches and the SQLite database are fully purged:

```bash
sudo systemctl stop lfr-tunneld
sudo sh -c 'rm -f /etc/lfr-tunneld/lfr-tunnel.db*'
sudo systemctl start lfr-tunneld
```

## Test Plan

### 0. Initial Bootstrap (Owner Setup)

- [x] **First Owner Login:** Enter your whitelisted owner email (from `server-config.yaml`) into the "Log In with Email" box and request a magic link.
- [x] **Account Provisioning:** Click the magic link. Verify you bypass the registration queue and are taken directly to the dashboard, and your Owner account is automatically populated into the database.
- [x] **First Time Welcome:** Verify the system detects this is your first session and prints "We're glad you're here. This appears to be your first time logging in."
- [x] **Client Setup Banner:** Verify the dashboard automatically detects your OS (macOS, Windows, or Linux) and presents a prominent banner to download the latest client binary.
- [x] **Complete Profile:** Navigate to the **Account** tab to adjust your First/Last name or Theme preferences, then hit Save.

### 1. The New User Registration & Moderation Flow

- [ ] **Register a dummy account:** Open an Incognito window and try to "Create Account" using an email address on your whitelisted domain (e.g., `test@fastmail.thefuturetoday.net`).
- [ ] **Verify the Email:** Check the dummy inbox for the verification email and click the link.
- [ ] **Complete Profile:** Fill out the First Name, Last Name, and Theme preference. You should be placed into a "Pending Approval" state.
- [ ] **Admin Moderation:** Log into your primary Owner account. Check the **Registrations** tab in the sidebar (you should see a red notification badge).
- [ ] **Approve/Deny:** Click "Approve" on the dummy user. Verify the dummy email address receives the "Account Approved" welcome email.

### 2. Authentication & Session Management

- [ ] **Magic Link Login:** From the dummy account, request a login link. Verify it arrives and successfully logs you in without a password.
- [ ] **Last Login Banner:** Log out and log back in (using a fresh magic link). Verify the banner at the top of the dashboard displays your *previous* login timestamp and IP address.
- [ ] **Magic Link Auto-Invalidation:** Request a magic link. Do *not* click it. Wait 1 minute, and request a *second* magic link. Click the *second* link to log in. Log out, then try to click the *first* link. Verify you are correctly denied access because the older token was automatically invalidated by your new login.
- [ ] **Session Expiration:** *(Optional)* Wait 15 minutes for a Magic Link to expire naturally, or check back in an hour to ensure the background garbage collector successfully prunes it from the database.

### 3. Personal Account Settings & Aesthetics

- [ ] **Preferred Name Greeting:** In the **Account** tab, set a "Preferred Name" and hit save. Log out and log back in. Verify the dashboard header dynamically updates to "Welcome Back, [Preferred Name]!" instead of your raw first name or email. Also, check that subsequent system emails address you by this name.
- [ ] **Theme Switching:** In the **Account** tab, change your theme to "Dark" or "Light" and hit save. Ensure the UI instantly repaints.
- [ ] **Time of Day Engine:** Change your theme to "Time of Day". Open your browser's developer console and run `testTimeTheme(10)` (morning) and `testTimeTheme(23)` (night) to watch the UI automatically adapt to the sun's schedule.
- [ ] **Notification Toggles:** Toggle your email notification preferences, save, and ensure the settings persist across hard page refreshes.

### 4. Admin Security & Control Panels

- [ ] **User Suspension:** Go to the **Users** tab (as the Owner). Try clicking "Revoke" on the dummy account you created. Verify their status changes to `revoked`.
- [ ] **IP Blacklisting:** Go to the **IP Blacklist** tab. Add a fake IP address to the ban list. Ensure it appears in the table. 
- [ ] **Token Management:** Go to the **API Tokens** tab. Generate a test Personal Access Token. Verify it appears in the list and that clicking "Revoke" successfully deletes it.
- [ ] **Audit Log Verification:** Open the **Audit Logs** tab under the Reporting section. You should see a chronological ledger of everything you just did (e.g., `admin.login`, `user.registered`, `user.approved`).

### 5. Analytics & Monitoring

- [ ] **Admin Global View:** Go to the **System Analytics** tab as the Owner. Verify you see the "Global Bandwidth Over Time" line chart and the "Top Users" bar chart.
- [ ] **Client Telemetry Table:** Still in the Admin Global View, verify the "Client Version Distribution" table populates automatically with the version and OS data harvested from user CLI connections.
- [ ] **Standard User View:** Log in as your dummy account and go to the **System Analytics** tab. Verify the Admin "Global Overview" section is entirely hidden, and they can *only* see their personal "My Usage" charts.

### 6. Threat Mitigation & Edge Cases

- [ ] **Account Enumeration Prevention:** Open an Incognito window and click "Create Account". Attempt to register using an email address that is *already registered* (e.g. your Owner email or dummy account email).
  - Verify you see a generic HTTP 200 "Success" UI message (*"Registration request submitted..."*) which prevents an attacker from deducing if the account exists.
  - Log into the portal as the Owner, check the **Audit Logs**, and verify there is a silent `auth.registration_attempt_existing` event recorded detailing the enumeration attempt.
- [ ] **Abuse Reporting (Registration):** Register a brand new dummy email address. In the verification email, click the "Didn't request this email? Report" link. Verify that clicking the link immediately invalidates the token, preventing the account from being created, and logs the abuse report.
- [ ] **Abuse Reporting (Login):** Request a Magic Link for your account. Instead of logging in, click the "Report" link at the bottom of the email. Verify that the magic link is instantly revoked and cannot be used to log in.
- [ ] **API DDoS & Brute-Force Auto-Ban:** Open a terminal and run a simple bash loop to hammer the API endpoints from your IP address (this tests the 10req/sec, 20 burst limit):
  - `for i in {1..55}; do curl -s -o /dev/null -w "%{http_code}\n" https://<your-vps-domain>/api/version; done`
  - Verify that around request ~21, the server starts returning `429 Too Many Requests`.
  - Verify that at request 50, the server transitions to returning `403 Forbidden` permanently.
  - Log into the portal (from a *different* IP/network, e.g. a mobile hotspot) and check the **IP Blacklist** tab to verify your original IP was successfully Auto-Banned by the Rate Limiter. Check your inbox for the automated threat alert email.
