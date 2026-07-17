package ops

import (
	"fmt"
)

// MaintenanceCommand toggles Nginx maintenance mode on the VPS.
func MaintenanceCommand(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: lfr-tunnel-ops maintenance <enable|disable> [-i identity_file]")
		return
	}

	action := args[0]
	identityFile := "~/.ssh/id_vm6_networks_vps"
	if len(args) > 1 && args[1] == "-i" && len(args) > 2 {
		identityFile = args[2]
	}

	vpsUser := GetEnvOrDefault("VPS_USER", "peterrichards")
	vpsIP := GetEnvOrDefault("VPS_IP", "82.39.133.178")
	sshTarget := fmt.Sprintf("%s@%s", vpsUser, vpsIP)

	if action == "enable" {
		fmt.Println("Enabling maintenance mode on the VPS...")
		err := RunCommand("ssh", "-i", identityFile, sshTarget, `sudo /usr/local/bin/enable-maintenance.sh -a "Maintenance" -r "System operations in progress" -d "15m"`)
		CheckFatal(err, "Failed to enable maintenance mode")
	} else if action == "disable" {
		fmt.Println("Disabling maintenance mode on the VPS...")
		err := RunCommand("ssh", "-i", identityFile, sshTarget, "sudo /usr/local/bin/disable-maintenance.sh")
		CheckFatal(err, "Failed to disable maintenance mode")
	} else {
		fmt.Printf("Unknown action: %q. Expected 'enable' or 'disable'\n", action)
	}
}

// DiagnoseCommand runs diagnostics on the VPS.
func DiagnoseCommand(args []string) {
	fmt.Println("=== Running Gateway Diagnostics ===")

	identityFile := "~/.ssh/id_vm6_networks_vps"
	if len(args) > 0 && args[0] == "-i" && len(args) > 1 {
		identityFile = args[1]
	}

	vpsUser := GetEnvOrDefault("VPS_USER", "peterrichards")
	vpsIP := GetEnvOrDefault("VPS_IP", "82.39.133.178")
	sshTarget := fmt.Sprintf("%s@%s", vpsUser, vpsIP)

	// A lightweight translation of diagnose-gateway.sh
	script := `
echo "1. System Uptime & Load:"
uptime

echo ""
echo "2. Systemd Service Status:"
systemctl is-active lfr-tunneld
systemctl status lfr-tunneld --no-pager | head -n 10

echo ""
echo "3. Nginx Status:"
systemctl is-active nginx
sudo nginx -t

echo ""
echo "4. UFW Firewall Rules:"
sudo ufw status | grep -E "80/tcp|443/tcp|22/tcp|25/tcp"

echo ""
echo "5. Let's Encrypt Certificates:"
sudo ls -la /etc/letsencrypt/live/

echo ""
echo "6. Recent Gateway Errors:"
sudo journalctl -u lfr-tunneld -p err -n 10 --no-pager
`

	err := RunCommand("ssh", "-i", identityFile, sshTarget, script)
	CheckFatal(err, "Failed to run diagnostics")
	fmt.Println("=== Diagnostics Complete ===")
}
