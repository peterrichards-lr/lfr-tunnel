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
	listener, listenErr := listenHandoff()
	var srv *http.Server
	if listenErr != nil {
		// An occupied port is not a reason to refuse to log in (#1718). The portal
		// already offers the token for copying whenever its handoff POST fails, and the
		// prompt below accepts it on stdin, so the flow still completes -- just by hand.
		// 4444 is a popular port (Selenium Grid's default, plus assorted debuggers and
		// proxies), which makes a collision routine rather than a broken install, and
		// aborting here threw away a login that would otherwise have worked.
		fmt.Printf("\n⚠️  Could not listen on %s: %v\n", handoffAddr, listenErr)
		fmt.Println("   Automatic token handoff is unavailable for this login -- copy the token from the portal instead.")
		fmt.Printf("   To restore it, find and stop whatever is holding the port:\n     %s\n\n", handoffPortHint())
	} else {
		srv = &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
		go func() {
			_ = srv.Serve(listener) //nolint:errcheck
		}()
	}

	portalURL := resolvePortalURL(serverURL)

	fmt.Println("Opening your browser to authenticate...")
	_ = openBrowserFunc(portalURL) //nolint:errcheck

	fmt.Println("Waiting for token delivery...")
	fmt.Print("If your browser didn't open or handoff fails, paste your token here: ")

	// Read from stdin in a goroutine. The reader is resolved here, on the caller's
	// goroutine, and captured -- not read inside the goroutine. That goroutine outlives
	// RunLogin whenever stdin never yields (the normal case: the browser delivered the
	// token first), so a test that swapped the source afterwards would be writing a
	// variable a live goroutine is still reading. The race detector caught exactly that.
	in := stdinReader
	failChan := make(chan error, 1)
	go func() {
		var manualToken string
		if _, err := fmt.Fscan(in, &manualToken); err == nil && manualToken != "" {
			tokenChan <- manualToken
			return
		}
		if listenErr != nil {
			// Neither route can deliver a token now: the handoff listener never started,
			// and stdin will not produce one either (closed, redirected from /dev/null,
			// or a non-interactive shell). Report the bind failure rather than blocking
			// forever on a channel nothing will ever send to -- an unattended
			// `lfr-tunnel login` still has to fail fast, as it did before #1718.
			failChan <- fmt.Errorf("failed to start local handoff listener on %s (%w), and no token was supplied on stdin", handoffAddr, listenErr)
		}
	}()

	// Wait for token
	var token string
	select {
	case token = <-tokenChan:
	case err := <-failChan:
		return err
	}

	// Shutdown server
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx) //nolint:errcheck
	}

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

// handoffPort is the loopback port the token handoff listener binds to.
//
// It is fixed, and cannot be moved to an ephemeral port or walked upwards the way
// StartInspector walks its own: the portal page that delivers the token POSTs to this
// exact address from the browser -- `fetch('http://127.0.0.1:4444/handoff', ...)` in
// pkg/server/static/dashboard.js -- so the number is half of a contract with JavaScript
// served by whichever gateway the user is logging in to. Nothing negotiates it: the POST
// is fired blind under `mode: 'no-cors'`, and the portal URL this CLI opens carries no
// port for the page to read back. A client that bound elsewhere would sit on a listener
// no browser ever contacts, and would do so against every already-deployed gateway even
// if the JavaScript were changed today.
//
// It is *not* an OAuth redirect_uri: no identity provider has it registered. The SSO flow
// (pkg/server/sso.go) redirects back to the gateway, never to the CLI, and the only
// registered redirect_uri in this repo is Slack's, which is server-side
// (pkg/server/slack.go). So the constraint is the portal's JavaScript, not an IdP --
// which is why #1718 fixes the failure mode rather than the port.
const handoffPort = "4444"

// handoffAddr is where that listener binds.
const handoffAddr = "127.0.0.1:" + handoffPort

// listenHandoff opens the handoff listener. It is a variable so tests can bind an
// ephemeral port instead of the fixed one, or simulate the port being taken; the product
// always uses handoffAddr for the reasons above.
var listenHandoff = func() (net.Listener, error) {
	return net.Listen("tcp", handoffAddr)
}

// stdinReader is where the manual token paste is read from. A variable so tests can
// supply their own pipe rather than mutating the os.Stdin global, which cannot be done
// safely while an earlier login's reader goroutine is still blocked on it.
var stdinReader io.Reader = os.Stdin

// handoffPortHint returns the platform's command for identifying whatever holds the
// handoff port. Naming the port is not actionable on its own -- the next thing anyone
// needs is the process behind it.
func handoffPortHint() string {
	if runtime.GOOS == "windows" {
		return "netstat -ano | findstr :" + handoffPort
	}
	return "lsof -nP -iTCP:" + handoffPort + " -sTCP:LISTEN"
}

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
