package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// RunLogin initiates the hybrid CLI login flow.
func RunLogin(serverURL string) error {
	tokenChan := make(chan string, 1)

	// 1. Start local listener for magic handoff
	mux := http.NewServeMux()
	mux.HandleFunc("/handoff", func(w http.ResponseWriter, r *http.Request) {
		// allow CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			token := strings.TrimSpace(string(body))
			if token != "" {
				w.WriteHeader(http.StatusOK)
				tokenChan <- token
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
	})

	srv := &http.Server{Addr: "127.0.0.1:4444", Handler: mux}
	go func() {
		_ = srv.ListenAndServe()
	}()

	portalURL := strings.Replace(serverURL, "tunnel.", "portal.", 1)
	if !strings.Contains(portalURL, "portal.") {
		portalURL = "https://portal.lfr-demo.se" // Fallback to our canonical portal URL
	}

	fmt.Println("Opening your browser to authenticate...")
	_ = openBrowserFunc(portalURL)

	fmt.Println("Waiting for token delivery...")
	fmt.Print("If your browser didn't open or handoff fails, paste your token here: ")

	// Read from stdin in a goroutine
	go func() {
		var manualToken string
		if _, err := fmt.Scan(&manualToken); err == nil && manualToken != "" {
			tokenChan <- manualToken
		}
	}()

	// Wait for token
	token := <-tokenChan

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	// Save the token
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get home dir: %w", err)
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("could not create config dir: %w", err)
	}

	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	fmt.Println("\n✅ Successfully authenticated! Your token has been saved securely to ~/.lfr-tunnel/token")
	return nil
}

var openBrowserFunc = openBrowser

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
