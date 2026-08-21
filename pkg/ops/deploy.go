package ops

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DeployCommand handles deploying server changes to the VPS.
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
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	restorePower, err := ensureInstanceRunning(target.Host, target.AWSRegion)
	CheckFatal(err, "Failed to ensure target instance is running")
	defer restorePower()

	version := os.Getenv("VERSION")
	if version == "" {
		version = extractVersion() // Re-use from build.go
	}

	fmt.Printf("Building Linux binary (version: %s)...\n", version)
	ldflags := fmt.Sprintf("-s -w -X lfr-tunnel/pkg/config.Version=%s", version)
	err = RunCommandWithEnv([]string{"GOOS=linux", "GOARCH=amd64"}, "go", "build", "-ldflags", ldflags, "-trimpath", "-o", "bin/lfr-tunneld-linux", "./cmd/lfr-tunneld")
	CheckFatal(err, "Failed to build lfr-tunneld for Linux")

	fmt.Println("Uploading binary to VPS...")
	err = RunCommand("scp", "-i", identityFile, "bin/lfr-tunneld-linux", sshTarget+":/home/"+vpsUser+"/lfr-tunneld")
	CheckFatal(err, "Failed to SCP binary")

	fmt.Println("Uploading error pages, static assets, translations, and templates...")
	err = RunCommand("scp", "-i", identityFile, "-r", "resources/server/error_pages", sshTarget+":/home/"+vpsUser+"/")
	CheckFatal(err, "Failed to SCP error_pages")
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/static", sshTarget+":/home/"+vpsUser+"/")
	CheckFatal(err, "Failed to SCP static")
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/i18n", sshTarget+":/home/"+vpsUser+"/")
	CheckFatal(err, "Failed to SCP i18n")
	err = RunCommand("scp", "-i", identityFile, "-r", "pkg/server/templates", sshTarget+":/home/"+vpsUser+"/")
	CheckFatal(err, "Failed to SCP templates")

	fmt.Println("Uploading maintenance and backup scripts...")
	scripts := []string{
		"scripts/common/enable-maintenance.sh", "scripts/common/disable-maintenance.sh",
		"scripts/common/restore-with-maintenance.sh", "scripts/common/restore-backup.sh",
		"scripts/liferay/vm6/sync-offsite-backups.sh", "scripts/liferay/vm6/sync-offsite-backups.service",
		"scripts/liferay/vm6/sync-offsite-backups.timer",
	}
	for _, script := range scripts {
		if fileExists(script) {
			err = RunCommand("scp", "-i", identityFile, script, sshTarget+":/home/"+vpsUser+"/")
			CheckFatal(err, "Failed to SCP script: "+script)
		}
	}

	remoteScript := `
	sudo mv /home/` + vpsUser + `/lfr-tunneld /usr/local/bin/lfr-tunneld
	sudo chmod +x /usr/local/bin/lfr-tunneld

	sudo mv /home/` + vpsUser + `/enable-maintenance.sh /usr/local/bin/enable-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/disable-maintenance.sh /usr/local/bin/disable-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/restore-with-maintenance.sh /usr/local/bin/restore-with-maintenance.sh 2>/dev/null || true
	sudo mv /home/` + vpsUser + `/restore-backup.sh /usr/local/bin/restore-backup.sh 2>/dev/null || true
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

	sudo systemctl restart lfr-tunneld

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

	fmt.Println("=== Deployment Complete! ===")
}

// verifyDeployedVersion polls the gateway's own /api/version until it reports want.
//
// Asks the node directly rather than going through central: an edge reports its version to
// central on the control-channel handshake, but that is a second hop with its own timing,
// and this needs to answer "is the binary I just installed the one now serving" rather than
// "has central noticed yet".
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
		return
	}

	fmt.Println("=== Deploying Client Binaries and Checksums to VPS ===")

	flagIdentity, flagUser, flagHost, flagTarget, err := parseTargetFlags("deploy-clients", args)
	CheckFatal(err, "Failed to parse arguments")

	target, err := ResolveDeployTarget(flagUser, flagHost, flagIdentity, flagTarget)
	CheckFatal(err, "Failed to resolve deployment target")
	identityFile := target.IdentityFile
	vpsUser := target.User
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	if !fileExists("dist/checksums.txt") {
		fmt.Println("ERROR: Client binaries or checksums.txt not found in dist/. Build and sign them first.")
		os.Exit(1)
	}

	fmt.Println("Uploading files from dist/ to", sshTarget)
	err = RunCommand("scp", "-i", identityFile, "-r", "dist", sshTarget+":/home/"+vpsUser+"/dist_tmp")
	CheckFatal(err, "Failed to SCP client binaries")

	fmt.Println("Moving files to secure web server downloads directory on VPS...")
	remoteScript := `
	sudo mkdir -p /var/www/lfr-tunnel/static/downloads
	sudo cp /home/` + vpsUser + `/dist_tmp/lfr-tunnel-* /home/` + vpsUser + `/dist_tmp/checksums.txt* /var/www/lfr-tunnel/static/downloads/ 2>/dev/null || true
	sudo chmod -R +r /var/www/lfr-tunnel/static/downloads
	rm -rf /home/` + vpsUser + `/dist_tmp
	`
	err = RunCommand("ssh", "-i", identityFile, sshTarget, remoteScript)
	CheckFatal(err, "Failed to move client binaries on VPS")

	fmt.Println("=== Client Binaries Deployment Complete! ===")
}
