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
	"strconv"
	"strings"
	"time"

	"github.com/jedisct1/go-minisign"
)

var (
	githubAPIBase     = "https://api.github.com"
	targetExecPath    = ""
	MinisignPublicKey = "RWQ4i1aka4ZBsR0gESesJ6Ay57fGFJ9T1ajVmanT7MFMCCDbPZ8uqDcS"
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
		minisigURL = checksumsURL + ".minisig"
	}

	fmt.Println("[Update] Fetching release checksum signature...")
	sigResp, err := client.Get(minisigURL)
	if err != nil {
		return fmt.Errorf("failed to download release checksum signature: %v", err)
	}
	defer sigResp.Body.Close() //nolint:errcheck

	if sigResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed downloading signature: server returned status %d", sigResp.StatusCode)
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
	if runtime.GOOS == "windows" {
		tempPath = filepath.Join(os.TempDir(), "lfr-tunnel-update-tmp.exe")
	} else if runtime.GOOS == "darwin" {
		// EDR (SentinelOne) whitelist requires specific exact path on macOS
		tempPath = "/private/tmp/lfr-tunnel"
	} else {
		tempPath = "/tmp/lfr-tunnel"
	}
	_ = os.Remove(tempPath) // Clean up any stale file

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
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temporary file (is directory writeable?): %v", err)
	}
	defer func() {
		tempFile.Close()        //nolint:errcheck
		_ = os.Remove(tempPath) // Clean up temp file if not swapped
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
	plistToReload, restartSystemd := stopActiveProcessesAndServices()

	// Perform replacement swap
	var swapErr error
	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath) // Remove any previous leftovers
		if err := os.Rename(execPath, oldPath); err != nil {
			swapErr = fmt.Errorf("failed to rename running binary: %v (please make sure you have permissions)", err)
		} else if err := replaceBinary(tempPath, execPath); err != nil {
			// Try to rollback rename
			_ = os.Rename(oldPath, execPath)
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

	// Post-upgrade: restart previously active processes and services
	restartActiveProcessesAndServices(plistToReload, restartSystemd)

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
	_ = os.Remove(execPath)

	in, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	out, err := os.OpenFile(execPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
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

func stopActiveProcessesAndServices() ([]string, bool) {
	var plistToReload []string
	var restartSystemd bool
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}

	// 1. Unload LaunchAgents on macOS
	if runtime.GOOS == "darwin" {
		guiPlist := filepath.Join(home, "Library", "LaunchAgents", "com.liferay.tunnel.gui.plist")
		if _, err := os.Stat(guiPlist); err == nil {
			fmt.Println("[Update] Unloading macOS GUI LaunchAgent...")
			_ = exec.Command("launchctl", "unload", guiPlist).Run()
			plistToReload = append(plistToReload, guiPlist)
		}
		daemonPlist := filepath.Join(home, "Library", "LaunchAgents", "com.liferay.tunnel.plist")
		if _, err := os.Stat(daemonPlist); err == nil {
			fmt.Println("[Update] Unloading macOS CLI Daemon LaunchAgent...")
			_ = exec.Command("launchctl", "unload", daemonPlist).Run()
			plistToReload = append(plistToReload, daemonPlist)
		}
		if len(plistToReload) > 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 2. Stop systemd services on Linux
	if runtime.GOOS == "linux" {
		cmd := exec.Command("systemctl", "--user", "is-active", "lfr-tunnel.service")
		if err := cmd.Run(); err == nil {
			fmt.Println("[Update] Stopping Linux systemd user service...")
			_ = exec.Command("systemctl", "--user", "stop", "lfr-tunnel.service").Run()
			restartSystemd = true
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 3. Terminate active GUI PID
	guiLock := filepath.Join(home, ".lfr-tunnel", "gui.pid")
	if data, err := os.ReadFile(guiLock); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if IsPIDRunning(pid) {
				fmt.Printf("[Update] Terminating active GUI process (PID: %d)...\n", pid)
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()
					_ = os.Remove(guiLock)
				}
			}
		}
	}

	// 4. Terminate active background CLI PIDs
	logDir := filepath.Join(home, ".lfr-tunnel")
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "client-") && strings.HasSuffix(entry.Name(), ".pid") {
				pidPath := filepath.Join(logDir, entry.Name())
				if data, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
						if IsPIDRunning(pid) {
							fmt.Printf("[Update] Terminating active background tunnel process (PID: %d)...\n", pid)
							if proc, err := os.FindProcess(pid); err == nil {
								_ = proc.Kill()
								_ = os.Remove(pidPath)
							}
						}
					}
				}
			}
		}
	}

	// Wait briefly for all processes to fully die and release binary file handles
	time.Sleep(500 * time.Millisecond)

	return plistToReload, restartSystemd
}

func restartActiveProcessesAndServices(plistToReload []string, restartSystemd bool) {
	// 1. Reload LaunchAgents on macOS
	if runtime.GOOS == "darwin" {
		for _, plist := range plistToReload {
			fmt.Printf("[Update] Restarting macOS LaunchAgent: %s...\n", filepath.Base(plist))
			_ = exec.Command("launchctl", "load", "-w", plist).Run()
		}
	}

	// 2. Restart systemd services on Linux
	if runtime.GOOS == "linux" && restartSystemd {
		fmt.Println("[Update] Restarting Linux systemd user service...")
		_ = exec.Command("systemctl", "--user", "start", "lfr-tunnel.service").Run()
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
}

type ServerVersionInfo struct {
	LatestVersion   string                        `json:"latest_version"`
	MinVersion      string                        `json:"min_version"`
	ClientPlatforms map[string]ServerPlatformInfo `json:"client_platforms"`
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
func CompareVersions(v1, v2 string) int {
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")

	for i := 0; i < len(p1) || i < len(p2); i++ {
		var n1, n2 int
		if i < len(p1) {
			fmt.Sscanf(p1[i], "%d", &n1) //nolint:errcheck
		}
		if i < len(p2) {
			fmt.Sscanf(p2[i], "%d", &n2) //nolint:errcheck
		}
		if n1 < n2 {
			return -1
		} else if n1 > n2 {
			return 1
		}
	}
	return 0
}
