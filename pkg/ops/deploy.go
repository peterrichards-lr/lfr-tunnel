package ops

import (
	"fmt"
	"os"
)

// DeployCommand handles deploying server changes to the VPS.
func DeployCommand(args []string) {
	fmt.Println("=== Starting VPS Deployment ===")

	identityFile := "~/.ssh/id_vm6_networks_vps"
	if len(args) > 0 && args[0] == "-i" && len(args) > 1 {
		identityFile = args[1]
	}

	vpsUser := GetEnvOrDefault("VPS_USER", "peterrichards")
	vpsIP := GetEnvOrDefault("VPS_IP", "82.39.133.178")
	sshTarget := fmt.Sprintf("%s@%s", vpsUser, vpsIP)

	version := os.Getenv("VERSION")
	if version == "" {
		version = extractVersion() // Re-use from build.go
	}

	fmt.Printf("Building Linux binary (version: %s)...\n", version)
	ldflags := fmt.Sprintf("-s -w -X lfr-tunnel/pkg/config.Version=%s", version)
	err := RunCommandWithEnv([]string{"GOOS=linux", "GOARCH=amd64"}, "go", "build", "-ldflags", ldflags, "-trimpath", "-o", "bin/lfr-tunneld-linux", "./cmd/lfr-tunneld")
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
		"scripts/enable-maintenance.sh", "scripts/disable-maintenance.sh",
		"scripts/restore-with-maintenance.sh", "scripts/restore-backup.sh",
		"scripts/sync-offsite-backups.sh", "scripts/sync-offsite-backups.service",
		"scripts/sync-offsite-backups.timer",
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
	CheckFatal(err, "Failed to execute remote deployment commands")

	fmt.Println("=== Deployment Complete! ===")
}

// DeployClientsCommand handles deploying signed client binaries to the VPS.
func DeployClientsCommand(args []string) {
	fmt.Println("=== Deploying Client Binaries and Checksums to VPS ===")

	identityFile := "~/.ssh/id_vm6_networks_vps"
	if len(args) > 0 && args[0] == "-i" && len(args) > 1 {
		identityFile = args[1]
	}

	vpsUser := GetEnvOrDefault("VPS_USER", "peterrichards")
	vpsIP := GetEnvOrDefault("VPS_IP", "lfr-demo.se") // deploy-client-binaries.sh uses lfr-demo.se
	sshTarget := fmt.Sprintf("%s@%s", vpsUser, vpsIP)

	if !fileExists("dist/checksums.txt") {
		fmt.Println("ERROR: Client binaries or checksums.txt not found in dist/. Build and sign them first.")
		os.Exit(1)
	}

	fmt.Println("Uploading files from dist/ to", sshTarget)
	err := RunCommand("scp", "-i", identityFile, "-r", "dist", sshTarget+":/home/"+vpsUser+"/dist_tmp")
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
