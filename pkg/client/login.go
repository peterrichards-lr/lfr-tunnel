package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
			token := normaliseToken(string(body))
			// Anything that is not a token is refused and the login keeps waiting (#2457), so a
			// stray POST can no longer end the login with junk. This is a shape check only: the
			// listener still answers any local process or web page, and one that POSTs a real
			// token -- its own -- is accepted, as it was before.
			if looksLikeAccessToken(token) {
				w.WriteHeader(http.StatusOK)
				select {
				case tokenChan <- token:
				default: // a token was already delivered; this login is finishing
				}
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

	// Marked so the portal asks for a token: the CLI only receives one when the user creates it, and
	// without a prompt a signed-in user just saw the Overview and nothing ever arrived (#2459).
	portalURL := withCLILoginMarker(resolvePortalURL(serverURL))

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
		// A whole line at a time, not fmt.Fscan's word at a time (#2479): `Bearer <token>` was
		// refused for "Bearer" and then accepted as the next word, and a token pasted with a
		// space inside its quotes was split. An empty line (Enter on its own) keeps waiting.
		lines := bufio.NewReader(in)
		for {
			line, readErr := lines.ReadString('\n')
			manualToken := normaliseToken(line)
			if manualToken == "" {
				if readErr != nil {
					break
				}
				continue
			}
			if looksLikeAccessToken(manualToken) {
				tokenChan <- manualToken
				return
			}
			// Not a token (#2457). It used to be saved over a working token and reported as
			// success. With the handoff listener up, refuse it and keep waiting: the browser
			// may still deliver, and ending the login here would throw that away. With no
			// listener, nothing else can deliver, so fail now and say why.
			refusal := refusedTokenError(manualToken)
			if listenErr != nil {
				failChan <- refusal
				return
			}
			fmt.Printf("\n⚠️  %v\n   Still waiting for the browser, or paste a token here: ", refusal)
			if readErr != nil {
				break
			}
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

	// Ask the gateway before saving (#2458). A well-formed token can still be revoked, expired or
	// minted by a different gateway, and "Successfully authenticated" followed by a 401 on first
	// use is the report #2457 started from. Only a definite refusal stops the save: a gateway
	// that cannot answer has said nothing about the token, so it is saved and reported as unverified.
	check, reason := verifyLoginToken(serverURL, token)
	if check == loginTokenRefused {
		return fmt.Errorf("the gateway at %s refused this token -- it may be revoked or expired, "+
			"or have come from a different gateway's portal; nothing was saved", serverURL)
	}

	// Save the token. Both routes above deliver only values looksLikeAccessToken accepts.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get home dir: %w", err)
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("could not create config dir: %w", err)
	}

	if err := writeTokenFile(dir, token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	if check != loginTokenVerified {
		fmt.Printf("\n⚠️  Token saved to ~/.lfr-tunnel/token, but not verified: %s.\n"+
			"   It will be checked when you next connect.\n", reason)
		return nil
	}
	fmt.Println("\n✅ Successfully authenticated! Your token has been saved securely to ~/.lfr-tunnel/token")
	return nil
}

// loginTokenCheck is what the gateway said about a token login is about to save.
type loginTokenCheck int

const (
	loginTokenVerified loginTokenCheck = iota
	loginTokenRefused
	// loginTokenUnknown: the gateway could not say -- unreachable, an edge, a gateway older than
	// the check, a failing token store. Never treated as a refusal.
	loginTokenUnknown
)

// verifyLoginToken is a variable so tests can answer for the gateway without a network.
var verifyLoginToken = checkLoginToken

// loginTokenCheckClient never follows a redirect. Go copies a custom header like X-Auth-Token to a
// redirect's new host -- it strips only Authorization, Cookie and WWW-Authenticate -- so following
// one would hand the token to whoever the gateway pointed at, and that host's answer would say
// nothing about this gateway's view of the token (review of #2467).
var loginTokenCheckClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// checkLoginToken asks the gateway at serverURL whether token is usable (#2458). The token travels
// in X-Auth-Token only, never the URL. Only the endpoint's own answer is a refusal: a 401 that is
// not JSON came from something in front of the gateway (a proxy's login page), and says nothing
// about the token.
func checkLoginToken(serverURL, token string) (loginTokenCheck, string) {
	if strings.TrimSpace(serverURL) == "" {
		return loginTokenUnknown, "no gateway is configured to check it against"
	}
	// Addressed as RegisterTunnel addresses the gateway: a scheme added when missing, and only
	// scheme://host kept, so any server_url registration accepts works here too.
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return loginTokenUnknown, fmt.Sprintf("the gateway address %q is not usable", serverURL)
	}
	req, err := http.NewRequest(http.MethodGet, parsed.Scheme+"://"+parsed.Host+"/api/client/token-check", nil)
	if err != nil {
		return loginTokenUnknown, fmt.Sprintf("the gateway address %q is not usable: %v", serverURL, err)
	}
	req.Header.Set("X-Auth-Token", token)
	resp, err := loginTokenCheckClient.Do(req)
	if err != nil {
		return loginTokenUnknown, fmt.Sprintf("could not reach %s (%v)", serverURL, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug(fmt.Sprintf("[Client] Closing the token-check response: %v", err))
		}
	}()

	switch {
	case resp.StatusCode == http.StatusNoContent:
		return loginTokenVerified, ""
	case resp.StatusCode == http.StatusUnauthorized && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"):
		return loginTokenRefused, ""
	case resp.StatusCode == http.StatusMisdirectedRequest:
		return loginTokenUnknown, serverURL + " is a regional edge, which cannot check tokens"
	case resp.StatusCode == http.StatusNotFound:
		// An older gateway, or an address that reaches the gateway without being one of its own
		// names (an alias), and so is served as a tunnel host. Either way it cannot say.
		return loginTokenUnknown, serverURL + " has no token check (an older gateway, or not the gateway's own address)"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return loginTokenUnknown, fmt.Sprintf("%s answered with a redirect to %q, which was not followed", serverURL, resp.Header.Get("Location"))
	default:
		return loginTokenUnknown, fmt.Sprintf("%s answered %s", serverURL, resp.Status)
	}
}

// claimedTokenPrefix marks the tokens minted by the registration-claim path.
const claimedTokenPrefix = "lfr_pat_"

// accessTokenPattern is every shape a gateway has issued (#2457): 32 lowercase hex from the
// portal (generateSecureToken in pkg/server/server.go), and the same with claimedTokenPrefix
// from the registration-claim path. The gateway itself accepts any string whose hash it has
// stored, so this is the client's only way to tell a token from a stray paste before saving.
var accessTokenPattern = regexp.MustCompile(`^(` + claimedTokenPrefix + `)?[0-9a-f]{32}$`)

// normaliseToken undoes what copying a token from chat, a document or an HTTP header adds to it
// (#2479): surrounding whitespace and CRLF, one matching pair of surrounding quotes (", ' or a
// backtick), and an Authorization scheme ("Bearer <token>"). Applied on both routes -- the paste
// prompt and the handoff -- before looksLikeAccessToken, which still decides; nothing here can
// turn a non-token into one.
func normaliseToken(s string) string {
	// Quotes on either side of the scheme: "Bearer <token>" and Bearer "<token>".
	s = unquoteOnce(strings.TrimSpace(s))
	const scheme = "bearer "
	if len(s) > len(scheme) && strings.EqualFold(s[:len(scheme)], scheme) {
		s = unquoteOnce(strings.TrimSpace(s[len(scheme):]))
	}
	return s
}

// unquoteOnce removes one matching pair of surrounding quotes, and the whitespace inside them.
func unquoteOnce(s string) string {
	if len(s) >= 2 {
		if q := s[0]; q == s[len(s)-1] && (q == '"' || q == '\'' || q == '`') {
			return strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

func looksLikeAccessToken(s string) bool {
	return accessTokenPattern.MatchString(s)
}

// refusedTokenError explains why a pasted value was not saved.
func refusedTokenError(s string) error {
	return fmt.Errorf("the value entered is not an access token (%s); nothing was saved. "+
		"Paste a token from the token list in the portal: 32 hex characters, "+
		"optionally starting with %q", describeRejectedValue(s), claimedTokenPrefix)
}

// describeRejectedValue says enough about a refused value for the user to recognise what they
// pasted -- a URL, a secrets-file line -- without printing a secret they pasted by mistake.
// Counted in characters, not bytes, so a pasted quote or BOM is never shown as half a rune.
func describeRejectedValue(s string) string {
	const shown = 4
	r := []rune(s)
	if len(r) <= shown {
		return fmt.Sprintf("%d characters", len(r))
	}
	return fmt.Sprintf("%d characters, starting %q", len(r), string(r[:shown]))
}

// writeTokenFile replaces the token file atomically, so a failed write cannot leave the user
// with a truncated token in place of a working one. A token file that is a symlink is written
// through to its target, as os.WriteFile did, rather than replaced by a regular file.
func writeTokenFile(dir, token string) error {
	path := filepath.Join(dir, "token")
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "token-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(token); err != nil {
		if closeErr := tmp.Close(); closeErr != nil {
			slog.Debug(fmt.Sprintf("[Client] Closing the temp token file after a failed write: %v", closeErr))
		}
		removeTempToken(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		removeTempToken(tmpPath)
		return err
	}
	// os.CreateTemp already creates the file 0600, the mode the token file has always had.
	if err := os.Rename(tmpPath, path); err != nil {
		removeTempToken(tmpPath)
		return err
	}
	return nil
}

func removeTempToken(path string) {
	if err := os.Remove(path); err != nil {
		slog.Debug(fmt.Sprintf("[Client] Removing the temp token file %s: %v", path, err))
	}
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
		// Not `cmd /c start`: cmd treats "=" and "&" in the URL as separators, which would cut
		// the portal URL at ?cli_login=1 (#2459). FileProtocolHandler hands the URL to the default
		// browser verbatim.
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}

// withCLILoginMarker adds cli_login=1 to the portal URL `login` opens (#2459), so the portal opens
// its create-token dialog -- immediately for a signed-in user, after sign-in otherwise.
func withCLILoginMarker(portalURL string) string {
	u, err := url.Parse(portalURL)
	if err != nil {
		return portalURL
	}
	q := u.Query()
	q.Set("cli_login", "1")
	u.RawQuery = q.Encode()
	return u.String()
}
