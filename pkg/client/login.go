package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
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

	// net.Listen synchronously, rather than letting srv.ListenAndServe() bind the socket
	// inside its own goroutine -- otherwise nothing guarantees the port is actually
	// listening before this function returns control to the caller and the browser
	// starts its handoff POST. Normally that bind takes microseconds, well within any
	// real browser's round-trip time, but it's a genuine (if rare) race either way -- and
	// it's exactly what made TestRunLogin flaky under CI scheduling pressure: its handoff
	// POST fired on a fixed timer, occasionally racing ListenAndServe's own goroutine and
	// hanging the whole test until Go's 10-minute timeout killed it, since the browser
	// mock and the stdin fallback would both then have nothing to deliver.
	listener, err := net.Listen("tcp", "127.0.0.1:4444")
	if err != nil {
		return fmt.Errorf("failed to start local handoff listener: %w", err)
	}
	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(listener) //nolint:errcheck
	}()

	portalURL := resolvePortalURL(serverURL)

	fmt.Println("Opening your browser to authenticate...")
	_ = openBrowserFunc(portalURL) //nolint:errcheck

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
	_ = srv.Shutdown(ctx) //nolint:errcheck

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

// resolvePortalURL works out which portal to open a browser at for a given gateway.
//
// Deployments conventionally name the gateway tunnel.<domain> and the portal
// portal.<domain>, so the substitution covers the normal case for anyone, not just one
// organisation. When it doesn't apply -- a gateway called something else entirely -- fall
// back to this build's configured portal if it has one, and otherwise to the gateway
// itself, which serves the portal too.
//
// The last step matters: this used to fall back to one organisation's production portal, so
// a self-hoster whose gateway wasn't named tunnel.* was sent to a stranger's login page to
// authenticate (#1188). Sending them to the server they are already talking to is both
// correct and the safe direction to be wrong in.
func resolvePortalURL(serverURL string) string {
	if portal := strings.Replace(serverURL, "tunnel.", "portal.", 1); strings.Contains(portal, "portal.") {
		return portal
	}
	if config.DefaultPortalURL != "" {
		return config.DefaultPortalURL
	}
	return serverURL
}

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
