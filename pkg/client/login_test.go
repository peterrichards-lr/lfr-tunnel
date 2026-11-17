package client

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// The two shapes a gateway has ever issued (#2457): bare 32-hex from the portal
// (generateSecureToken) and lfr_pat_-prefixed from the registration-claim path.
const (
	portalPAT  = "0123456789abcdef0123456789abcdef"
	claimedPAT = "lfr_pat_fedcba9876543210fedcba9876543210"
	// What #2457's reporter most plausibly pasted: a portal URL, not a token.
	notAToken = "https://portal.example.com/portal?token=0123456789abcdef0123456789abcdef&lang=x"
)

// stubLoginBrowser stops RunLogin opening a real browser for the length of a test, and answers
// its token check as "verified" so no login test reaches a real gateway. A test about the check
// itself overrides that with answerTokenCheck afterwards.
func stubLoginBrowser(t *testing.T) {
	t.Helper()
	old := openBrowserFunc
	openBrowserFunc = func(string) error { return nil }
	t.Cleanup(func() { openBrowserFunc = old })
	answerTokenCheck(t, loginTokenVerified, "")
}

// answerTokenCheck makes the gateway's answer to RunLogin's token check check/reason.
func answerTokenCheck(t *testing.T, check loginTokenCheck, reason string) {
	t.Helper()
	old := verifyLoginToken
	verifyLoginToken = func(string, string) (loginTokenCheck, string) { return check, reason }
	t.Cleanup(func() { verifyLoginToken = old })
}

// handoffBusy makes the handoff listener unavailable, so stdin is the only route in.
func handoffBusy(t *testing.T) {
	t.Helper()
	old := listenHandoff
	listenHandoff = func() (net.Listener, error) {
		return nil, errors.New("listen tcp 127.0.0.1:4444: bind: address already in use")
	}
	t.Cleanup(func() { listenHandoff = old })
}

// handoffEphemeral binds the handoff listener to a free port and reports its address.
func handoffEphemeral(t *testing.T) <-chan string {
	t.Helper()
	addr := make(chan string, 1)
	old := listenHandoff
	listenHandoff = func() (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		addr <- l.Addr().String()
		return l, nil
	}
	t.Cleanup(func() { listenHandoff = old })
	return addr
}

// stdinWith feeds RunLogin's paste prompt from a string.
func stdinWith(t *testing.T, s string) {
	t.Helper()
	old := stdinReader
	stdinReader = strings.NewReader(s)
	t.Cleanup(func() { stdinReader = old })
}

// seedToken writes an existing, working token file, as a user who logs in again has.
func seedToken(t *testing.T, home, token string) string {
	t.Helper()
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create token dir: %v", err)
	}
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
	return path
}

func postHandoff(t *testing.T, addr, body string) int {
	t.Helper()
	resp, err := http.Post("http://"+addr+"/handoff", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatalf("handoff POST failed: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Logf("draining handoff response: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Logf("closing handoff response: %v", err)
	}
	return resp.StatusCode
}

// #2457: a value pasted at the prompt that is not a token was saved over the user's
// working token, and login reported success. It must fail, and leave the old token alone.
func TestRunLogin_RefusesNonTokenPastedOnStdin(t *testing.T) {
	stubLoginBrowser(t)
	handoffBusy(t)
	stdinWith(t, notAToken+"\r\n")
	home := fakeHome(t)
	tokenPath := seedToken(t, home, portalPAT)

	err := RunLogin("https://tunnel.example.com")

	if err == nil {
		t.Fatal("RunLogin accepted a value that is not a token; it must fail")
	}
	if !strings.Contains(err.Error(), "not an access token") {
		t.Errorf("expected the error to say the value is not an access token, got: %v", err)
	}
	if strings.Contains(err.Error(), notAToken) {
		t.Errorf("the error must not echo the pasted value in full, got: %v", err)
	}
	saved, readErr := os.ReadFile(tokenPath)
	if readErr != nil {
		t.Fatalf("the existing token file must survive a refused value: %v", readErr)
	}
	if string(saved) != portalPAT {
		t.Errorf("token file = %q, want the previous token %q left untouched", saved, portalPAT)
	}
}

// #2457: the handoff listener answered 200 to any body from anyone and ended the login with
// it. A non-token must get 400 and leave the login waiting, so the real token can still
// arrive.
func TestRunLogin_HandoffRefusesNonTokenAndKeepsWaiting(t *testing.T) {
	stubLoginBrowser(t)
	addrChan := handoffEphemeral(t)

	// A stdin that never yields until the test is over, so only the handoff can deliver.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	old := stdinReader
	stdinReader = stdinR
	t.Cleanup(func() {
		stdinReader = old
		if err := stdinW.Close(); err != nil {
			t.Logf("closing stdin pipe: %v", err)
		}
	})

	home := fakeHome(t)
	errChan := make(chan error, 1)
	go func() { errChan <- RunLogin("https://tunnel.example.com") }()

	addr := <-addrChan
	if code := postHandoff(t, addr, notAToken); code != http.StatusBadRequest {
		t.Errorf("handoff of a non-token answered %d, want %d", code, http.StatusBadRequest)
	}
	select {
	case runErr := <-errChan:
		t.Fatalf("a refused handoff must not end the login, but RunLogin returned: %v", runErr)
	case <-time.After(200 * time.Millisecond):
	}

	if code := postHandoff(t, addr, portalPAT); code != http.StatusOK {
		t.Errorf("handoff of a real token answered %d, want %d", code, http.StatusOK)
	}
	select {
	case runErr := <-errChan:
		if runErr != nil {
			t.Fatalf("RunLogin failed after a real token was handed off: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLogin did not finish after a real token was handed off")
	}

	saved, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
	if err != nil {
		t.Fatalf("expected the handed-off token to be saved: %v", err)
	}
	if string(saved) != portalPAT {
		t.Errorf("saved token = %q, want %q", saved, portalPAT)
	}
}

func TestLooksLikeAccessToken(t *testing.T) {
	cases := map[string]bool{
		portalPAT:                       true,
		claimedPAT:                      true,
		notAToken:                       false,
		"":                              false,
		portalPAT[:31]:                  false, // one short
		portalPAT + "0":                 false, // one long
		strings.ToUpper(portalPAT):      false, // the gateway issues lowercase only
		"lfr_pat_" + portalPAT[:31]:     false,
		"LFT_CLIENT_TOKEN=" + portalPAT: false, // a secrets-file line
		"Bearer " + portalPAT:           false,
		portalPAT + "\r":                false, // the handoff trims; nothing else may smuggle it in
	}
	for in, want := range cases {
		if got := looksLikeAccessToken(in); got != want {
			t.Errorf("looksLikeAccessToken(%q) = %v, want %v", in, got, want)
		}
	}
}

// The refusal must let the user recognise what they pasted without printing a secret they may
// have pasted by mistake.
func TestDescribeRejectedValue(t *testing.T) {
	if got, want := describeRejectedValue(notAToken), `79 characters, starting "http"`; got != want {
		t.Errorf("describeRejectedValue = %q, want %q", got, want)
	}
	if got, want := describeRejectedValue("abc"), "3 characters"; got != want {
		t.Errorf("describeRejectedValue = %q, want %q", got, want)
	}
}

// A junk paste while the handoff listener is alive is refused and the login keeps going, so the
// next paste (or the browser) can still finish it. Ending the login there threw away a handoff
// that might have been seconds away, and opened a window in which a real handed-off token was
// acknowledged and then dropped (review of #2461).
func TestRunLogin_RePromptsAfterAJunkPasteWhileTheHandoffIsAlive(t *testing.T) {
	stubLoginBrowser(t)
	handoffEphemeral(t)
	stdinWith(t, notAToken+"\n"+portalPAT+"\n")
	home := fakeHome(t)

	errChan := make(chan error, 1)
	go func() { errChan <- RunLogin("https://tunnel.example.com") }()
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("a junk paste ended the login while the handoff could still arrive: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLogin did not finish after a valid paste followed the junk one")
	}
	saved, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
	if err != nil || string(saved) != portalPAT {
		t.Errorf("saved token = %q (%v), want %q", saved, err, portalPAT)
	}
}

// A token file that is a symlink -- into a dotfiles repository or a mounted secret store -- is
// written through, as os.WriteFile always did, not replaced by a regular file that leaves the
// real one holding the old token (review of #2461).
func TestRunLogin_WritesThroughASymlinkedTokenFile(t *testing.T) {
	stubLoginBrowser(t)
	handoffBusy(t)
	stdinWith(t, portalPAT+"\n")
	home := fakeHome(t)

	target := filepath.Join(t.TempDir(), "real-token")
	if err := os.WriteFile(target, []byte(claimedPAT), 0600); err != nil {
		t.Fatalf("seeding target: %v", err)
	}
	link := filepath.Join(home, ".lfr-tunnel", "token")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here (Windows without the privilege): %v", err)
	}

	if err := RunLogin("https://tunnel.example.com"); err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the token file is no longer a symlink (mode %v, err %v)", fi.Mode(), err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the symlink target: %v", err)
	}
	if string(got) != portalPAT {
		t.Errorf("symlink target holds %q, want the new token %q", got, portalPAT)
	}
}

// #2458: each answer the gateway can give is read as exactly one verdict, and only the token-check
// endpoint's own refusal counts as one.
func TestCheckLoginTokenReadsEachAnswer(t *testing.T) {
	type answer struct {
		status      int
		contentType string
	}
	cases := []struct {
		name   string
		answer answer
		want   loginTokenCheck
		reason string
	}{
		{"valid", answer{http.StatusNoContent, ""}, loginTokenVerified, ""},
		{"refused", answer{http.StatusUnauthorized, "application/json"}, loginTokenRefused, ""},
		{"a proxy's 401, not the gateway's", answer{http.StatusUnauthorized, "text/html"}, loginTokenUnknown, "401"},
		{"an edge", answer{http.StatusMisdirectedRequest, "application/json"}, loginTokenUnknown, "regional edge"},
		{"an older gateway", answer{http.StatusNotFound, "text/html"}, loginTokenUnknown, "older than this client"},
		{"token store down", answer{http.StatusServiceUnavailable, "application/json"}, loginTokenUnknown, "503"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotHeader, gotPath, gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHeader, gotPath, gotQuery = r.Header.Get("X-Auth-Token"), r.URL.Path, r.URL.RawQuery
				if tc.answer.contentType != "" {
					w.Header().Set("Content-Type", tc.answer.contentType)
				}
				w.WriteHeader(tc.answer.status)
			}))
			defer srv.Close()

			got, reason := checkLoginToken(srv.URL+"/", portalPAT)
			if got != tc.want {
				t.Errorf("verdict %v (%q), want %v", got, reason, tc.want)
			}
			if tc.reason != "" && !strings.Contains(reason, tc.reason) {
				t.Errorf("reason %q does not say %q", reason, tc.reason)
			}
			if gotHeader != portalPAT || gotPath != "/api/client/token-check" || gotQuery != "" {
				t.Errorf("sent header %q to %q?%q; want the token in X-Auth-Token only", gotHeader, gotPath, gotQuery)
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		addr := srv.URL
		srv.Close()
		if got, reason := checkLoginToken(addr, portalPAT); got != loginTokenUnknown || !strings.Contains(reason, "could not reach") {
			t.Errorf("unreachable gateway: %v %q", got, reason)
		}
	})
	t.Run("no gateway configured", func(t *testing.T) {
		if got, _ := checkLoginToken("", portalPAT); got != loginTokenUnknown {
			t.Errorf("no gateway: %v", got)
		}
	})
}

// captureLoginOutput returns what fn printed to stdout.
func captureLoginOutput(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("reading captured output: %v", err)
		}
		done <- string(b)
	}()
	fn()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe: %v", err)
	}
	return <-done
}

// #2458: a token the gateway refuses is not saved, and the working one stays.
func TestRunLogin_RefusedTokenIsNotSaved(t *testing.T) {
	stubLoginBrowser(t)
	answerTokenCheck(t, loginTokenRefused, "")
	handoffBusy(t)
	stdinWith(t, portalPAT+"\n")
	home := fakeHome(t)
	tokenPath := seedToken(t, home, claimedPAT)

	err := RunLogin("https://tunnel.example.com")
	if err == nil || !strings.Contains(err.Error(), "refused this token") {
		t.Fatalf("RunLogin = %v; want the gateway's refusal", err)
	}
	saved, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("reading the token file: %v", err)
	}
	if string(saved) != claimedPAT {
		t.Errorf("token file = %q; the refused token must not replace %q", saved, claimedPAT)
	}
}

// #2458: a gateway that cannot answer says nothing about the token. It is saved -- refusing would
// lock out everyone on a gateway older than this check -- and the output says it is unverified
// rather than claiming success.
func TestRunLogin_UnverifiedTokenIsSavedAndSaysSo(t *testing.T) {
	stubLoginBrowser(t)
	answerTokenCheck(t, loginTokenUnknown, "the gateway is older than this client")
	handoffBusy(t)
	stdinWith(t, portalPAT+"\n")
	home := fakeHome(t)

	var err error
	out := captureLoginOutput(t, func() { err = RunLogin("https://tunnel.example.com") })
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	saved, readErr := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
	if readErr != nil {
		t.Fatalf("reading the token file: %v", readErr)
	}
	if string(saved) != portalPAT {
		t.Errorf("saved %q, want %q", saved, portalPAT)
	}
	if !strings.Contains(out, "not verified: the gateway is older than this client") {
		t.Errorf("output does not say the token is unverified, and why:\n%s", out)
	}
	if strings.Contains(out, "Successfully authenticated") {
		t.Errorf("an unverified token was reported as a success:\n%s", out)
	}
}

// BOUNDING, not FIRING: passes before and after #2457. It pins that both shapes a gateway
// issues still log in, so a shape check that is too strict turns this red.
func TestRunLogin_AcceptsEveryIssuedTokenShape(t *testing.T) {
	for _, token := range []string{portalPAT, claimedPAT} {
		t.Run(token[:8], func(t *testing.T) {
			stubLoginBrowser(t)
			handoffBusy(t)
			stdinWith(t, token+"\n")
			home := fakeHome(t)

			if err := RunLogin("https://tunnel.example.com"); err != nil {
				t.Fatalf("RunLogin refused a token shape the gateway issues: %v", err)
			}
			saved, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
			if err != nil {
				t.Fatalf("expected the token to be saved: %v", err)
			}
			if string(saved) != token {
				t.Errorf("saved token = %q, want %q", saved, token)
			}
		})
	}
}

// resolvePortalURL used to fall back to one organisation's production portal, so a
// self-hoster whose gateway wasn't named tunnel.* was sent to a stranger's login page to
// authenticate (#1188).
func TestResolvePortalURL(t *testing.T) {
	tests := []struct {
		name          string
		serverURL     string
		defaultPortal string
		want          string
	}{
		{
			name:      "the tunnel/portal convention is honoured for anyone, not just one deployment",
			serverURL: "https://tunnel.example.com",
			want:      "https://portal.example.com",
		},
		{
			name:      "a self-hosted deployment following the convention resolves to its own portal",
			serverURL: "https://tunnel.mycorp.internal",
			want:      "https://portal.mycorp.internal",
		},
		{
			// The important case: no convention to follow and no configured portal. The
			// gateway serves the portal too, so sending them there is both correct and the
			// safe direction to be wrong in -- unlike sending them to a third party.
			name:      "a gateway named something else falls back to the gateway itself",
			serverURL: "https://gateway.mycorp.internal",
			want:      "https://gateway.mycorp.internal",
		},
		{
			name:          "a build with a configured portal uses it when the convention does not apply",
			serverURL:     "https://gateway.mycorp.internal",
			defaultPortal: "https://portal.mycorp.internal",
			want:          "https://portal.mycorp.internal",
		},
		{
			// The convention wins over the build-time default, so one binary still works
			// against several gateways.
			name:          "the derived portal wins over the configured default",
			serverURL:     "https://tunnel.other.example",
			defaultPortal: "https://portal.mycorp.internal",
			want:          "https://portal.other.example",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := config.DefaultPortalURL
			config.DefaultPortalURL = tc.defaultPortal
			t.Cleanup(func() { config.DefaultPortalURL = original })

			if got := resolvePortalURL(tc.serverURL); got != tc.want {
				t.Errorf("resolvePortalURL(%q) = %q, want %q", tc.serverURL, got, tc.want)
			}
		})
	}
}

// No deployment hostname may be baked into the source tree. They are injected at build
// time instead (#1188, same class as #1124).
func TestDeploymentURLsAreNotHardcoded(t *testing.T) {
	cases := map[string]string{
		"DefaultServerURL":     config.DefaultServerURL,
		"DefaultStatusPageURL": config.DefaultStatusPageURL,
		"DefaultPortalURL":     config.DefaultPortalURL,
	}
	for name, value := range cases {
		if value != "" {
			t.Errorf("%s is %q in an unconfigured build -- deployment hostnames must come "+
				"from -ldflags, not from source", name, value)
		}
	}
}
