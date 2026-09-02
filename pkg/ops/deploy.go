package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeployCommand handles deploying server changes to the VPS.
// drainWindowSeconds is how long a client is told it has before this gateway restarts: long
// enough for its migrator to notice on the next heartbeat and complete a registration
// elsewhere, short enough that a deploy is not held up waiting for it.
//
// drainWaitSeconds bounds how long the deploy waits for the node to actually empty. A client
// that is asleep, or whose user has shut the laptop, will never move -- so the deploy reports
// what is left and carries on rather than blocking indefinitely (#1303).
const (
	drainWindowSeconds = 45
	drainWaitSeconds   = 90
)

// uiDistIndex is the file //go:embed ui-dist/* needs in order for portal v2 to exist in the
// binary. Generated, and no longer committed since #1196.
const uiDistIndex = "pkg/server/ui-dist/index.html"

// BuildUI builds portal v2 into the embed directory, by invoking the same `make ui-dist` rule
// `make build` uses.
//
// deploy used to merely *check* that something was there and then embed it. That made the portal
// which shipped a function of when the operator last ran `make build`, not of the commit being
// deployed -- and in #1632 it put a 12-day-old portal v2 into production behind a correct
// version string, on a deploy that reported success on all five gateways.
//
// Building here rather than detecting staleness because detection can be defeated by an operator
// who does not know the rule, and not knowing the rule is the actual failure mode. RequireBuiltUI
// still runs afterwards as a post-condition.
func BuildUI() error {
	fmt.Println("Building portal v2 (make ui-dist)...")
	if err := RunCommand("make", "ui-dist"); err != nil {
		return fmt.Errorf(
			"building the UI failed: %w\n"+
				"portal v2 is embedded from pkg/server/ui-dist, which is gitignored, so a deploy\n"+
				"cannot proceed without building it -- see `make ui-dist`", err)
	}
	return nil
}

// RequireBuiltUI refuses to deploy a gateway whose portal v2 would answer 500.
//
// `make build` builds the UI and copies it into the embed directory; deploy cross-compiles
// directly and embeds whatever happens to be there. On a fresh clone that is just .gitkeep, and
// after a `make test` or a CI run it is a ZERO-BYTE index.html, because both create a dummy to
// satisfy the embed. Either way the deploy succeeds, reports success, and ships a gateway with no
// portal -- which is exactly what reached production in #1494.
//
// The size check is the point. Existence alone is satisfied by the dummy, which is the most
// likely way to hit this: anyone who has run the tests has one.
func RequireBuiltUI() error {
	info, err := os.Stat(uiDistIndex)
	if err != nil {
		return fmt.Errorf(
			"%s is missing, so this binary would embed no portal v2 and serve 500 for it.\n"+
				"Run `make build` first -- deploy cross-compiles but does not build the UI", uiDistIndex)
	}
	if info.Size() == 0 {
		return fmt.Errorf(
			"%s is empty -- this is the placeholder `make test` and CI create to satisfy the embed,\n"+
				"not a built UI. Run `make build` to produce a real one", uiDistIndex)
	}
	return nil
}

func DeployCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops deploy [-i identity_file] [-u user] [-s host] [-aws-region region] [-target name]")
		fmt.Println("\nBuilds lfr-tunneld for linux/amd64, uploads it plus static assets/i18n/")
		fmt.Println("templates/maintenance scripts to the central VPS, then over SSH installs the")
		fmt.Println("binary, enables maintenance mode, restarts lfr-tunneld, and disables")
		fmt.Println("maintenance mode. The target is resolved from (highest precedence first) the")
		fmt.Println("-i/-u/-s flags / VPS_USER,VPS_IP,LFT_IDENTITY_FILE env vars /")
		fmt.Println("lfr-tunnel-ops.yaml -- see lfr-tunnel-ops.yaml.example. -target/LFT_OPS_TARGET")
		fmt.Println("selects which named target to use from a multi-target config file. Real,")
		fmt.Println("live deployment, no dry-run mode.")
		fmt.Println("\n-aws-region (or AWS_REGION env var / central.aws_region in the yaml) is")
		fmt.Println("optional (#1050): if set and the target's EC2 instance is stopped (e.g. an")
		fmt.Println("edge caught inside its scheduled power-off window), it's started, waited on")
		fmt.Println("until SSH is reachable, deployed to, then stopped back afterward -- whether")
		fmt.Println("the deploy succeeds or fails. Left unset, EC2 power state is never touched.")
		return
	}

	fmt.Println("=== Starting VPS Deployment ===")

	flagIdentity, flagUser, flagHost, flagAWSRegion, flagTarget, err := parseTargetFlagsWithRegion("deploy", args)
	CheckFatal(err, "Failed to parse arguments")

	target, err := ResolveDeployTargetWithRegion(flagUser, flagHost, flagIdentity, flagAWSRegion, flagTarget)
	CheckFatal(err, "Failed to resolve deployment target")
	identityFile := target.IdentityFile
	vpsUser := target.User
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	// Registered before the power restore below so it runs *after* it: deferred functions
	// run last-in-first-out. Anything that fails past this point must exit through here
	// rather than CheckFatal, because CheckFatal calls os.Exit, which skips defers
	// entirely and would leave a started instance running outside its schedule.
	exitCode := 0
	defer func() {
		// A stranded instance fails the deploy even if everything else worked. Checked
		// here rather than in the restore itself because this defer runs after it, and
		// because an unattended run must not exit 0 having left a node running outside
		// its schedule (#1183).
		if failure := PowerRestoreFailure(); failure != "" {
			fmt.Fprintf(os.Stderr, "FATAL: deploy finished but %s\n", failure)
			exitCode = 1
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	// failed reports an error the way CheckFatal does, but WITHOUT os.Exit -- which skips
	// deferred functions, including the restorePower below. Every failure past that point has to
	// come through here and return, or a node started for this deploy is left running outside
	// its schedule while the process exits blaming something else entirely (#1453).
	//
	// Returns a bool rather than doing the return itself, because a helper cannot return from
	// its caller in Go. That makes `if failed(...) { return }` the pattern, and an omitted
	// `return` the way to get this wrong again -- which is what TestDeployCommandHasNoCheckFatal
	// AfterPowerRestore guards, since the mistake is invisible on every successful deploy.
	failed := func(err error, msg string) bool {
		if err == nil {
			return false
		}
		fmt.Fprintf(os.Stderr, "FATAL: %s: %v\n", msg, err)
		exitCode = 1
		return true
	}

	// aws_region used to be the whole switch for power management. It is now just one of
	// the values handed to a hook, so a config still carrying it alone would quietly stop
	// managing power -- and silently leaving a started node running is the exact failure
	// #1183 exists to catch. Fail here with the line to add (#1187).
	//
	// Checked in deploy rather than in ResolveDeployTarget because deploy is the only
	// command that manages power; the others share that resolver and have no business
	// failing over a power hook they never use.
	CheckFatal(checkPowerConfig(target), "Power management is misconfigured")

	// AWSRegion and InstanceTag are not interpreted here -- they are handed to whatever
	// hook the operator configured, which is the only thing that knows what they mean
	// (#1187).
	restorePower, err := ensureInstanceRunning(target.Host, powerHook{
		Path: target.PowerHook,
		Env: []string{
			"AWS_REGION=" + target.AWSRegion,
			"LFT_INSTANCE_TAG=" + target.InstanceTag,
		},
	})
	CheckFatal(err, "Failed to ensure target instance is running")
	defer restorePower()

	version := os.Getenv("VERSION")
	if version == "" {
		version = extractVersion() // Re-use from build.go
	}

	// Build the UI here rather than trusting whatever is on disk: ui-dist is gitignored, so
	// without this the portal that ships is whatever the operator last built, which is how a
	// 12-day-old portal v2 reached production in #1632.
	if err := BuildUI(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		exitCode = 1
		return
	}

	// Still checked afterwards, as a post-condition rather than a precondition: a build that
	// exits 0 but produces nothing usable must not reach the box either (#1494).
	if err := RequireBuiltUI(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		exitCode = 1
		return
	}

	fmt.Printf("Building Linux binary (version: %s)...\n", version)
	ldflags := fmt.Sprintf("-s -w -X lfr-tunnel/pkg/config.Version=%s", version)
	err = RunCommandWithEnv([]string{"GOOS=linux", "GOARCH=amd64"}, "go", "build", "-ldflags", ldflags, "-trimpath", "-o", "bin/lfr-tunneld-linux", "./cmd/lfr-tunneld")
	if failed(err, "Failed to build lfr-tunneld for Linux") {
		return
	}

	fmt.Println("Uploading binary to VPS...")
	err = RunCommand("scp", "-i", identityFile, "bin/lfr-tunneld-linux", sshTarget+":/home/"+vpsUser+"/lfr-tunneld")
	if failed(err, "Failed to SCP binary") {
		return
	}

	fmt.Println("Uploading error pages, static assets, translations, and templates...")
	err = RunCommand("scp", "-i", identityFile, "-r", "resources/server/error_pages", sshTarget+":/home/"+vpsUser+"/")
	if failed(err, "Failed to SCP error_pages") {
		return
	}
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/static", sshTarget+":/home/"+vpsUser+"/")
	if failed(err, "Failed to SCP static") {
		return
	}
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/i18n", sshTarget+":/home/"+vpsUser+"/")
	if failed(err, "Failed to SCP i18n") {
		return
	}
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/templates", sshTarget+":/home/"+vpsUser+"/")
	if failed(err, "Failed to SCP templates") {
		return
	}

	fmt.Println("Uploading maintenance and backup scripts...")
	scripts := []string{
		"scripts/common/enable-maintenance.sh", "scripts/common/disable-maintenance.sh",
		"scripts/common/restore-with-maintenance.sh", "scripts/common/restore-backup.sh",
		"scripts/common/drain-and-wait.sh",
		"scripts/liferay/vm6/sync-offsite-backups.sh", "scripts/liferay/vm6/sync-offsite-backups.service",
		"scripts/liferay/vm6/sync-offsite-backups.timer",
	}
	for _, script := range scripts {
		if fileExists(script) {
			err = RunCommand("scp", "-i", identityFile, script, sshTarget+":/home/"+vpsUser+"/")
			if failed(err, "Failed to SCP script: "+script) {
				return
			}
		}
	}

	remoteScript := `
	sudo mv /home/` + vpsUser + `/lfr-tunneld /usr/local/bin/lfr-tunneld
	sudo chmod +x /usr/local/bin/lfr-tunneld

	sudo mv /home/` + vpsUser + `/enable-maintenance.sh /usr/local/bin/enable-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/disable-maintenance.sh /usr/local/bin/disable-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/restore-with-maintenance.sh /usr/local/bin/restore-with-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/restore-backup.sh /usr/local/bin/restore-backup.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/drain-and-wait.sh /usr/local/bin/drain-and-wait.sh 2>/dev/null || true
	sudo chmod +x /usr/local/bin/*.sh 2>/dev/null || true

	sudo mkdir -p /var/www/lfr-tunnel/error_pages
	sudo cp -r /home/` + vpsUser + `/error_pages/* /var/www/lfr-tunnel/error_pages/ 2>/dev/null || true
	sudo mkdir -p /var/www/lfr-tunnel/static
	sudo cp -r /home/` + vpsUser + `/static/* /var/www/lfr-tunnel/static/ 2>/dev/null || true
	
	sudo mkdir -p /etc/lfr-tunneld/i18n /etc/lfr-tunneld/templates
	sudo cp -r /home/` + vpsUser + `/i18n/*.properties /etc/lfr-tunneld/i18n/ 2>/dev/null || true
	sudo cp -r /home/` + vpsUser + `/templates/* /etc/lfr-tunneld/templates/ 2>/dev/null || true
	
	rm -rf /home/` + vpsUser + `/error_pages /home/` + vpsUser + `/static /home/` + vpsUser + `/i18n /home/` + vpsUser + `/templates

	if [ -x /usr/local/bin/enable-maintenance.sh ]; then
		sudo /usr/local/bin/enable-maintenance.sh "System Upgrade" "Deploying new Gateway version" 120 || true
	fi

	# Drain before restarting (#1303). Maintenance mode above stops NEW connections arriving;
	# it does nothing about the ones already attached, which the restart kills outright. This
	# announces the restart to them instead, so they move to another gateway first -- the same
	# make-before-break path a scheduled stop uses (#1246), where a client that moved on the
	# warning had no downtime and one that waited to be dropped was down 24m36s.
	#
	# One copy, in scripts/common/drain-and-wait.sh, installed just above -- before this point
	# on purpose, so it is present even on the first deploy after this change. It used to be
	# this bash block, which is why every maintenance path written before #1305 silently had
	# no drain and every one after had to know to copy it (#1455). Guarded on existence and
	# never fatal: an older box, or one where the scp was skipped, behaves as it did before.
	if [ -x /usr/local/bin/drain-and-wait.sh ]; then
		sudo /usr/local/bin/drain-and-wait.sh announce ` + fmt.Sprint(drainWindowSeconds) + ` ` + fmt.Sprint(drainWaitSeconds) + ` "Gateway is restarting for a deployment" || true
	fi

	sudo systemctl restart lfr-tunneld

	# Clear the announcement, or clients keep migrating away from a node that is staying up.
	if [ -x /usr/local/bin/drain-and-wait.sh ]; then
		sleep 2
		sudo /usr/local/bin/drain-and-wait.sh clear || true
	fi

	if [ -x /usr/local/bin/disable-maintenance.sh ]; then
		sleep 2
		sudo /usr/local/bin/disable-maintenance.sh || true
	fi
	`

	fmt.Println("Executing remote deployment configuration...")
	err = RunCommand("ssh", "-i", identityFile, sshTarget, remoteScript)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Failed to execute remote deployment commands: %v\n", err)
		exitCode = 1
		return
	}

	// Confirm the node is serving what was just built, before the deferred restore may
	// power it back down. Without this a failed or partial deploy to a scheduled-off edge
	// was indistinguishable from a good one until the box next woke -- which happened
	// twice, once leaving a node on the previous version with nothing reporting a problem
	// (issue #1176).
	if err := verifyDeployedVersion(target.Host, version, 90*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Deployment could not be verified: %v\n", err)
		exitCode = 1
		return
	}

	// The version check proves the right binary is running; this proves it is usable.
	if err := verifyPortalV2(target.Host); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		exitCode = 1
		return
	}

	fmt.Println("=== Deployment Complete! ===")
}

// verifyDeployedVersion polls the gateway's own /api/version until it reports want.
//
// Asks the node directly rather than going through central: an edge reports its version to
// central on the control-channel handshake, but that is a second hop with its own timing,
// and this needs to answer "is the binary I just installed the one now serving" rather than
// "has central noticed yet".
// verifyPortalV2 checks the deployed gateway actually serves its portal.
//
// The version check above proves the right BINARY is running. It says nothing about what is inside
// it, and a gateway with no embedded UI passes every other check while answering 500 for the
// portal -- up, correct version, and unusable (#1494). This is the check that would have caught
// that from the deploying side rather than when somebody opened the page.
//
// Not fatal on a non-200 other than 500: an edge legitimately has no portal, and a deployment
// behind maintenance mode answers 503. The specific failure being caught is "UI not built".
func verifyPortalV2(host string) error {
	return verifyPortalV2At("https://" + host)
}

func verifyPortalV2At(baseURL string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/portalv2")
	if err != nil {
		// A gateway that cannot be reached at all is already reported by the version check; not
		// worth failing twice for one problem.
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusInternalServerError {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err == nil && strings.Contains(string(body), "UI not built") {
		return fmt.Errorf(
			"the deployed gateway serves 500 for /portalv2: %q.\n"+
				"The binary embedded no UI. Run `make build` and deploy again", strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("the deployed gateway serves 500 for /portalv2")
}

func verifyDeployedVersion(host, want string, timeout time.Duration) error {
	return verifyDeployedVersionAt("https://"+host, want, timeout, 5*time.Second)
}

// verifyDeployedVersionAt is the testable form: baseURL and the poll interval are explicit
// so tests do not have to sit through the real cadence.
func verifyDeployedVersionAt(baseURL, want string, timeout, interval time.Duration) error {
	url := strings.TrimRight(baseURL, "/") + "/api/version"
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(timeout)

	fmt.Printf("Verifying %s is serving %s...\n", baseURL, want)

	var lastSeen, lastErr string
	for time.Now().Before(deadline) {
		got, err := fetchServerVersion(client, url)
		switch {
		case err != nil:
			// Expected for the first few seconds: the service is restarting and nginx
			// answers 502, or the maintenance page is still up.
			lastErr = err.Error()
		case got == want:
			fmt.Printf("Verified: %s is serving %s.\n", baseURL, got)
			return nil
		default:
			lastSeen = got
		}
		time.Sleep(interval)
	}

	if lastSeen != "" {
		return fmt.Errorf("%s is still serving %s, expected %s -- the deploy did not take", baseURL, lastSeen, want)
	}
	return fmt.Errorf("%s never reported a version within %s (last error: %s)", baseURL, timeout, lastErr)
}

func fetchServerVersion(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var payload struct {
		ServerVersion string `json:"server_version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload); err != nil {
		return "", err
	}
	return payload.ServerVersion, nil
}

// DeployClientsCommand handles deploying signed client binaries to the VPS.
func DeployClientsCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops deploy-clients [-i identity_file] [-u user] [-s host] [-target name]")
		fmt.Println("\nUploads dist/ (built + signed client binaries and checksums.txt*) to the")
		fmt.Println("central VPS and moves them into the web server's static downloads")
		fmt.Println("directory. Requires dist/checksums.txt to already exist -- run build then")
		fmt.Println("sign first. The target is resolved from (highest precedence first) the")
		fmt.Println("-i/-u/-s flags / VPS_USER,VPS_IP,LFT_IDENTITY_FILE env vars /")
		fmt.Println("lfr-tunnel-ops.yaml -- see lfr-tunnel-ops.yaml.example. -target/LFT_OPS_TARGET")
		fmt.Println("selects which named target to use from a multi-target config file. Real,")
		fmt.Println("live upload, no dry run.")
		fmt.Println("\nRefuses to run unless dist/'s build manifest matches pkg/config/version.go,")
		fmt.Println("and afterwards verifies the gateway is serving the bytes just uploaded")
		fmt.Println("(#1279). -allow-stale overrides the first check; -skip-verify the second.")
		fmt.Println("\nAlso refuses to publish clients built with no default gateway, which force")
		fmt.Println("every user to pass -server and so pin the client (#1692).")
		fmt.Println("-allow-no-default overrides that, for a deployment that wants none.")
		return
	}

	fmt.Println("=== Deploying Client Binaries and Checksums to VPS ===")

	flags, err := parseDeployClientsFlags("deploy-clients", args)
	CheckFatal(err, "Failed to parse arguments")

	target, err := ResolveDeployTarget(flags.user, flags.host, flags.identityFile, flags.target)
	CheckFatal(err, "Failed to resolve deployment target")
	identityFile := target.IdentityFile
	vpsUser := target.User
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	if !fileExists("dist/checksums.txt") {
		fmt.Println("ERROR: Client binaries or checksums.txt not found in dist/. Build and sign them first.")
		os.Exit(1)
	}

	// Say which version is about to be published, before publishing it. This alone would have
	// made the #1279 incident visible immediately: every step reported success, and none of them
	// said what they were shipping.
	manifest := RequireCurrentDist("dist", "deploy-clients", flags.allowStale)

	// Before the upload, not after: a client with no default gateway is unusable as downloaded,
	// and unlike a stale version it cannot be spotted by looking at what is being published
	// (#1692). The build already reported it, and that report was ignored twice because it
	// scrolls past in a successful build -- so this one stops.
	RequireDefaultGateway(manifest, "deploy-clients", flags.allowNoDefault)

	fmt.Printf("Publishing client binaries for %s to %s.\n", manifest.Version, target.Host)

	fmt.Println("Uploading files from dist/ to", sshTarget)
	err = RunCommand("scp", "-i", identityFile, "-r", "dist", sshTarget+":/home/"+vpsUser+"/dist_tmp")
	CheckFatal(err, "Failed to SCP client binaries")

	fmt.Println("Moving files to secure web server downloads directory on VPS...")
	remoteScript := `
	sudo mkdir -p /var/www/lfr-tunnel/static/downloads
	sudo cp /home/` + vpsUser + `/dist_tmp/lfr-tunnel-* /home/` + vpsUser + `/dist_tmp/checksums.txt* /home/` + vpsUser + `/dist_tmp/` + BuildManifestName + ` /var/www/lfr-tunnel/static/downloads/ 2>/dev/null || true
	sudo chmod -R +r /var/www/lfr-tunnel/static/downloads
	rm -rf /home/` + vpsUser + `/dist_tmp
	`
	err = RunCommand("ssh", "-i", identityFile, sshTarget, remoteScript)
	CheckFatal(err, "Failed to move client binaries on VPS")

	if flags.skipVerify {
		fmt.Println("Skipping post-upload verification (-skip-verify).")
	} else {
		err = verifyPublishedClientsAt("https://"+target.Host, "dist", &http.Client{Timeout: 2 * time.Minute})
		CheckFatal(err, "Client binaries were uploaded but the gateway is not serving them")
	}

	fmt.Printf("=== Client Binaries Deployment Complete! Published %s ===\n", manifest.Version)
}

// --- Post-upload verification of the client downloads (#1279) ---
//
// `deploy` already proves the gateway is serving the version it just installed ("Verifying ...
// is serving vX"). The client download path had no equivalent, which is how binaries from an
// earlier version were served for a full day while every step reported success. "The copy
// succeeded" and "the right bytes are being served" are different claims, and only the second
// one matters to a user running --upgrade.

// parseChecksums reads the `<sha256>  <name>` lines that generateChecksums writes.
//
// Compared as a parsed map rather than as raw bytes on purpose: it makes the mismatch report
// name the file that differs, and it is immune to a trailing-newline difference introduced in
// transit, which would otherwise read as a deploy failure.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		out[fields[1]] = fields[0]
	}
	return out
}

// diffChecksums describes how published differs from local, or returns "" when they agree.
func diffChecksums(local, published map[string]string) string {
	var problems []string
	for name, want := range local {
		got, ok := published[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not published", name))
		case got != want:
			problems = append(problems, fmt.Sprintf("%s published as %s, expected %s", name, short(got), short(want)))
		}
	}
	// Extra published entries are reported but are not a failure on their own: an older
	// artefact left in the downloads directory is untidy, not a wrong answer to --upgrade.
	var extra []string
	for name := range published {
		if _, ok := local[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(problems)
	sort.Strings(extra)
	if len(extra) > 0 {
		fmt.Printf("Note: the downloads directory also holds %s (left from an earlier deploy).\n",
			strings.Join(extra, ", "))
	}
	if len(problems) == 0 {
		return ""
	}
	return strings.Join(problems, "; ")
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// verifyPublishedClientsAt is the testable form: the base URL is explicit so tests can point at
// an httptest server rather than a real gateway.
func verifyPublishedClientsAt(baseURL, distDir string, client *http.Client) error {
	localRaw, err := os.ReadFile(filepath.Join(distDir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("could not read local checksums: %w", err)
	}
	local := parseChecksums(localRaw)
	if len(local) == 0 {
		return fmt.Errorf("local checksums.txt lists no artefacts")
	}

	downloads := strings.TrimRight(baseURL, "/") + "/static/downloads"

	fmt.Printf("Verifying %s is serving what was just uploaded...\n", downloads)

	publishedRaw, err := httpGetLimited(client, downloads+"/checksums.txt", 1<<20)
	if err != nil {
		return fmt.Errorf("could not fetch the published checksums.txt: %w", err)
	}
	published := parseChecksums(publishedRaw)

	if problems := diffChecksums(local, published); problems != "" {
		return fmt.Errorf("the published checksums do not match what was uploaded: %s", problems)
	}
	fmt.Printf("Verified: all %d published checksums match.\n", len(local))

	// The checksums agreeing only proves the manifest was replaced. Fetch one real binary and
	// hash it, so a stale binary sitting behind a fresh checksums.txt cannot pass. Prefer
	// linux-amd64: it is what install.sh fetches by default, so it is the one most users get.
	probe := "lfr-tunnel-linux-amd64"
	if _, ok := local[probe]; !ok {
		names := make([]string, 0, len(local))
		for name := range local {
			names = append(names, name)
		}
		sort.Strings(names)
		probe = names[0]
	}

	fmt.Printf("Fetching %s to confirm the bytes match...\n", probe)
	body, err := httpGetLimited(client, downloads+"/"+probe, 256<<20)
	if err != nil {
		return fmt.Errorf("could not fetch published %s: %w", probe, err)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if got != local[probe] {
		return fmt.Errorf("published %s hashes to %s, expected %s -- the downloads directory is "+
			"serving different bytes from the ones just uploaded", probe, short(got), short(local[probe]))
	}
	fmt.Printf("Verified: published %s matches (%s).\n", probe, short(got))
	return nil
}

func httpGetLimited(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
