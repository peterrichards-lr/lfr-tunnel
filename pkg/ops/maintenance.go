package ops

import (
	"fmt"
	"os"
)

// MaintenanceCommand toggles Nginx maintenance mode on the VPS.
func MaintenanceCommand(args []string) {
	if len(args) < 1 || IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops maintenance <enable|disable> [-i identity_file] [-u user] [-s host]")
		fmt.Println("\nEnables or disables Nginx maintenance mode on the central VPS over SSH.")
		fmt.Println("The target is resolved from (highest precedence first) the -i/-u/-s flags /")
		fmt.Println("VPS_USER,VPS_IP,LFT_IDENTITY_FILE env vars / lfr-tunnel-ops.yaml -- see")
		fmt.Println("lfr-tunnel-ops.yaml.example. Real, live effect on production traffic.")
		return
	}

	action := args[0]
	flagIdentity, flagUser, flagHost, err := parseTargetFlags("maintenance", args[1:])
	CheckFatal(err, "Failed to parse arguments")

	target, err := ResolveDeployTarget(flagUser, flagHost, flagIdentity)
	CheckFatal(err, "Failed to resolve deployment target")
	identityFile := target.IdentityFile
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	switch action {
	case "enable":
		fmt.Println("Enabling maintenance mode on the VPS...")
		err := RunCommand("ssh", "-i", identityFile, sshTarget, `sudo /usr/local/bin/enable-maintenance.sh -a "Maintenance" -r "System operations in progress" -d "15m"`)
		CheckFatal(err, "Failed to enable maintenance mode")
	case "disable":
		fmt.Println("Disabling maintenance mode on the VPS...")
		err := RunCommand("ssh", "-i", identityFile, sshTarget, "sudo /usr/local/bin/disable-maintenance.sh")
		CheckFatal(err, "Failed to disable maintenance mode")
	default:
		fmt.Println("Usage: make maintenance action=enable|disable")
		os.Exit(1)
	}
}

// DiagnoseCommand runs diagnostics on the VPS.
func DiagnoseCommand(args []string) {
	if IsHelpRequest(args) {
		fmt.Println("Usage: lfr-tunnel-ops diagnose [-i identity_file] [-u user] [-s host]")
		fmt.Println("\nSSHes into the central VPS and runs read-only checks: uptime,")
		fmt.Println("lfr-tunneld/nginx status, UFW rules, cert listing, recent error logs. Does")
		fmt.Println("not modify anything on the VPS. The target is resolved from (highest")
		fmt.Println("precedence first) the -i/-u/-s flags / VPS_USER,VPS_IP,LFT_IDENTITY_FILE env")
		fmt.Println("vars / lfr-tunnel-ops.yaml -- see lfr-tunnel-ops.yaml.example.")
		return
	}

	fmt.Println("=== Running Gateway Diagnostics ===")

	flagIdentity, flagUser, flagHost, err := parseTargetFlags("diagnose", args)
	CheckFatal(err, "Failed to parse arguments")

	target, err := ResolveDeployTarget(flagUser, flagHost, flagIdentity)
	CheckFatal(err, "Failed to resolve deployment target")
	identityFile := target.IdentityFile
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

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

	err = RunCommand("ssh", "-i", identityFile, sshTarget, script)
	CheckFatal(err, "Failed to run diagnostics")
	fmt.Println("=== Diagnostics Complete ===")
}
