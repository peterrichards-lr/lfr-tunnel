package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	githubAPIBase  = "https://api.github.com"
	targetExecPath = ""
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
	defer resp.Body.Close()

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
func SelfUpgrade(currentVersion string) error {
	fmt.Printf("[Update] Checking for updates (current version: %s)...\n", currentVersion)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(githubAPIBase + "/repos/peterrichards-lr/lfr-tunnel/releases/latest")
	if err != nil {
		return fmt.Errorf("failed to fetch latest release: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return fmt.Errorf("failed to parse release metadata: %v", err)
	}

	latest := strings.TrimSpace(rel.TagName)
	if latest == currentVersion {
		fmt.Printf("[Update] You are already running the latest version (%s).\n", currentVersion)
		return nil
	}

	fmt.Printf("[Update] New version found: %s. Preparing update...\n", latest)

	// Determine matching asset name based on OS and architecture
	expectedAsset := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		expectedAsset += ".exe"
	}

	var downloadURL string
	var checksumsURL string
	for _, asset := range rel.Assets {
		if asset.Name == expectedAsset {
			downloadURL = asset.DownloadURL
		} else if asset.Name == "checksums.txt" {
			checksumsURL = asset.DownloadURL
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no matching pre-built binary asset found for your platform (%s)", expectedAsset)
	}

	if checksumsURL == "" {
		return fmt.Errorf("release checksums file (checksums.txt) not found in latest release assets")
	}

	fmt.Println("[Update] Fetching release checksums...")
	chkResp, err := client.Get(checksumsURL)
	if err != nil {
		return fmt.Errorf("failed to download release checksums: %v", err)
	}
	defer chkResp.Body.Close()

	if chkResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed downloading checksums: server returned status %d", chkResp.StatusCode)
	}

	checksumsContent, err := io.ReadAll(chkResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read release checksums content: %v", err)
	}
	chkResp.Body.Close()

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

	binDir := filepath.Dir(execPath)
	tempPath := filepath.Join(binDir, "lfr-tunnel-update-tmp")
	if runtime.GOOS == "windows" {
		tempPath += ".exe"
	}

	fmt.Println("[Update] Downloading latest binary...")
	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download new binary: %v", err)
	}
	defer downloadResp.Body.Close()

	if downloadResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed downloading binary: server returned %d", downloadResp.StatusCode)
	}

	// Write to temporary file
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temporary file (is directory writeable?): %v", err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempPath) // Clean up temp file if not swapped
	}()

	if _, err := io.Copy(tempFile, downloadResp.Body); err != nil {
		return fmt.Errorf("failed to write binary content: %v", err)
	}
	tempFile.Close()

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

	// Perform replacement swap
	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath) // Remove any previous leftovers
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to rename running binary: %v. Please make sure you have permissions.", err)
		}
		if err := os.Rename(tempPath, execPath); err != nil {
			// Try to rollback rename
			_ = os.Rename(oldPath, execPath)
			return fmt.Errorf("failed to rename downloaded binary: %v", err)
		}
		fmt.Println("[Update] Upgrade successful! You are now running the latest version.")
		fmt.Printf("[Update] Note: You can delete the backup file: %s\n", oldPath)
	} else {
		// On Unix we can rename directly
		if err := os.Rename(tempPath, execPath); err != nil {
			return fmt.Errorf("failed to replace running binary: %v. If permission is denied, try running as sudo.", err)
		}
		fmt.Println("[Update] Upgrade successful! You are now running the latest version.")
	}

	return nil
}

func computeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

type ServerVersionInfo struct {
	LatestVersion string `json:"latest_version"`
	MinVersion    string `json:"min_version"`
}

func CheckServerCompatibility(serverURL string) (*ServerVersionInfo, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(serverURL, "/") + "/api/version")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
			fmt.Sscanf(p1[i], "%d", &n1)
		}
		if i < len(p2) {
			fmt.Sscanf(p2[i], "%d", &n2)
		}
		if n1 < n2 {
			return -1
		} else if n1 > n2 {
			return 1
		}
	}
	return 0
}
