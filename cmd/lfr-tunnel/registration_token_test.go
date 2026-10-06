package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// The client side of #2342: what a user sees when the gateway will not accept their token, or
// when they have none. Every case here goes red against the code before it -- a 401 was
// retryable and carried no advice, and an empty token was sent to the gateway regardless.

// isolateClientTokenEnvironment gives the loader an empty home and no token variables, so a
// test sees only the token source it sets up and never the developer's own.
func isolateClientTokenEnvironment(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN", "LFT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	// Both, because os.UserHomeDir reads USERPROFILE on Windows and HOME everywhere else.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// tokenlessConfigFile is passed to LoadClientConfig explicitly, as pkg/config's own tests do, rather
// than relying on the default location: the token sources under test still resolve through the
// HOME set above, but nothing reads the default config path of whoever runs the suite.
func tokenlessConfigFile(t *testing.T, home string) string {
	t.Helper()
	// One unrelated key, because an empty file is not a valid config: the decoder reports EOF.
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("subdomain: token-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func adviceText(f *registrationFailure) string {
	return strings.Join(f.advice, "\n")
}

func TestUnauthorizedRegistrationIsTerminal(t *testing.T) {
	srv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized"}`, nil)

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t", TokenSource: "-token flag"}
	_, failure := attemptRegistration(cfg, nil, "sub", nil)
	if failure == nil {
		t.Fatal("expected a failure for HTTP 401")
	}
	if !failure.terminal {
		t.Error("a 401 must be terminal: tokens are checked on central, so no region can accept one another refused")
	}
	if len(failure.advice) == 0 {
		t.Error("a 401 must say what to do next, not only print the status")
	}
}

// The gateway answers every token failure with the same 401 so the answer cannot reveal whether
// a token exists. The client must not undo that by varying its advice on anything else in the
// response -- so two token refusals that differ in every field but the refusal itself must read
// identically, and the advice must not repeat the gateway's message.
func TestUnauthorizedAdviceDoesNotDependOnTheResponse(t *testing.T) {
	a := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized","portal_url":"https://portal.example","server_version":"v1.0.0"}`, nil)
	b := registrationServer(t, http.StatusUnauthorized, `{"status":"denied","error":"unauthorized","portal_url":"https://portal.example","server_version":"v9.9.9","warning":"extra"}`, nil)

	advice := func(url string) *registrationFailure {
		cfg := &config.ClientConfig{ServerURL: url, AuthToken: "t", TokenSource: "-token flag"}
		_, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure == nil {
			t.Fatalf("expected a failure from %s", url)
		}
		return failure
	}
	fa, fb := advice(a.URL), advice(b.URL)

	// Two empty lists are equal too, which is how this passed against a client that gave no
	// advice at all. The property is only worth asserting once there is advice to vary.
	if len(fa.advice) == 0 {
		t.Fatal("no advice was given, so there is nothing to compare")
	}
	if !reflect.DeepEqual(fa.advice, fb.advice) {
		t.Errorf("advice varied with the gateway's response:\n%s\n---\n%s", adviceText(fa), adviceText(fb))
	}
	for _, line := range fa.advice {
		if strings.Contains(line, "v1.0.0") || strings.Contains(line, `"`+gatewayTokenRefusal+`"`) {
			t.Errorf("the advice repeated part of the gateway's response: %q", line)
		}
	}
}

// The client recognises a token refusal by its message, so the two must agree: if the gateway's
// wording changed, every token failure would silently fall back to a retryable, advice-free 401
// and nothing else would go red. Read from the server's source rather than restated, because a
// restated copy is what would drift. Covers both places a user token is refused at registration:
// directly, and on central for an edge.
func TestGatewayTokenRefusalMatchesTheServer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "server", "server.go"))
	if err != nil {
		t.Fatalf("reading the server source: %v", err)
	}
	src := string(raw)
	for _, handler := range []string{"func (s *Server) handleRegister(", "func (s *Server) handleEdgeRegister("} {
		body := funcSource(t, src, handler)
		check := strings.Index(body, "s.authenticateToken(")
		if check < 0 {
			t.Fatalf("%s no longer calls authenticateToken -- re-derive where it refuses a user token", handler)
		}
		refusal := body[check:]
		if end := strings.Index(refusal, "return"); end > 0 {
			refusal = refusal[:end]
		}
		if !strings.Contains(refusal, `"`+gatewayTokenRefusal+`"`) {
			t.Errorf("%s refuses a user token with something other than %q:\n%s", handler, gatewayTokenRefusal, refusal)
		}
	}
}

// `lfr-tunnel login` writes ~/.lfr-tunnel/token, the lowest-ranked source. Advising it for a
// token that came from anywhere else would save a new token that is then ignored. The sources
// are produced by the real loader, not spelled here, so a change to how the loader names them
// fails this test rather than silently disabling the login advice.
func TestUnauthorizedAdviceNamesTheRemedyForTheTokenSource(t *testing.T) {
	srv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized"}`, nil)

	refuse := func(t *testing.T, cfg *config.ClientConfig) string {
		t.Helper()
		cfg.ServerURL = srv.URL
		_, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure == nil {
			t.Fatal("expected a failure for HTTP 401")
		}
		return adviceText(failure)
	}

	t.Run("default token file: login fixes it", func(t *testing.T) {
		home := isolateClientTokenEnvironment(t)
		tokenPath := filepath.Join(home, ".lfr-tunnel", "token")
		if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tokenPath, []byte("lft_pat_expired\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadClientConfig(tokenlessConfigFile(t, home))
		if err != nil {
			t.Fatal(err)
		}

		got := refuse(t, cfg)
		if !strings.Contains(got, loginCommand) {
			t.Errorf("a token from %s is replaced by %q, which the advice did not say:\n%s", tokenPath, loginCommand, got)
		}
		if !strings.Contains(got, tokenPath) {
			t.Errorf("the advice must say where the rejected token came from (%s):\n%s", tokenPath, got)
		}
	})

	t.Run("environment variable: login would be ignored", func(t *testing.T) {
		home := isolateClientTokenEnvironment(t)
		t.Setenv("LFT_TOKEN", "lft_pat_expired")
		cfg, err := config.LoadClientConfig(tokenlessConfigFile(t, home))
		if err != nil {
			t.Fatal(err)
		}

		got := refuse(t, cfg)
		if strings.Contains(got, loginCommand) {
			t.Errorf("LFT_TOKEN outranks the file login writes, so advising login is wrong:\n%s", got)
		}
		if !strings.Contains(got, "LFT_TOKEN environment variable") {
			t.Errorf("the advice must name the environment variable the token came from:\n%s", got)
		}
	})

	// LDM's credentials file is read only when ~/.lfr-tunnel/token supplied nothing, so the
	// token login writes there outranks it: login is the right advice here too.
	t.Run("LDM credentials file: login fixes it", func(t *testing.T) {
		home := isolateClientTokenEnvironment(t)
		secrets := filepath.Join(home, ".config", "lfr", "secrets")
		if err := os.MkdirAll(filepath.Dir(secrets), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(secrets, []byte("LFT_TOKEN=lft_pat_expired\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadClientConfig(tokenlessConfigFile(t, home))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(cfg.TokenSource, config.TokenSourceLDMFile) {
			t.Fatalf("the fixture did not exercise the LDM source; TokenSource = %q", cfg.TokenSource)
		}

		got := refuse(t, cfg)
		if !strings.Contains(got, loginCommand) {
			t.Errorf("login outranks the LDM credentials file, so it fixes this token, which the advice did not say:\n%s", got)
		}
	})

	// BOUNDING: the configured token_file: key differs from the default file by one character
	// in its source name ("token_file (" against "token file ("). Login does not write a
	// configured path, so this must stay on the "replace it there" branch.
	t.Run("configured token_file: login would be ignored", func(t *testing.T) {
		home := isolateClientTokenEnvironment(t)
		tokenPath := filepath.Join(home, "elsewhere-token")
		if err := os.WriteFile(tokenPath, []byte("lft_pat_expired\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(home, "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("token_file: "+tokenPath+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadClientConfig(cfgPath)
		if err != nil {
			t.Fatal(err)
		}

		got := refuse(t, cfg)
		if strings.Contains(got, loginCommand) {
			t.Errorf("a configured token_file: is not where login writes, so advising login is wrong:\n%s", got)
		}
		if !strings.Contains(got, tokenPath) {
			t.Errorf("the advice must say where the rejected token came from (%s):\n%s", tokenPath, got)
		}
	})
}

func TestMissingTokenNeverContactsTheGateway(t *testing.T) {
	t.Run("no token anywhere", func(t *testing.T) {
		isolateClientTokenEnvironment(t)
		var hits int32
		srv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized"}`, &hits)

		_, failure := attemptRegistration(&config.ClientConfig{ServerURL: srv.URL}, nil, "sub", nil)
		if failure == nil {
			t.Fatal("expected a failure with no token")
		}
		if got := atomic.LoadInt32(&hits); got != 0 {
			t.Errorf("an empty token can never register, so the gateway must not be asked; got %d requests", got)
		}
		if !failure.terminal {
			t.Error("a missing token must be terminal")
		}
		if !strings.Contains(adviceText(failure), loginCommand) {
			t.Errorf("the advice must say how to get a token:\n%s", adviceText(failure))
		}
	})

	// LFT_TOKEN_FILE replaces the default path, so the file login writes would never be read.
	t.Run("LFT_TOKEN_FILE pointing at nothing", func(t *testing.T) {
		home := isolateClientTokenEnvironment(t)
		missing := filepath.Join(home, "no-such-token")
		t.Setenv("LFT_TOKEN_FILE", missing)

		_, failure := attemptRegistration(&config.ClientConfig{ServerURL: "https://gateway.invalid"}, nil, "sub", nil)
		if failure == nil {
			t.Fatal("expected a failure with no token")
		}
		if !strings.Contains(adviceText(failure), missing) {
			t.Errorf("the advice must name the LFT_TOKEN_FILE path that held no token (%s):\n%s", missing, adviceText(failure))
		}
	})
}

// The failure reported to the user: an expired token met by failover used to be retried on
// every region and end "Failover exhausted every candidate region" -- an outage, to anyone
// reading it.
func TestReregisterAcrossRegionsStopsOnUnauthorized(t *testing.T) {
	resetCooldowns(t)
	shortenBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized"}`, &hits)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"a": srv.URL, "b": srv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
	_, ok, refused := reregisterAcrossRegions(cfg, nil, "sub", nil)
	if ok {
		t.Fatal("a 401 cannot succeed on another region")
	}
	if !refused {
		t.Error("a 401 must be reported as refused, not as every region exhausted")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected one attempt before stopping, got %d", got)
	}
}

// A pinned client used to retry a 401 every 30s for as long as it ran.
func TestReregisterSameGatewayStopsOnUnauthorized(t *testing.T) {
	shortenSameGatewayBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized"}`, &hits)

	// Bounded, so the pre-fix behaviour -- retrying forever -- fails this test rather than
	// hanging it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, ok := reregisterSameGateway(ctx, pinnedConfig(srv.URL), nil, "sub", nil); ok {
		t.Fatal("a 401 cannot succeed by retrying")
	}
	if ctx.Err() != nil {
		t.Fatal("the reconnect retried a 401 until the deadline instead of stopping")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected one attempt before stopping, got %d", got)
	}
}

// An edge relays central's refusal of its OWN credential with the same 401 status (#2342 review).
// That is an edge misconfigured mid-rotation, not the user's token: another region can serve
// them, and telling them to replace a working token would be the outage-as-credential mirror
// image of the defect this issue fixed.
func TestEdgeAuthRefusalIsNotATokenProblem(t *testing.T) {
	for _, body := range []string{
		`{"status":"error","error":"invalid edge token"}`,
		`{"status":"error","error":"missing edge token"}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := registrationServer(t, http.StatusUnauthorized, body, nil)

			cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t", TokenSource: "token file (/x)"}
			_, failure := attemptRegistration(cfg, nil, "sub", nil)
			if failure == nil {
				t.Fatal("expected a failure")
			}
			if failure.terminal {
				t.Error("an edge's own credential failure must stay retryable on another region")
			}
			if strings.Contains(adviceText(failure), "access token") || strings.Contains(adviceText(failure), loginCommand) {
				t.Errorf("an edge's credential failure was reported as the user's token:\n%s", adviceText(failure))
			}
		})
	}
}

// The same, end to end through failover: the misconfigured edge is set aside and the user lands
// on a region that can serve them.
func TestFailoverMovesPastAnEdgeAuthRefusal(t *testing.T) {
	resetCooldowns(t)
	shortenBackoff(t)

	var badHits int32
	badSrv := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"invalid edge token"}`, &badHits)
	goodSrv := registrationServer(t, http.StatusOK, `{"status":"success","session_token":"tok","subdomain_prefix":"sub"}`, nil)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	// Only the bad edge is advertised on the first election, so it is certainly tried; the
	// election is by latency and would otherwise be free to skip it, passing this vacuously.
	var fetches int32
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		if atomic.AddInt32(&fetches, 1) == 1 {
			c.Regions = map[string]string{"edge": badSrv.URL}
			return
		}
		c.Regions = map[string]string{"edge": badSrv.URL, "central": goodSrv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cfg := &config.ClientConfig{ServerURL: badSrv.URL, AuthToken: "t"}
	resp, ok, refused := reregisterAcrossRegions(cfg, nil, "sub", nil)
	if got := atomic.LoadInt32(&badHits); got != 1 {
		t.Fatalf("the misconfigured edge must be tried once and then set aside, got %d attempts", got)
	}
	if !ok {
		t.Fatalf("expected failover to reach the healthy region; refused=%v", refused)
	}
	if resp.SessionToken != "tok" {
		t.Errorf("expected the healthy region's session, got %+v", resp)
	}
}
