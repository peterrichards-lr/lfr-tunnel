package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir reads USERPROFILE on Windows; the HOME override does not apply")
	}
	for _, k := range []string{"LFT_CLIENT_TOKEN", "LFT_TOKEN", "LFT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
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
// a token exists. The client must not undo that by varying its advice on whatever the response
// says -- so two 401s that differ in everything the gateway controls must read identically.
func TestUnauthorizedAdviceDoesNotDependOnTheRefusal(t *testing.T) {
	a := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"unauthorized","portal_url":"https://portal.example"}`, nil)
	b := registrationServer(t, http.StatusUnauthorized, `{"status":"error","error":"token expired","portal_url":"https://portal.example"}`, nil)

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
	if strings.Contains(adviceText(fb), "token expired") {
		t.Error("the advice echoed the gateway's reason; it must name every cause, never a diagnosed one")
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
		cfg, err := config.LoadClientConfig("")
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
		isolateClientTokenEnvironment(t)
		t.Setenv("LFT_TOKEN", "lft_pat_expired")
		cfg, err := config.LoadClientConfig("")
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
