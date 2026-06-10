package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	// 1. Create a mock GitHub server returning a newer version
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rel := Release{
			TagName: "v1.0.3",
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	// Override API endpoint
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	// 2. Query update with current version v1.0.2
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
	// Create a temporary mock binary that represents the running executable
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execFilename := "lfr-tunnel-fake"
	if runtime.GOOS == "windows" {
		execFilename += ".exe"
	}
	fakeExecPath := filepath.Join(tmpDir, execFilename)

	// Write initial dummy content to fake binary
	if err := os.WriteFile(fakeExecPath, []byte("fake-binary-old-content"), 0755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	// Mock server to host the release API and the binary asset download
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
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
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

	// Override API base
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = oldBase }()

	// Override targetExecPath
	targetExecPath = fakeExecPath
	defer func() { targetExecPath = "" }()

	// Execute SelfUpgrade from v1.0.2 to v1.0.3
	err = SelfUpgrade("v1.0.2")
	if err != nil {
		t.Fatalf("SelfUpgrade failed: %v", err)
	}

	// Verify that the executable file was replaced with the new content
	newContent, err := os.ReadFile(fakeExecPath)
	if err != nil {
		t.Fatalf("failed to read fake binary after upgrade: %v", err)
	}

	if string(newContent) != "fake-binary-new-content" {
		t.Errorf("expected new content 'fake-binary-new-content', got %s", string(newContent))
	}
}
