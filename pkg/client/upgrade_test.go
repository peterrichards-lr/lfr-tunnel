package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jedisct1/go-minisign"
)

const testSK = `untrusted comment: minisign encrypted secret key
RWQAAEIyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAOItWpGuGQbG4C9WXaxEYLgZ2xxuqfbuZmDgAhQ8Unot8t7SyxZ0nVh0gESesJ6Ay57fGFJ9T1ajVmanT7MFMCCDbPZ8uqDcSAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := Release{
			TagName: "v1.0.3",
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	latest, err := CheckForUpdate("v1.0.2")
	if err != nil {
		t.Fatalf("unexpected error check for update: %v", err)
	}

	if latest != "v1.0.3" {
		t.Errorf("expected version v1.0.3, got %s", latest)
	}
}

func TestCheckForUpdate_SameVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := Release{
			TagName: "v1.0.2",
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	latest, err := CheckForUpdate("v1.0.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if latest != "" {
		t.Errorf("expected empty string (no update), got %s", latest)
	}
}

func TestSelfUpgrade(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	execFilename := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		execFilename += ".exe"
	}
	fakeExecPath := filepath.Join(tmpDir, execFilename)

	if err := os.WriteFile(fakeExecPath, []byte("fake-binary-old-content"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/peterrichards-lr/lfr-tunnel/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			rel := Release{
				TagName: "v1.0.3",
				Assets: []struct {
					Name        string `json:"name"`
					DownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:        assetName,
						DownloadURL: fmt.Sprintf("http://%s/download/asset", r.Host),
					},
					{
						Name:        "checksums.txt",
						DownloadURL: fmt.Sprintf("http://%s/download/checksums", r.Host),
					},
					{
						Name:        "checksums.txt.minisig",
						DownloadURL: fmt.Sprintf("http://%s/download/signature", r.Host),
					},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}

		if r.URL.Path == "/download/checksums" {
			w.WriteHeader(http.StatusOK)
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			h := sha256.Sum256([]byte("fake-binary-new-content"))
			hexHash := hex.EncodeToString(h[:])
			_, _ = fmt.Fprintf(w, "%s  %s\n", hexHash, assetName)
			return
		}

		if r.URL.Path == "/download/signature" {
			w.WriteHeader(http.StatusOK)
			sk, err := minisign.DecodePrivateKey(testSK)
			if err != nil {
				t.Fatalf("failed to decode private key: %v", err)
			}
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			h := sha256.Sum256([]byte("fake-binary-new-content"))
			hexHash := hex.EncodeToString(h[:])
			checksumsText := fmt.Sprintf("%s  %s\n", hexHash, assetName)

			sig, err := sk.Sign([]byte(checksumsText), minisign.SignOptions{Hashed: true})
			if err != nil {
				t.Fatalf("failed to sign checksums: %v", err)
			}
			_, _ = w.Write(sig.Encode())
			return
		}

		if r.URL.Path == "/download/asset" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-binary-new-content"))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	targetExecPath = fakeExecPath
	defer func() { targetExecPath = "" }()

	err = SelfUpgrade("v1.0.2", "")
	if err != nil {
		t.Fatalf("SelfUpgrade failed: %v", err)
	}

	newContent, err := os.ReadFile(fakeExecPath)
	if err != nil {
		t.Fatalf("failed to read fake binary after upgrade: %v", err)
	}

	if string(newContent) != "fake-binary-new-content" {
		t.Errorf("expected new content 'fake-binary-new-content', got %s", string(newContent))
	}
}

func TestSelfUpgrade_ChecksumMismatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	execFilename := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		execFilename += ".exe"
	}
	fakeExecPath := filepath.Join(tmpDir, execFilename)

	if err := os.WriteFile(fakeExecPath, []byte("fake-binary-old-content"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/peterrichards-lr/lfr-tunnel/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			rel := Release{
				TagName: "v1.0.3",
				Assets: []struct {
					Name        string `json:"name"`
					DownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:        assetName,
						DownloadURL: fmt.Sprintf("http://%s/download/asset", r.Host),
					},
					{
						Name:        "checksums.txt",
						DownloadURL: fmt.Sprintf("http://%s/download/checksums", r.Host),
					},
					{
						Name:        "checksums.txt.minisig",
						DownloadURL: fmt.Sprintf("http://%s/download/signature", r.Host),
					},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}

		if r.URL.Path == "/download/checksums" {
			w.WriteHeader(http.StatusOK)
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			// Write an incorrect checksum
			_, _ = fmt.Fprintf(w, "%s  %s\n", "badchecksum1234567890", assetName)
			return
		}

		if r.URL.Path == "/download/signature" {
			w.WriteHeader(http.StatusOK)
			sk, err := minisign.DecodePrivateKey(testSK)
			if err != nil {
				t.Fatalf("failed to decode private key: %v", err)
			}
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			// Sign exactly the bad checksums content we serve above
			checksumsText := fmt.Sprintf("%s  %s\n", "badchecksum1234567890", assetName)

			sig, err := sk.Sign([]byte(checksumsText), minisign.SignOptions{Hashed: true})
			if err != nil {
				t.Fatalf("failed to sign checksums: %v", err)
			}
			_, _ = w.Write(sig.Encode())
			return
		}

		if r.URL.Path == "/download/asset" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-binary-new-content"))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	targetExecPath = fakeExecPath
	defer func() { targetExecPath = "" }()

	err = SelfUpgrade("v1.0.2", "")
	if err == nil {
		t.Fatal("expected SelfUpgrade to fail due to checksum mismatch, but it succeeded")
	}

	if !strings.Contains(err.Error(), "integrity check failed") {
		t.Errorf("expected error message to contain 'integrity check failed', got: %v", err)
	}

	// Verify that the executable file was NOT replaced and still has old content
	content, err := os.ReadFile(fakeExecPath)
	if err != nil {
		t.Fatalf("failed to read fake binary: %v", err)
	}

	if string(content) != "fake-binary-old-content" {
		t.Errorf("expected binary to retain 'fake-binary-old-content', got %s", string(content))
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.1.0", "v1.0.1", 1},
		{"1.0.0", "v1.0.0", 0},
		{"v1.2", "v1.2.0", 0},
		{"v1.7.7-dirty", "v1.7.7", 0},
		{"v1.7.7-8-g3cc6820", "v1.7.7", 0},
		{"v1.7.8-dirty", "v1.7.7", 1},
		{"v1.7.6-8-g3cc6820", "v1.7.7", -1},
		{"v1.7.7-8-g3cc6820-dirty", "v1.7.7", 0},
		{"v1.7.7-8-g3cc6820-dirty", "v1.7.8", -1},
	}
	for _, tt := range tests {
		got := CompareVersions(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d; want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestSelfUpgrade_GatewayFirst(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	execFilename := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		execFilename += ".exe"
	}
	fakeExecPath := filepath.Join(tmpDir, execFilename)

	if err := os.WriteFile(fakeExecPath, []byte("fake-binary-old-content"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			osKey := runtime.GOOS
			if osKey == "darwin" {
				osKey = "macos"
			}
			platformKey := fmt.Sprintf("%s_%s", osKey, runtime.GOARCH)

			resp := ServerVersionInfo{
				LatestVersion: "v1.0.3",
				MinVersion:    "v1.0.0",
				ClientPlatforms: map[string]ServerPlatformInfo{
					platformKey: {
						URL:         "/static/downloads/lfr-tunnel-fake",
						BinaryName:  "lfr-tunnel-fake",
						Recommended: "url",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/static/downloads/checksums.txt" {
			w.WriteHeader(http.StatusOK)
			h := sha256.Sum256([]byte("fake-binary-new-content"))
			hexHash := hex.EncodeToString(h[:])
			_, _ = fmt.Fprintf(w, "%s  lfr-tunnel-fake\n", hexHash)
			return
		}

		if r.URL.Path == "/static/downloads/checksums.txt.minisig" {
			w.WriteHeader(http.StatusOK)
			sk, err := minisign.DecodePrivateKey(testSK)
			if err != nil {
				t.Fatalf("failed to decode private key: %v", err)
			}
			h := sha256.Sum256([]byte("fake-binary-new-content"))
			hexHash := hex.EncodeToString(h[:])
			checksumsText := fmt.Sprintf("%s  lfr-tunnel-fake\n", hexHash)

			sig, err := sk.Sign([]byte(checksumsText), minisign.SignOptions{Hashed: true})
			if err != nil {
				t.Fatalf("failed to sign checksums: %v", err)
			}
			_, _ = w.Write(sig.Encode())
			return
		}

		if r.URL.Path == "/static/downloads/lfr-tunnel-fake" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-binary-new-content"))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	targetExecPath = fakeExecPath
	defer func() { targetExecPath = "" }()

	err = SelfUpgrade("v1.0.2", srv.URL)
	if err != nil {
		t.Fatalf("SelfUpgrade with gateway URL failed: %v", err)
	}

	newContent, err := os.ReadFile(fakeExecPath)
	if err != nil {
		t.Fatalf("failed to read fake binary after upgrade: %v", err)
	}

	if string(newContent) != "fake-binary-new-content" {
		t.Errorf("expected new content 'fake-binary-new-content', got %s", string(newContent))
	}
}

func TestSelfUpgrade_GatewayRecommendation(t *testing.T) {
	tests := []struct {
		rec         string
		cmdVal      string
		expectedMsg string
	}{
		{"brew", "", "Recommended upgrade method is via Homebrew"},
		{"scoop", "", "Recommended upgrade method is via Scoop"},
		{"cmd", "curl -sSfL custom | sh", "Recommended upgrade method is running the installation command"},
	}

	for _, tt := range tests {
		t.Run(tt.rec, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				osKey := runtime.GOOS
				if osKey == "darwin" {
					osKey = "macos"
				}
				platformKey := fmt.Sprintf("%s_%s", osKey, runtime.GOARCH)

				resp := ServerVersionInfo{
					LatestVersion: "v1.0.3",
					MinVersion:    "v1.0.0",
					ClientPlatforms: map[string]ServerPlatformInfo{
						platformKey: {
							Recommended: tt.rec,
							Cmd:         tt.cmdVal,
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			err := SelfUpgrade("v1.0.2", srv.URL)
			if err != nil {
				t.Fatalf("unexpected error for recommendation test: %v", err)
			}
		})
	}
}

func TestCheckServerCompatibility(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"server_version":"v1.27.0", "min_client_version":"v1.20.0"}`)) //nolint:errcheck
	}))
	defer ts.Close()
	_, _ = CheckServerCompatibility(ts.URL) //nolint:errcheck
}

func TestSelfUpgrade_InvalidSignature(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	execFilename := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		execFilename += ".exe"
	}
	fakeExecPath := filepath.Join(tmpDir, execFilename)

	if err := os.WriteFile(fakeExecPath, []byte("fake-binary-old-content"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/peterrichards-lr/lfr-tunnel/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			rel := Release{
				TagName: "v1.0.3",
				Assets: []struct {
					Name        string `json:"name"`
					DownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:        assetName,
						DownloadURL: fmt.Sprintf("http://%s/download/asset", r.Host),
					},
					{
						Name:        "checksums.txt",
						DownloadURL: fmt.Sprintf("http://%s/download/checksums", r.Host),
					},
					{
						Name:        "checksums.txt.minisig",
						DownloadURL: fmt.Sprintf("http://%s/download/signature", r.Host),
					},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}

		if r.URL.Path == "/download/checksums" {
			w.WriteHeader(http.StatusOK)
			assetName := fmt.Sprintf("lfr-tunnel-%s-%s", runtime.GOOS, runtime.GOARCH)
			if runtime.GOOS == "windows" {
				assetName += ".exe"
			}
			h := sha256.Sum256([]byte("fake-binary-new-content"))
			hexHash := hex.EncodeToString(h[:])
			_, _ = fmt.Fprintf(w, "%s  %s\n", hexHash, assetName)
			return
		}

		if r.URL.Path == "/download/signature" {
			w.WriteHeader(http.StatusOK)
			// Return a garbage/invalid signature string!
			_, _ = w.Write([]byte("untrusted comment: minisign signature\nGARBAGESIGNATUREBASE64"))
			return
		}

		if r.URL.Path == "/download/asset" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-binary-new-content"))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	targetExecPath = fakeExecPath
	defer func() { targetExecPath = "" }()

	err = SelfUpgrade("v1.0.2", "")
	if err == nil {
		t.Fatal("expected SelfUpgrade to fail due to invalid signature, but it succeeded")
	}

	if !strings.Contains(err.Error(), "signature verification failed") && !strings.Contains(err.Error(), "failed to decode signature") {
		t.Errorf("expected signature verification or decoding failure, got: %v", err)
	}
}
