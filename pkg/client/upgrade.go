package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jedisct1/go-minisign"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/osutil"
)

var (
	githubAPIBase  = "https://api.github.com"
	targetExecPath = ""
	// Rotated 2026-08-06: the previous key was found hardcoded, unencrypted, in
	// scripts/minisign_helper.go and this package's own test fixtures -- effectively
	// public since 2026-07-15. The new private half lives only in 1Password
	// (self-signed-minisign-key), password-protected, used only for local signing before
	// deploying release artifacts -- it is never committed anywhere.
	MinisignPublicKey = "RWQYNqPJry2eQ1/p1nmASik7Pka0gr7a+b2oMJ5dl/Ods9CyF/jIs+Pv"
)

// Release represents GitHub release metadata
type Release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// CheckForUpdate queries GitHub for the latest release and returns the version if a newer one exists.
func CheckForUpdate(currentVersion string) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(githubAPIBase + "/repos/peterrichards-lr/lfr-tunnel/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}

	latest := strings.TrimSpace(rel.TagName)
	current := strings.TrimSpace(currentVersion)

	if latest != "" && latest != current {
		// Simple helper to check if latest version is indeed different
		return latest, nil
	}

	return "", nil
}

// SelfUpgrade performs the update process.
func SelfUpgrade(currentVersion string, serverURL string) error {
	var downloadURL string
	var checksumsURL string
	var minisigURL string
	var latest string
	var expectedAsset string
	var useGateway bool
	var configuredInstallDir string

	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Try querying the Gateway first if serverURL is configured
	if serverURL != "" {
		fmt.Printf("[Update] Checking gateway for updates (current version: %s)...\n", currentVersion)
		gatewayURL := strings.TrimRight(serverURL, "/") + "/api/version"
		resp, err := client.Get(gatewayURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			var svrVer ServerVersionInfo
			if err := json.NewDecoder(resp.Body).Decode(&svrVer); err == nil {
				latest = strings.TrimSpace(svrVer.LatestVersion)
				if latest == currentVersion {
					fmt.Printf("[Update] You are already running the latest version (%s).\n", currentVersion)
					resp.Body.Close() //nolint:errcheck
					return nil
				}

				// Resolve target platform key on gateway
				osKey := runtime.GOOS
				if osKey == "darwin" {
					osKey = "macos"
				}
				platformKey := fmt.Sprintf("%s_%s", osKey, runtime.GOARCH)

				if platInfo, ok := svrVer.ClientPlatforms[platformKey]; ok {
					configuredInstallDir = platInfo.InstallDir
					// Check recommendations
					rec := strings.ToLower(platInfo.Recommended)
					if rec == "brew" {
						fmt.Printf("[Update] A newer version is available: %s\n", latest)
						fmt.Println("[Update] Recommended upgrade method is via Homebrew:")
						fmt.Println("[Update]   brew upgrade peterrichards-lr/homebrew-tap/lfr-tunnel")
						resp.Body.Close() //nolint:errcheck
						return nil
					} else if rec == "scoop" {
						fmt.Printf("[Update] A newer version is available: %s\n", latest)
						fmt.Println("[Update] Recommended upgrade method is via Scoop:")
						fmt.Println("[Update]   scoop update lfr-tunnel")
						resp.Body.Close() //nolint:errcheck
						return nil
					} else if rec == "cmd" && platInfo.Cmd != "" {
						fmt.Printf("[Update] A newer version is available: %s\n", latest)
						fmt.Println("[Update] Recommended upgrade method is running the installation command:")
						fmt.Printf("[Update]   %s\n", platInfo.Cmd)
						resp.Body.Close() //nolint:errcheck
						return nil
					} else if rec == "cmd_fallback" && platInfo.CmdFallback != "" {
						fmt.Printf("[Update] A newer version is available: %s\n", latest)
						fmt.Println("[Update] Recommended upgrade method is running the fallback command:")
						fmt.Printf("[Update]   %s\n", platInfo.CmdFallback)
						resp.Body.Close() //nolint:errcheck
						return nil
					}

					// Proceed with direct download URL from gateway
					if platInfo.URL != "" {
						if strings.HasPrefix(platInfo.URL, "http://") || strings.HasPrefix(platInfo.URL, "https://") {
							downloadURL = platInfo.URL
						} else {
							downloadURL = strings.TrimRight(serverURL, "/") + "/" + strings.TrimLeft(platInfo.URL, "/")
						}
						// Dynamic checksum file served from the same static directory
						checksumsURL = strings.TrimRight(serverURL, "/") + "/static/downloads/checksums.txt"
						minisigURL = checksumsURL + ".minisig"
						expectedAsset = platInfo.BinaryName
						if expectedAsset == "" {
							expectedAsset = fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
							if runtime.GOOS == "windows" {
								expectedAsset += ".exe"
							}
						}
						useGateway = true
						fmt.Printf("[Update] Gateway recommended update available: %s. Downloading from gateway...\n", latest)
					}
				}
			}
			resp.Body.Close() //nolint:errcheck
		} else {
			if resp != nil {
				resp.Body.Close() //nolint:errcheck
			}
			fmt.Printf("[Update] Warning: Gateway upgrade check failed (err: %v). Falling back to GitHub...\n", err)
		}
	}

	// 2. Fall back to GitHub Releases if no serverURL or gateway check was bypassed/failed
	if !useGateway {
		fmt.Printf("[Update] Checking GitHub Releases for updates...\n")
		resp, err := client.Get(githubAPIBase + "/repos/peterrichards-lr/lfr-tunnel/releases/latest")
		if err != nil {
			return fmt.Errorf("failed to fetch latest release from GitHub: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("github API returned status %d", resp.StatusCode)
		}

		var rel Release
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return fmt.Errorf("failed to parse release metadata: %v", err)
		}

		latest = strings.TrimSpace(rel.TagName)
		if latest == currentVersion {
			fmt.Printf("[Update] You are already running the latest version (%s).\n", currentVersion)
			return nil
		}

		fmt.Printf("[Update] New version found on GitHub: %s. Preparing update...\n", latest)

		expectedAsset = fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
		if runtime.GOOS == "windows" {
			expectedAsset += ".exe"
		}

		for _, asset := range rel.Assets {
			switch asset.Name {
			case expectedAsset:
				downloadURL = asset.DownloadURL
			case "checksums.txt":
				checksumsURL = asset.DownloadURL
			case "checksums.txt.minisig":
				minisigURL = asset.DownloadURL
			}
		}

		if downloadURL == "" {
			return fmt.Errorf("no matching pre-built binary asset found for your platform (%s)", expectedAsset)
		}

		if checksumsURL == "" {
			return fmt.Errorf("release checksums file (checksums.txt) not found in latest release assets")
		}
	}

	fmt.Println("[Update] Fetching release checksums...")
	chkResp, err := client.Get(checksumsURL)
	if err != nil {
		return fmt.Errorf("failed to download release checksums: %v", err)
	}
	defer chkResp.Body.Close() //nolint:errcheck

	if chkResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed downloading checksums: server returned status %d", chkResp.StatusCode)
	}

	checksumsContent, err := io.ReadAll(chkResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read release checksums content: %v", err)
	}

	if minisigURL == "" {
		// On the GitHub path the signature has to be a published asset. Appending
		// ".minisig" to an asset download URL cannot work -- release assets are addressed
		// by identifier, not by filename -- so the synthesised URL always 404s and the
		// user is told a status code instead of the problem (#1265).
		if !useGateway {
			return fmt.Errorf("this release publishes no checksums.txt.minisig, so the download cannot be verified.\n" +
				"       Upgrade from a gateway instead, which publishes one:\n" +
				"         lfr-tunnel --upgrade -server https://your-gateway.example.com\n" +
				"       (a configured gateway is also how deployments that must control the install location get their binaries)")
		}
		minisigURL = checksumsURL + ".minisig"
	}

	fmt.Println("[Update] Fetching release checksum signature...")
	sigResp, err := client.Get(minisigURL)
	if err != nil {
		return fmt.Errorf("failed to download release checksum signature: %v", err)
	}
	defer sigResp.Body.Close() //nolint:errcheck

	if sigResp.StatusCode != http.StatusOK {
		// A missing signature is a publishing gap, not a transport failure, and saying so
		// saves the reader from inferring it from a bare status code.
		if sigResp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("the checksum signature is missing from %s (404), so the download cannot be verified.\n"+
				"       If this is a GitHub release, it was published without a signature; try a gateway instead:\n"+
				"         lfr-tunnel --upgrade -server https://your-gateway.example.com", minisigURL)
		}
		return fmt.Errorf("failed downloading signature from %s: server returned status %d", minisigURL, sigResp.StatusCode)
	}

	sigContent, err := io.ReadAll(sigResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read release checksum signature content: %v", err)
	}

	// Verify the signature of the checksums file using the embedded public key
	pubKey, err := minisign.NewPublicKey(MinisignPublicKey)
	if err != nil {
		return fmt.Errorf("failed to parse embedded public key: %v", err)
	}

	sig, err := minisign.DecodeSignature(string(sigContent))
	if err != nil {
		return fmt.Errorf("failed to decode signature: %v", err)
	}

	valid, err := pubKey.Verify(checksumsContent, sig)
	if err != nil {
		if strings.Contains(err.Error(), "Incompatible key identifiers") {
			// This specific error means the signature was made with a different minisign
			// key than the one this binary trusts -- i.e. this install predates a signing-key
			// rotation (see #949/#955). That's not a corrupted download or an attack; it's the
			// intended, permanent consequence of rotating away from a compromised key: a
			// pre-rotation binary can never verify a post-rotation signature, by design.
			// Self-upgrade cannot bridge that gap on its own -- a fresh manual install is
			// required, exactly once, to pick up the new trusted key.
			reinstallHint := "the project's GitHub Releases page or your gateway's download page"
			if serverURL != "" {
				reinstallHint = strings.TrimRight(serverURL, "/") + "/install"
			}
			return fmt.Errorf("this installation predates a signing-key rotation and can no longer verify new releases automatically -- download and reinstall manually, once, from %s (see https://github.com/peterrichards-lr/lfr-tunnel/issues/955 for background)", reinstallHint)
		}
		return fmt.Errorf("signature verification failed: %v", err)
	}
	if !valid {
		return fmt.Errorf("signature verification failed: signature is invalid")
	}

	fmt.Println("[Update] Checksums file signature verified successfully.")

	// Resolve running executable path
	execPath := targetExecPath
	if execPath == "" {
		execPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find current executable path: %v", err)
		}
	}

	// Get real path (resolves symlinks)
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks for executable: %v", err)
	}

	var tempPath string
	switch runtime.GOOS {
	case "windows":
		tempPath = filepath.Join(os.TempDir(), "lfr-tunnel-update-tmp.exe")
	case "darwin":
		// EDR (SentinelOne) whitelist requires specific exact path on macOS
		tempPath = "/private/tmp/lfr-tunnel"
	default:
		tempPath = "/tmp/lfr-tunnel"
	}
	_ = os.Remove(tempPath) // Clean up any stale file //nolint:errcheck

	fmt.Println("[Update] Downloading latest binary...")
	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download new binary: %v", err)
	}
	defer downloadResp.Body.Close() //nolint:errcheck

	if downloadResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed downloading binary: server returned %d", downloadResp.StatusCode)
	}

	// Write to temporary file
	// 0755 required (#1408): this becomes the client binary, so it must be executable.
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create temporary file (is directory writeable?): %v", err)
	}
	defer func() {
		tempFile.Close()        //nolint:errcheck
		_ = os.Remove(tempPath) // Clean up temp file if not swapped //nolint:errcheck
	}()

	if _, err := io.Copy(tempFile, downloadResp.Body); err != nil {
		return fmt.Errorf("failed to write binary content: %v", err)
	}
	tempFile.Close() //nolint:errcheck

	// Verify SHA256 integrity of the downloaded binary
	computedHash, err := computeSHA256(tempPath)
	if err != nil {
		return fmt.Errorf("failed to compute downloaded binary checksum: %v", err)
	}

	expectedHash := ""
	lines := strings.Split(string(checksumsContent), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == expectedAsset {
			expectedHash = strings.ToLower(parts[0])
			break
		}
	}

	if expectedHash == "" {
		return fmt.Errorf("binary integrity check failed: asset %q not found in release checksums", expectedAsset)
	}

	if computedHash != expectedHash {
		return fmt.Errorf("binary integrity check failed: checksum mismatch (expected: %s, got: %s)", expectedHash, computedHash)
	}

	fmt.Println("[Update] Binary integrity verified successfully.")

	// Pre-upgrade: stop all active processes and services running the binary to release file handles and prevent EDR quarantine
	plistToReload, restartSystemd, relaunch := stopActiveProcessesAndServices()

	migrated := false
	// Check for environment variable install directory overrides if targetExecPath was not explicitly specified by caller/test
	if targetExecPath == "" {
		envInstallDir := os.Getenv("LFR_TUNNEL_MACOS_ARM64_INSTALL_DIR")
		if envInstallDir == "" {
			envInstallDir = os.Getenv("LFR_TUNNEL_MACOS_AMD64_INSTALL_DIR")
		}
		if envInstallDir == "" {
			envInstallDir = os.Getenv("LFR_TUNNEL_LINUX_AMD64_INSTALL_DIR")
		}
		if envInstallDir == "" {
			envInstallDir = os.Getenv("LFR_TUNNEL_WINDOWS_AMD64_INSTALL_DIR")
		}
		if envInstallDir == "" {
			envInstallDir = os.Getenv("LFR_TUNNEL_INSTALL_DIR")
		}
		if envInstallDir == "" {
			envInstallDir = os.Getenv("LFT_INSTALL_DIR")
		}
		if envInstallDir != "" {
			configuredInstallDir = envInstallDir
		}
	}

	if configuredInstallDir != "" {
		configuredInstallDir = os.ExpandEnv(configuredInstallDir)
		if strings.HasPrefix(configuredInstallDir, "~/") || strings.HasPrefix(configuredInstallDir, "~\\") {
			home, err := os.UserHomeDir()
			if err == nil {
				configuredInstallDir = filepath.Join(home, configuredInstallDir[2:])
			}
		}
		expectedTarget := filepath.Join(configuredInstallDir, "lfr-tunnel")
		if runtime.GOOS == "windows" {
			expectedTarget += ".exe"
		}

		if execPath != expectedTarget {
			fmt.Printf("[Update] Binary is currently running from %s, but server policy requires %s.\n", execPath, expectedTarget)
			fmt.Println("[Update] Migrating binary to the new directory...")

			// Ensure target directory exists
			// 0750 (#1408): the user's own install directory; they are the only one who
			// needs to run what lands in it.
			if err := os.MkdirAll(configuredInstallDir, 0o750); err != nil {
				return fmt.Errorf("failed to create target directory %s: %v (try running with sudo/Administrator)", configuredInstallDir, err)
			}

			// Move temp binary to new location
			if err := replaceBinary(tempPath, expectedTarget); err != nil {
				return fmt.Errorf("failed to move binary to %s: %v (try running with sudo/Administrator)", expectedTarget, err)
			}

			fmt.Println("[Update] Binary migrated successfully. Cleaning up old binary...")
			_ = os.Remove(execPath)   //nolint:errcheck
			execPath = expectedTarget // Update execPath for restartActiveProcessesAndServices
			migrated = true

			// We need to re-register services pointing to the new path
			fmt.Println("[Update] Re-registering background services to point to the new location...")
			_ = exec.Command(execPath, "install-service").Run() //nolint:errcheck

			// install-service does not merely rewrite the unit file: installLinux enables AND
			// starts the systemd unit, and installDarwin loads the plist. So by this line a
			// client is already coming up, whether or not one was running before the upgrade --
			// and the relaunch planner has to be told, or it starts a second one beside it
			// (#2194). On macOS the plists below say so; on Linux nothing did, because
			// restartSystemd was decided from whether the unit was active BEFORE the upgrade.
			if runtime.GOOS == "linux" {
				restartSystemd = true
			}
			if runtime.GOOS == "darwin" {
				_ = exec.Command(execPath, "install-gui-service").Run() //nolint:errcheck
				// The install commands above generate new plists, so we must reload those instead of the old ones
				home, _ := os.UserHomeDir()
				plistToReload = []string{
					filepath.Join(home, "Library", "LaunchAgents", daemonPlistName),
					filepath.Join(home, "Library", "LaunchAgents", guiPlistName),
				}
			}
		}
	}

	var swapErr error
	if !migrated {
		// Perform replacement swap
		if runtime.GOOS == "windows" {
			oldPath := execPath + ".old"
			_ = os.Remove(oldPath) // Remove any previous leftovers //nolint:errcheck
			if err := os.Rename(execPath, oldPath); err != nil {
				swapErr = fmt.Errorf("failed to rename running binary: %v (please make sure you have permissions)", err)
			} else if err := replaceBinary(tempPath, execPath); err != nil {
				// Try to rollback rename
				_ = os.Rename(oldPath, execPath) //nolint:errcheck
				swapErr = fmt.Errorf("failed to replace downloaded binary: %v", err)
			} else {
				fmt.Println("[Update] Upgrade successful! You are now running the latest version.")
				fmt.Printf("[Update] Note: You can delete the backup file: %s\n", oldPath)
			}
		} else {
			// On Unix we can replace directly
			if err := replaceBinary(tempPath, execPath); err != nil {
				swapErr = fmt.Errorf("failed to replace running binary: %v (if permission is denied, try running as sudo)", err)
			} else {
				fmt.Println("[Update] Upgrade successful! You are now running the latest version.")
			}
		}
	}

	// Post-upgrade: restart previously active processes and services
	restartActiveProcessesAndServices(plistToReload, restartSystemd, relaunch)

	if swapErr != nil {
		return swapErr
	}

	return nil
}

func replaceBinary(tempPath, execPath string) error {
	// First attempt simple rename (fastest, atomic if same filesystem)
	err := os.Rename(tempPath, execPath)
	if err == nil {
		return nil
	}

	// If it fails (e.g. EXDEV cross-device link), fallback to copying
	// Remove the target first to prevent ETXTBSY if any processes still hold open file handles
	_ = os.Remove(execPath) //nolint:errcheck

	in, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	// 0755 required (#1408): same -- the replacement binary has to be executable.
	out, err := os.OpenFile(execPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Sync()
}

// appendRelaunchTarget records how a process was started, so the upgrade can start it again.
//
// Called immediately BEFORE the process is killed, because that is the only moment the
// information exists: once it is gone the kernel has nothing left to ask, and it is
// deliberately never persisted. argv carries -passcode, -basic-auth and -token values, so a
// file holding it would put credentials at rest -- the leak closed in #2137, and the reason
// #2148 records flag NAMES only. Held in memory for the seconds between stopping and starting,
// and it is already visible to this user through `ps` anyway.
//
// A process whose command line cannot be read is NOT relaunched, and says so. Guessing at the
// arguments would start a tunnel configured differently from the one the user had -- a
// different subdomain, or no passcode -- which is worse than leaving it down, because it looks
// like it worked.
func appendRelaunchTarget(relaunch [][]string, pid int, kind string) [][]string {
	argv, err := processArgv(pid)
	if err != nil {
		fmt.Printf("[Update] Could not read how the %s process (PID: %d) was started, so it "+
			"will not be restarted automatically: %v\n", kind, pid, err)
		return relaunch
	}
	return append(relaunch, argv)
}

// The service definitions this upgrade reloads, named here so the planner and the stop path
// agree with each other. The templates that create them are in service_installer.go; the flags
// each one runs are modelled in serviceStartArgv and held to the templates by
// TestServiceStartArgvMatchesWhatTheInstallerWrites.
const (
	daemonPlistName = "com.liferay.tunnel.plist"
	guiPlistName    = "com.liferay.tunnel.gui.plist"
	systemdUnitName = "lfr-tunnel.service"
)

// serviceStartArgv returns the command lines the service managers this upgrade reloads will run
// by themselves, once it has reloaded them.
//
// This is the half of the picture planRelaunch never had. The upgrade restarts a client through
// two independent mechanisms -- the service manager it reloads, and the argv it captured from
// the process it killed -- and until now only the captured half was planned. A machine with the
// LaunchAgent or the systemd unit installed therefore got both: one client from the reloaded
// service and one from the relaunch, which is #2194.
//
// argv[0] is this binary because that is what the templates write, but nothing here compares it:
// the plist records a canonical path that need not match os.Executable(), so every comparison in
// the planner is on flags alone, exactly as the #2189 one already was.
func serviceStartArgv(exe string, plistToReload []string, restartSystemd bool) [][]string {
	var out [][]string
	for _, plist := range plistToReload {
		switch filepath.Base(plist) {
		case daemonPlistName:
			out = append(out, []string{exe, "-" + backgroundFlagName})
		case guiPlistName:
			out = append(out, []string{exe, "-" + guiFlagName})
		}
	}
	if restartSystemd {
		out = append(out, []string{exe, "-" + backgroundFlagName})
	}
	return out
}

// startedTunnel returns the configuration of the tunnel a starter brings up by itself.
//
// A "starter" is anything that ends up with a client running: a captured background tunnel, a
// tray (which spawns one on startup unless told not to -- #2076), or a service definition. The
// configuration is the flag vector the running client carries, which is what decides the
// subdomain, the region and the Inspector port -- so two starters with the same one are two
// clients racing for one lease. That is the thing this upgrade must not create.
func startedTunnel(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	if IsTrayArgv(argv) {
		if !TrayConnectsOnStart(argv[1:]) {
			return nil, false
		}
		return TrayTunnelArgs(argv[1:]), true
	}
	return withoutBackgroundFlag(argv[1:]), true
}

// planRelaunch turns the processes this upgrade killed into the command lines it should run, and
// is where every "do not start a second one" rule lives.
//
// It does two things.
//
// **It drops what something else will start.** The upgrade restarts every process it found,
// which is right for every process EXCEPT one that something else it restarted also starts.
// There are three such starters, and until #2194 the planner knew about one:
//
//   - a tray, which spawns a client on startup (#2076, fixed for the tray alone in #2189)
//   - the CLI service -- launchd's com.liferay.tunnel.plist or the systemd user unit -- which
//     runs `lfr-tunnel -background`
//   - the GUI LaunchAgent, which runs `lfr-tunnel -gui`, i.e. another tray
//
// The rule is one rule over all three rather than a special case each: a captured entry is
// dropped when something else already produces the same process, or the same tunnel.
//
// **It marks a tunnel as one to start through -background.** The captured argv is the CHILD's,
// which has already had -background stripped, so running it verbatim starts a client that never
// passes through handleBackground -- the only place in the tree that writes a pid file. Such a
// client is invisible to -stop, to -status and to every later -upgrade, so it survives them all
// and accumulates (#2194). Restoring the flag hands it back to the one writer rather than
// introducing a second one, which is the mistake #2128 was.
//
// Deliberately NOT "a GUI or a service came back, so skip the tunnel". A tray started with
// -no-autoconnect is a control surface that starts nothing, and a service's client is configured
// differently from a tunnel the user started by hand. Skipping either would leave the user with
// no tunnel at all, which is #2164 -- so every uncertainty still resolves towards restarting,
// and the worst case remains one surplus client rather than none.
func planRelaunch(relaunch [][]string, serviceStarts [][]string) [][]string {
	// What will be running after this upgrade without it starting anything itself.
	startedElsewhere := map[string]bool{} // by the flags of the process
	tunnelsElsewhere := map[string]bool{} // by the flags of the tunnel that results

	note := func(argv []string) {
		if len(argv) == 0 {
			return
		}
		startedElsewhere[flagKey(argv[1:])] = true
		if tunnel, ok := startedTunnel(argv); ok {
			tunnelsElsewhere[flagKey(tunnel)] = true
		}
	}
	for _, argv := range serviceStarts {
		note(argv)
	}
	// A captured tray is a starter too, and this is the #2189 rule: it covers the tunnel it will
	// spawn, but not itself -- otherwise it would suppress its own relaunch and the tray would
	// never come back.
	for _, argv := range relaunch {
		if IsTrayArgv(argv) && !startedElsewhere[flagKey(argv[1:])] {
			if tunnel, ok := startedTunnel(argv); ok {
				tunnelsElsewhere[flagKey(tunnel)] = true
			}
		}
	}

	out := make([][]string, 0, len(relaunch))
	for _, argv := range relaunch {
		if len(argv) > 0 {
			if startedElsewhere[flagKey(argv[1:])] {
				fmt.Printf("[Update] Not restarting %s separately: the service definition this "+
					"upgrade reloaded runs it (%s).\n",
					filepath.Base(argv[0]), describeFlags(argv[1:]))
				continue
			}
			// Trays are excluded deliberately, not incidentally: the loop above recorded a
			// captured tray's own tunnel as covered, so without this a tray would suppress its
			// own relaunch and never come back -- #2164, by way of the fix for #2189.
			//
			// Every match, not just the first: if the machine was already in the doubled state
			// #2189 and #2194 describe, one relaunch for each of them is still one too many.
			if tunnel, ok := startedTunnel(argv); ok && !IsTrayArgv(argv) && tunnelsElsewhere[flagKey(tunnel)] {
				fmt.Printf("[Update] Not restarting the background tunnel separately: something "+
					"else this upgrade restarted starts it (%s).\n", describeFlags(tunnel))
				continue
			}
		}
		out = append(out, relaunchArgv(argv))
	}
	return out
}

// describeFlags names a flag vector for a human reading the upgrade's output. The service's own
// client is configured entirely by the config file and carries none, and "()" on the end of a
// sentence reads like a truncation rather than an answer.
func describeFlags(flags []string) string {
	if len(flags) == 0 {
		return "no flags"
	}
	return strings.Join(flags, " ")
}

// flagKey identifies a command line by its flags alone, never by argv[0].
//
// The paths legitimately differ for the same program: a plist records the canonical executable
// path, os.Executable() resolves symlinks, and a user's own shell may have used either. Keying
// on the flags is what the #2189 comparison already did, and widening the key to include the
// path would silently stop matching the service case this is here for.
func flagKey(flags []string) string {
	return strings.Join(flags, "\x00")
}

// relaunchArgv is the command line the upgrade actually runs for a planned entry.
//
// A tray is started exactly as it was found. A tunnel is started through -background, so that
// handleBackground writes its pid file and the next -stop or -upgrade can see it (#2194).
func relaunchArgv(argv []string) []string {
	if len(argv) == 0 || IsTrayArgv(argv) {
		return argv
	}
	for _, a := range argv[1:] {
		if isFlagToken(a, backgroundFlagName) {
			return argv
		}
	}
	return append(slices.Clone(argv), "-"+backgroundFlagName)
}

func stopActiveProcessesAndServices() ([]string, bool, [][]string) {
	var plistToReload []string
	var restartSystemd bool
	var relaunch [][]string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false, nil
	}

	// 1. Unload LaunchAgents on macOS
	if runtime.GOOS == "darwin" {
		guiPlist := filepath.Join(home, "Library", "LaunchAgents", guiPlistName)
		if _, err := os.Stat(guiPlist); err == nil {
			fmt.Println("[Update] Unloading macOS GUI LaunchAgent...")
			_ = exec.Command("launchctl", "unload", guiPlist).Run() //nolint:errcheck
			plistToReload = append(plistToReload, guiPlist)
		}
		daemonPlist := filepath.Join(home, "Library", "LaunchAgents", daemonPlistName)
		if _, err := os.Stat(daemonPlist); err == nil {
			fmt.Println("[Update] Unloading macOS CLI Daemon LaunchAgent...")
			_ = exec.Command("launchctl", "unload", daemonPlist).Run() //nolint:errcheck
			plistToReload = append(plistToReload, daemonPlist)
		}
		if len(plistToReload) > 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 2. Stop systemd services on Linux
	if runtime.GOOS == "linux" {
		cmd := exec.Command("systemctl", "--user", "is-active", systemdUnitName)
		if err := cmd.Run(); err == nil {
			fmt.Println("[Update] Stopping Linux systemd user service...")
			_ = exec.Command("systemctl", "--user", "stop", systemdUnitName).Run() //nolint:errcheck
			restartSystemd = true
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 3. Terminate active GUI PID
	guiLock, guiLockErr := GUIPIDPath()
	if guiLockErr != nil {
		fmt.Printf("[Update] Could not resolve the GUI lock file, skipping that step: %v\n", guiLockErr)
	}
	if data, err := os.ReadFile(guiLock); guiLockErr == nil && err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if IsPIDRunning(pid) {
				// Read how it was started BEFORE killing it. Once the process is gone the
				// kernel has nothing left to ask, and this is the only moment the information
				// exists anywhere -- it is deliberately never written to disk (#2164).
				relaunch = appendRelaunchTarget(relaunch, pid, "GUI")
				fmt.Printf("[Update] Terminating active GUI process (PID: %d)...\n", pid)
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()        //nolint:errcheck
					_ = os.Remove(guiLock) //nolint:errcheck
				}
			}
		}
	}

	// 4. Terminate active background CLI PIDs
	logDir := filepath.Join(home, ".lfr-tunnel")
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, entry := range entries {
			// Was `strings.HasPrefix(entry.Name(), "client-")`, which is the LOG file's
			// prefix. Tunnel pid files are named lfr-tunnel-<sub>.pid, so this loop matched
			// nothing and terminated no background tunnel, ever (#2128). Both sides of the
			// name now come from pidfiles.go so they cannot disagree again.
			if _, isTunnelPID := TunnelPIDFileSubdomain(entry.Name()); !entry.IsDir() && isTunnelPID {
				pidPath := filepath.Join(logDir, entry.Name())
				if data, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
						if IsPIDRunning(pid) {
							relaunch = appendRelaunchTarget(relaunch, pid, "background tunnel")
							fmt.Printf("[Update] Terminating active background tunnel process (PID: %d)...\n", pid)
							if proc, err := os.FindProcess(pid); err == nil {
								_ = proc.Kill()        //nolint:errcheck
								_ = os.Remove(pidPath) //nolint:errcheck
							}
						}
					}
				}
			}
		}
	}

	// Wait briefly for all processes to fully die and release binary file handles
	time.Sleep(500 * time.Millisecond)

	return plistToReload, restartSystemd, relaunch
}

func restartActiveProcessesAndServices(plistToReload []string, restartSystemd bool, relaunch [][]string) {
	// 1. Reload LaunchAgents on macOS
	if runtime.GOOS == "darwin" {
		for _, plist := range plistToReload {
			fmt.Printf("[Update] Restarting macOS LaunchAgent: %s...\n", filepath.Base(plist))
			_ = exec.Command("launchctl", "load", "-w", plist).Run() //nolint:errcheck
		}
	}

	// 2. Restart systemd services on Linux
	if runtime.GOOS == "linux" && restartSystemd {
		fmt.Println("[Update] Restarting Linux systemd user service...")
		_ = exec.Command("systemctl", "--user", "start", systemdUnitName).Run() //nolint:errcheck
	}

	// 3. Restart the processes this upgrade killed directly (#2164), minus any that something
	// else it just restarted will start by itself -- a tray (#2189), or one of the service
	// definitions reloaded in steps 1 and 2 (#2194).
	//
	// stopActiveProcessesAndServices stops four kinds of thing -- launchd plists, the systemd
	// service, the GUI process and background tunnels -- and until now restarted only the first
	// two. So whether a client survived an upgrade depended on how it had been started: a
	// service came back, a `-background` or `-gui` client was killed and left down, with the
	// upgrade reporting success either way.
	//
	// The services are passed in rather than re-derived, because steps 1 and 2 above are the
	// only place that knows which of them this upgrade actually stopped -- an installed unit it
	// found inactive is not one that is about to start a client.
	//
	// Launched detached, exactly as handleBackground does; planRelaunch restores the -background
	// flag on a tunnel so that the process that survives is one the pid-file writer has seen.
	exe, exeErr := os.Executable()
	if exeErr != nil {
		// Only argv[0] of the modelled service command lines, which nothing compares. A blank
		// one costs the planner nothing and is better than abandoning the plan.
		exe = ""
	}
	serviceStarts := serviceStartArgv(exe, plistToReload, restartSystemd)

	for _, argv := range planRelaunch(relaunch, serviceStarts) {
		if len(argv) == 0 {
			continue
		}
		fmt.Printf("[Update] Restarting %s...\n", filepath.Base(argv[0]))
		cmd := osutil.BackgroundCommand(argv[0], argv[1:]...)
		cmd.Dir = "."
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Update] Failed to restart %s: %v\n", filepath.Base(argv[0]), err)
		}
	}
}

func computeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

type ServerPlatformInfo struct {
	URL              string `json:"url"`
	BinaryName       string `json:"binary_name"`
	SHA256           string `json:"sha256"`
	Cmd              string `json:"cmd"`
	CmdLabel         string `json:"cmd_label"`
	CmdFallback      string `json:"cmd_fallback"`
	CmdFallbackLabel string `json:"cmd_fallback_label"`
	Recommended      string `json:"recommended"`
	InstallDir       string `json:"install_dir"`
}

type ServerVersionInfo struct {
	LatestVersion   string `json:"latest_version"`
	MinVersion      string `json:"min_version"`
	MaintenanceMode string `json:"maintenance_mode"`

	ClientPlatforms map[string]ServerPlatformInfo `json:"client_platforms"`

	// MinVersionServerEnforced is set by a gateway that refuses a too-old client itself
	// (#1988), rather than relying on the client to refuse itself.
	//
	// It is what lets the client stop hard-failing on its own pre-flight check: when the
	// gateway enforces the floor, it also applies a per-user grace window, and a client that
	// killed itself first would make that window unreachable for every client new enough to
	// look. Absent -- an older gateway -- the client keeps its own hard stop, so enforcement
	// is never weaker than it was before this field existed.
	MinVersionServerEnforced bool `json:"min_version_server_enforced,omitempty"`

	// ClientSettings is the declarative block a gateway advertises so tuning reaches clients
	// without a release (#1948). Absent from an older gateway, which reads as "no opinion"
	// about everything in it. Every value in it is clamped client-side -- see
	// pkg/client/settings.go.
	ClientSettings *ClientSettings `json:"client_settings,omitempty"`

	// ClientReconnectSeconds is how long this gateway would like clients to keep trying to
	// reattach to it before falling back to region failover (#1946). Absent or zero means the
	// gateway has no opinion and the client's own default applies. Advisory in both
	// directions: the client clamps it, and a client too old to read it simply does not.
	ClientReconnectSeconds int `json:"client_reconnect_seconds,omitempty"`
}

func CheckServerCompatibility(serverURL string) (*ServerVersionInfo, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(serverURL, "/") + "/api/version")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var info ServerVersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// CompareVersions returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
//
// The implementation moved to pkg/config (#1988) so the gateway can order versions with the
// exact same rules the client does; this stays as the name every existing caller uses.
func CompareVersions(v1, v2 string) int {
	return config.CompareVersions(v1, v2)
}
