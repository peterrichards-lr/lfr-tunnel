package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"lfr-tunnel/pkg/config"
)

// `lfr-tunnel check-subdomain` against a stub gateway (#2562). The stub is the client's view of
// the endpoint; the server's own behaviour is held by pkg/server/check_subdomain_test.go.

const checkTestToken = "lfr_pat_resolved_token_do_not_print"

// availableWord matches "available" as a status, and not inside "unavailable".
var availableWord = regexp.MustCompile(`(^|\s)available(\s|$)`)

type fakeGateway struct {
	mu       sync.Mutex
	domains  []string // supported_domains; nil omits the field, as an older gateway does
	answer   func(w http.ResponseWriter, r *http.Request)
	auths    []string
	queries  []string
	versions int
}

func (g *fakeGateway) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			g.mu.Lock()
			g.versions++
			g.mu.Unlock()
			body := map[string]any{"server_version": "v0.0.0-test"}
			if g.domains != nil {
				body["supported_domains"] = g.domains
			}
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Errorf("encode version: %v", err)
			}
		case "/api/check-subdomain":
			g.mu.Lock()
			g.auths = append(g.auths, r.Header.Get("Authorization"))
			g.queries = append(g.queries, r.URL.RawQuery)
			g.mu.Unlock()
			g.answer(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func checkCfg(serverURL string) *config.ClientConfig {
	return &config.ClientConfig{
		ServerURL:   serverURL,
		AuthToken:   checkTestToken,
		TokenSource: "LFT_TOKEN environment variable",
	}
}

func runCheck(t *testing.T, cfg *config.ClientConfig, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCheckSubdomain(args, cfg, &stdout, &stderr)
	out, errOut := stdout.String(), stderr.String()
	// Whatever the outcome, the token never reaches the terminal.
	if strings.Contains(out+errOut, checkTestToken) {
		t.Errorf("the token was printed:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	return code, out, errOut
}

// The per-domain answer, the way the fixed gateway gives it: about the domain asked, and saying so.
func perDomain(t *testing.T, taken map[string][]string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		sub, dom := r.URL.Query().Get("subdomain"), r.URL.Query().Get("domain")
		if suggestions, isTaken := taken[dom]; isTaken {
			writeJSON(t, w, http.StatusOK, map[string]any{
				"available": false, "subdomain": sub, "domain": dom,
				"reason": "subdomain is already taken", "suggestions": suggestions,
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"available": true, "subdomain": sub, "domain": dom})
	}
}

func TestCheckSubdomain_Available(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = perDomain(t, nil)
	srv := g.start(t)

	code, out, _ := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainAvailable {
		t.Errorf("exit %d, want %d", code, checkSubdomainAvailable)
	}
	if !regexp.MustCompile(`(?m)^acme-poc\.lfr-demo\.se\s+available$`).MatchString(out) {
		t.Errorf("output does not report acme-poc.lfr-demo.se available:\n%s", out)
	}
}

func TestCheckSubdomain_TakenWithSuggestions(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = perDomain(t, map[string][]string{"lfr-demo.se": {"acme-poc-dev", "acme-poc-app"}})
	srv := g.start(t)

	code, out, _ := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainTaken {
		t.Errorf("exit %d, want %d", code, checkSubdomainTaken)
	}
	if !regexp.MustCompile(`(?m)^acme-poc\.lfr-demo\.se\s+unavailable -- subdomain is already taken$`).MatchString(out) {
		t.Errorf("output does not report the name taken:\n%s", out)
	}
	if !strings.Contains(out, "Suggestions on lfr-demo.se: acme-poc-dev, acme-poc-app") {
		t.Errorf("suggestions missing:\n%s", out)
	}
	if availableWord.MatchString(out) {
		t.Errorf("a taken name was reported available:\n%s", out)
	}
}

func TestCheckSubdomain_EveryDomainIsAskedAboutByName(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se", "lfr-demo.online"}}
	g.answer = perDomain(t, map[string][]string{"lfr-demo.se": {"acme-poc-2"}})
	srv := g.start(t)

	code, out, _ := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainAvailable {
		t.Errorf("exit %d, want %d (available on one domain)", code, checkSubdomainAvailable)
	}
	for _, want := range []string{
		`(?m)^acme-poc\.lfr-demo\.se\s+unavailable -- subdomain is already taken$`,
		`(?m)^acme-poc\.lfr-demo\.online\s+available$`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("output does not match %s:\n%s", want, out)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.queries) != 2 ||
		!strings.Contains(g.queries[0], "domain=lfr-demo.se") ||
		!strings.Contains(g.queries[1], "domain=lfr-demo.online") {
		t.Errorf("each domain must be asked about by name; queries were %v", g.queries)
	}
}

func TestCheckSubdomain_TokenRefused(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	srv := g.start(t)

	code, out, errOut := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainError {
		t.Errorf("exit %d, want %d", code, checkSubdomainError)
	}
	if !strings.Contains(errOut, "did not accept this access token") {
		t.Errorf("stderr does not say the token was refused:\n%s", errOut)
	}
	if !strings.Contains(errOut, "The token was read from: LFT_TOKEN environment variable") {
		t.Errorf("stderr does not say where the token was read from:\n%s", errOut)
	}
	if availableWord.MatchString(out + errOut) {
		t.Errorf("a refused token was reported as available:\n%s%s", out, errOut)
	}
}

func TestCheckSubdomain_GatewayErrorIsNeverAvailable(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se", "lfr-demo.online"}}
	g.answer = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("domain") == "lfr-demo.online" {
			writeJSON(t, w, http.StatusServiceUnavailable, map[string]string{"error": "the gateway could not check subdomain reservations just now; retry in a moment"})
			return
		}
		perDomain(t, nil)(w, r)
	}
	srv := g.start(t)

	code, out, errOut := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainError {
		t.Errorf("exit %d, want %d: one domain could not be checked", code, checkSubdomainError)
	}
	if !regexp.MustCompile(`(?m)^acme-poc\.lfr-demo\.online\s+unknown -- the gateway answered HTTP 503: .*reservations`).MatchString(out) {
		t.Errorf("the unchecked domain is not reported unknown:\n%s", out)
	}
	if regexp.MustCompile(`(?m)^acme-poc\.lfr-demo\.online\s+available`).MatchString(out) {
		t.Errorf("a 503 was reported as available:\n%s", out)
	}
	if !strings.Contains(errOut, "could not be checked") {
		t.Errorf("stderr does not say a domain went unchecked:\n%s", errOut)
	}
}

func TestCheckSubdomain_Unreachable(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = perDomain(t, nil)
	srv := g.start(t)
	url := srv.URL
	srv.Close()

	code, out, errOut := runCheck(t, checkCfg(url), "acme-poc")
	if code != checkSubdomainError {
		t.Errorf("exit %d, want %d", code, checkSubdomainError)
	}
	if !strings.Contains(errOut, "could not reach the gateway at "+url) {
		t.Errorf("stderr does not say the gateway was unreachable:\n%s", errOut)
	}
	if availableWord.MatchString(out + errOut) {
		t.Errorf("an unreachable gateway was reported as available:\n%s%s", out, errOut)
	}
}

func TestCheckSubdomain_TokenIsSentFromTheResolvedSourceOnly(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se", "lfr-demo.online"}}
	g.answer = perDomain(t, nil)
	srv := g.start(t)

	if code, _, _ := runCheck(t, checkCfg(srv.URL), "acme-poc"); code != checkSubdomainAvailable {
		t.Fatalf("exit %d", code)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.auths) != 2 {
		t.Fatalf("expected one request per domain, got %d", len(g.auths))
	}
	for i, auth := range g.auths {
		if auth != "Bearer "+checkTestToken {
			t.Errorf("request %d: Authorization %q, want the resolved token as a bearer token", i, auth)
		}
		// Never in the URL, which is what proxies and access logs record.
		if strings.Contains(g.queries[i], checkTestToken) || strings.Contains(g.queries[i], "token=") {
			t.Errorf("request %d: token in the query string: %s", i, g.queries[i])
		}
	}
}

func TestCheckSubdomain_TokenFlagOverridesTheResolvedToken(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	srv := g.start(t)

	code, _, errOut := runCheck(t, checkCfg(srv.URL), "acme-poc", "-token", "lfr_pat_from_flag")
	if code != checkSubdomainError {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(errOut, "The token was read from: -token flag") {
		t.Errorf("the refusal does not name the -token flag as the source:\n%s", errOut)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.auths) != 1 || g.auths[0] != "Bearer lfr_pat_from_flag" {
		t.Errorf("Authorization %v, want the -token value", g.auths)
	}
}

func TestCheckSubdomain_NoTokenAsksNobody(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = perDomain(t, nil)
	srv := g.start(t)
	cfg := checkCfg(srv.URL)
	cfg.AuthToken, cfg.TokenSource = "", ""

	code, out, errOut := runCheck(t, cfg, "acme-poc")
	if code != checkSubdomainError {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(errOut, "No access token was found") {
		t.Errorf("stderr:\n%s", errOut)
	}
	if availableWord.MatchString(out) {
		t.Errorf("reported available with no token:\n%s", out)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.versions != 0 || len(g.auths) != 0 {
		t.Errorf("the gateway was contacted with no token to send")
	}
}

func TestCheckSubdomain_AnEdgeIsNamedNotTheToken(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se"}}
	g.answer = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusMisdirectedRequest, map[string]string{
			"error":         "This gateway is a regional edge and does not hold the data for /api/check-subdomain.",
			"control_plane": "https://tunnel.lfr-demo.se",
		})
	}
	srv := g.start(t)

	code, _, errOut := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainError {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(errOut, "regional edge") || !strings.Contains(errOut, "https://tunnel.lfr-demo.se") {
		t.Errorf("stderr does not name the edge and the control plane:\n%s", errOut)
	}
	if strings.Contains(errOut, "did not accept") {
		t.Errorf("an edge's 421 was reported as a refused token:\n%s", errOut)
	}
}

// A gateway older than #2562 ignores `domain` and does not echo one. Its answer must not be
// printed under a domain label it was never about.
func TestCheckSubdomain_OlderGatewayIsNotLabelledWithADomain(t *testing.T) {
	g := &fakeGateway{domains: []string{"lfr-demo.se", "lfr-demo.online"}}
	g.answer = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"available": true, "subdomain": r.URL.Query().Get("subdomain")})
	}
	srv := g.start(t)

	code, out, errOut := runCheck(t, checkCfg(srv.URL), "acme-poc")
	if code != checkSubdomainAvailable {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(out, "acme-poc.lfr-demo") {
		t.Errorf("an answer about an unnamed domain was labelled with one:\n%s", out)
	}
	if !strings.Contains(errOut, "did not say which domain it checked") {
		t.Errorf("no note about the older gateway:\n%s", errOut)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.queries) != 1 {
		t.Errorf("asked %d times; the same answer would only repeat under other labels", len(g.queries))
	}
}

func TestCheckSubdomain_Usage(t *testing.T) {
	cfg := checkCfg("http://127.0.0.1:1")
	for _, args := range [][]string{nil, {"a", "b"}, {""}} {
		if code, _, _ := runCheck(t, cfg, args...); code != checkSubdomainError {
			t.Errorf("args %q: exit %d, want %d", args, code, checkSubdomainError)
		}
	}
}
