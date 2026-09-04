package main

import (
	"errors"
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPIDManagement(t *testing.T) {
	sub := "test-subdomain"

	err := writePID(sub, 12345)
	if err != nil {
		t.Fatalf("Failed to write PID: %v", err)
	}

	pid, err := readPID(sub)
	if err != nil {
		t.Fatalf("Failed to read PID: %v", err)
	}
	if pid != 12345 {
		t.Errorf("Expected PID 12345, got %d", pid)
	}

	subs, err := getActiveSubdomains()
	if err != nil {
		t.Fatalf("Failed to get active subdomains: %v", err)
	}
	found := false
	for _, s := range subs {
		if s == sub {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find subdomain %s", sub)
	}

	// Clean up
	path, _err := getPIDFilePath(sub)
	_ = _err            //nolint:errcheck
	_ = os.Remove(path) //nolint:errcheck
}

func TestIsPIDRunning(t *testing.T) {
	// Current process is definitely running
	if !isPIDRunning(os.Getpid()) {
		t.Errorf("Current PID should be running")
	}

	// Large unlikely PID
	if isPIDRunning(9999999) {
		t.Errorf("Unlikely PID should not be running")
	}
}

func TestArrayFlags(t *testing.T) {
	var a arrayFlags
	_ = a.Set("foo") //nolint:errcheck
	_ = a.Set("bar") //nolint:errcheck
	if a.String() != "foo, bar" {
		t.Errorf("Expected 'foo, bar', got %s", a.String())
	}
}

func TestProbeFastestRegion(t *testing.T) {
	regions := map[string]string{
		"local": "http://127.0.0.1:0", // won't connect, will fail
	}
	// It will return an empty string or whatever is fastest (none in this case, meaning default/error)
	// We just want to ensure it doesn't panic
	_, _ = probeFastestRegion(regions) //nolint:errcheck
}

// newHealthyEdge returns a stand-in edge node that answers probeFastestRegion's
// health check, so region election is decided locally rather than by whichever
// real edges happen to be powered up.
func newHealthyEdge(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveServerURL_OfflineRegionFallback(t *testing.T) {
	usSrv := newHealthyEdge(t)
	euSrv := newHealthyEdge(t)

	// resolveServerURL otherwise reaches production: fetchRemoteRegions replaces
	// cfg.Regions with the live region list (which would put 'apac' back and defeat
	// the fixture), and saveRegionCache writes to the real ~/.lfr-tunnel. Stub both.
	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(*config.ClientConfig) {}
	var cachedRegion, cachedURL string
	saveRegionCacheFn = func(bestRegion, serverURL string, _ bool, _ []string) {
		cachedRegion, cachedURL = bestRegion, serverURL
	}

	cfg := &config.ClientConfig{
		Region:    "apac",
		ServerURL: "https://tunnel.invalid",
		Regions: map[string]string{
			"us": usSrv.URL,
			"eu": euSrv.URL,
		},
	}

	resolveServerURL(cfg, false)

	// 'apac' is absent from Regions, standing in for an offline edge, so resolution
	// must fall back to one of the two reachable regions.
	if cfg.Region == "apac" {
		t.Fatalf("Expected region failover from offline 'apac', but cfg.Region was still 'apac'")
	}
	if cfg.Region != "us" && cfg.Region != "eu" {
		t.Fatalf("Expected failover to 'us' or 'eu', got %q", cfg.Region)
	}
	if want := cfg.Regions[cfg.Region]; cfg.ServerURL != want {
		t.Errorf("Expected ServerURL %q for elected region %q, got %q", want, cfg.Region, cfg.ServerURL)
	}
	// The elected region must also be the one persisted, or the next client start
	// reads back a region that disagrees with the one actually in use.
	if cachedRegion != cfg.Region || cachedURL != cfg.ServerURL {
		t.Errorf("Expected region cache to record %q/%q, got %q/%q",
			cfg.Region, cfg.ServerURL, cachedRegion, cachedURL)
	}
}

// registrationServer stands in for a gateway. It answers /api/healthz with 200 so
// probeFastestRegion will elect it, and returns the given status and body for
// /api/register, counting the registration attempts it receives.
func registrationServer(t *testing.T, status int, body string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

// shortenBackoff removes the real retry pauses so failover tests stay fast.
func shortenBackoff(t *testing.T) {
	t.Helper()
	orig := failoverRetryBackoff
	failoverRetryBackoff = time.Millisecond
	t.Cleanup(func() { failoverRetryBackoff = orig })
}

// resetCooldowns clears failover cooldown state so tests don't leak into each other
// through the package-level tracker.
func resetCooldowns(t *testing.T) {
	t.Helper()
	cooldowns.mu.Lock()
	cooldowns.until = make(map[string]time.Time)
	cooldowns.mu.Unlock()
	t.Cleanup(func() {
		cooldowns.mu.Lock()
		cooldowns.until = make(map[string]time.Time)
		cooldowns.mu.Unlock()
	})
}

// TestRegionCooldownSurvivesRemoteRefresh is the regression test for #1121: a region
// put into cooldown must stay excluded even though resolveServerURL refreshes
// cfg.Regions from the gateway, which previously restored the entry and let the client
// re-elect the region it had just failed away from.
func TestRegionCooldownSurvivesRemoteRefresh(t *testing.T) {
	resetCooldowns(t)
	failedSrv := newHealthyEdge(t)
	goodSrv := newHealthyEdge(t)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	// Stand in for the live gateway: always re-advertises both regions, including the
	// failed one. This is exactly the behaviour that defeated the old delete().
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"apac": failedSrv.URL, "eu": goodSrv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cooldowns.exclude(failedSrv.URL, regionFailoverCooldown)

	cfg := &config.ClientConfig{Region: "", ServerURL: failedSrv.URL}
	resolveServerURL(cfg, false)

	if cfg.Region == "apac" {
		t.Fatalf("region in cooldown was re-elected; cooldown did not survive the region refresh")
	}
	if cfg.Region != "eu" {
		t.Fatalf("expected election of 'eu', got %q", cfg.Region)
	}
	if cfg.ServerURL != goodSrv.URL {
		t.Errorf("expected ServerURL %q, got %q", goodSrv.URL, cfg.ServerURL)
	}
}

// TestRegionCooldownFallsBackWhenAllExcluded checks that excluding every region does
// not leave the client with nowhere to go -- retrying a recently-failed region beats
// having no gateway at all.
func TestRegionCooldownFallsBackWhenAllExcluded(t *testing.T) {
	resetCooldowns(t)
	onlySrv := newHealthyEdge(t)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"eu": onlySrv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cooldowns.exclude(onlySrv.URL, regionFailoverCooldown)

	cfg := &config.ClientConfig{Region: "", ServerURL: onlySrv.URL}
	resolveServerURL(cfg, false)

	if cfg.Region != "eu" {
		t.Fatalf("expected fallback to the only region even though it is in cooldown, got %q", cfg.Region)
	}
}

// TestRegionCooldownExpires confirms a cooldown is not permanent.
func TestRegionCooldownExpires(t *testing.T) {
	resetCooldowns(t)
	regions := map[string]string{"apac": "https://apac.invalid", "eu": "https://eu.invalid"}

	cooldowns.exclude("https://apac.invalid", 1*time.Millisecond)
	if got := cooldowns.filter(regions); len(got) != 1 {
		t.Fatalf("expected 'apac' filtered out while in cooldown, got %v", got)
	}

	time.Sleep(5 * time.Millisecond)
	if got := cooldowns.filter(regions); len(got) != 2 {
		t.Errorf("expected cooldown to expire and both regions to return, got %v", got)
	}
}

// TestRegionCooldownClear covers the successful-reconnect path clearing prior state.
func TestRegionCooldownClear(t *testing.T) {
	resetCooldowns(t)
	regions := map[string]string{"apac": "https://apac.invalid", "eu": "https://eu.invalid"}

	cooldowns.exclude("https://APAC.invalid", regionFailoverCooldown)
	if _, present := cooldowns.filter(regions)["apac"]; present {
		t.Fatalf("exclude should be case-insensitive on the host")
	}

	cooldowns.clear("https://apac.invalid")
	if _, present := cooldowns.filter(regions)["apac"]; !present {
		t.Errorf("clear should return the region to the candidate set")
	}
}

// TestRegionDiscoveryURLAvoidsFailedRegion covers #1121's second half: the region list
// must not be refetched from the edge we are trying to exclude.
func TestRegionDiscoveryURLAvoidsFailedRegion(t *testing.T) {
	cfg := &config.ClientConfig{
		Regions: map[string]string{
			"apac":    "https://apac.example",
			"central": "https://central.example",
		},
	}

	if got := regionDiscoveryURL(cfg, nil, "https://apac.example"); got != "https://central.example" {
		t.Errorf("expected the central control plane to be preferred, got %q", got)
	}

	// With central itself the failed region, any other region will do.
	cfg2 := &config.ClientConfig{Regions: map[string]string{"central": "https://central.example"}}
	if got := regionDiscoveryURL(cfg2, nil, "https://central.example"); got != "" {
		t.Errorf("expected no discovery URL when only the failed region is known, got %q", got)
	}

	// Falls back to the primary map when cfg has been emptied.
	cfg3 := &config.ClientConfig{}
	primary := map[string]string{"eu": "https://eu.example"}
	if got := regionDiscoveryURL(cfg3, primary, "https://apac.example"); got != "https://eu.example" {
		t.Errorf("expected fallback to the primary region map, got %q", got)
	}
}

// TestExcludeFailedRegionSkipsAfterFailback is the regression test for #1137. A failback
// that fails restores the region the client was already serving from and logs that it is
// staying there -- but execution then fell into the failover path, which cooled that
// region down as though it were the casualty, abandoning a healthy edge.
func TestExcludeFailedRegionSkipsAfterFailback(t *testing.T) {
	resetCooldowns(t)

	newCfg := func() *config.ClientConfig {
		return &config.ClientConfig{
			Region:    "eu",
			ServerURL: "https://eu.example",
			Regions: map[string]string{
				"eu":      "https://eu.example",
				"central": "https://central.example",
			},
		}
	}

	// After a failed failback: no cooldown, and discovery stays where it is.
	cfg := newCfg()
	excludeFailedRegion(cfg, nil, "https://eu.example", true, false)
	if _, present := cooldowns.filter(cfg.Regions)["eu"]; !present {
		t.Error("the region returned to after a failed failback must stay a candidate; cooling it down abandons a healthy edge")
	}
	if cfg.ServerURL != "https://eu.example" {
		t.Errorf("discovery should not move off a region that did not fail, got %q", cfg.ServerURL)
	}

	// A genuine connection loss still cools the region down and moves discovery.
	resetCooldowns(t)
	cfg = newCfg()
	excludeFailedRegion(cfg, nil, "https://eu.example", false, false)
	if _, present := cooldowns.filter(cfg.Regions)["eu"]; present {
		t.Error("a region whose connection was lost must be excluded from the candidate set")
	}
	if cfg.ServerURL != "https://central.example" {
		t.Errorf("discovery should move to central after a real failure, got %q", cfg.ServerURL)
	}
}

// TestExcludeFailedRegionAfterFailbackKeepsTwoRegionCaseSane covers the sharpest edge of
// #1137. With two regions, cooling down both the primary (by the failed failback) and the
// fallback (by the failover path) empties the candidate set, and regionCooldowns.filter
// then returns the unfiltered map -- so the client could re-elect the primary whose
// failback had just failed, which is the loop #1121 exists to prevent.
func TestExcludeFailedRegionAfterFailbackKeepsTwoRegionCaseSane(t *testing.T) {
	resetCooldowns(t)

	cfg := &config.ClientConfig{
		Region:    "eu",
		ServerURL: "https://eu.example",
		Regions:   map[string]string{"eu": "https://eu.example", "apac": "https://apac.example"},
	}

	// The failed failback cools the primary, as it should.
	cooldowns.exclude("https://apac.example", regionFailoverCooldown)
	excludeFailedRegion(cfg, nil, "https://eu.example", true, false)

	candidates := cooldowns.filter(cfg.Regions)
	if _, present := candidates["apac"]; present {
		t.Fatal("the primary whose failback failed must stay excluded")
	}
	if _, present := candidates["eu"]; !present {
		t.Fatal("expected 'eu' to remain a candidate")
	}
	if len(candidates) != 1 {
		t.Errorf("expected exactly one candidate, got %v -- an empty set would trip filter's unfiltered fallback", candidates)
	}
}

// TestAttemptRegistrationClassifiesFailures is the regression test for #1120: the
// handshake must report failures instead of terminating the process, and must mark a
// 403 as terminal so failover does not pointlessly retry it against every region.
func TestAttemptRegistrationClassifiesFailures(t *testing.T) {
	t.Run("gateway 5xx is retryable", func(t *testing.T) {
		srv := registrationServer(t, http.StatusBadGateway, `{}`, nil)

		cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
		resp, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure == nil {
			t.Fatalf("expected a failure for HTTP 502, got response %+v", resp)
		}
		if failure.terminal {
			t.Errorf("a gateway 5xx must be retryable on another region, got terminal=true")
		}
	})

	t.Run("undecodable gateway error is retryable", func(t *testing.T) {
		// A 502 from a reverse proxy in front of the gateway returns an HTML error
		// page, not JSON. RegisterTunnel fails at the decode step before it ever looks
		// at the status code, producing an error that matches none of the gateway
		// patterns -- which is precisely the case that used to reach log.Fatalf.
		srv := registrationServer(t, http.StatusBadGateway, `<html>502 Bad Gateway</html>`, nil)

		cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
		_, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure == nil {
			t.Fatalf("expected a failure for an undecodable 502 body")
		}
		if failure.terminal {
			t.Errorf("an undecodable gateway response must stay retryable, got terminal=true")
		}
	})

	t.Run("403 is terminal", func(t *testing.T) {
		srv := registrationServer(t, http.StatusForbidden, `{"error":"subdomain reserved by another user"}`, nil)

		cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
		_, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure == nil {
			t.Fatalf("expected a failure for HTTP 403")
		}
		if !failure.terminal {
			t.Errorf("a 403 reservation/limit rejection must be terminal, got terminal=false")
		}
		if len(failure.advice) == 0 {
			t.Errorf("expected portal guidance to be attached to a 403 failure")
		}
	})

	t.Run("success", func(t *testing.T) {
		srv := registrationServer(t, http.StatusOK, `{"status":"success","session_token":"tok","subdomain_prefix":"sub"}`, nil)

		cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
		resp, failure := attemptRegistration(cfg, nil, "sub", nil)
		if failure != nil {
			t.Fatalf("expected success, got failure %v", failure.err)
		}
		if resp.SessionToken != "tok" {
			t.Errorf("expected session token 'tok', got %q", resp.SessionToken)
		}
	})
}

// TestReregisterAcrossRegionsMovesOn checks that a region whose registration fails is
// abandoned for a different one, rather than the same broken gateway being retried
// until the attempt budget runs out.
func TestReregisterAcrossRegionsMovesOn(t *testing.T) {
	resetCooldowns(t)
	shortenBackoff(t)

	var badHits, goodHits int32
	badSrv := registrationServer(t, http.StatusBadGateway, `{}`, &badHits)
	goodSrv := registrationServer(t, http.StatusOK,
		`{"status":"success","session_token":"tok","subdomain_prefix":"sub"}`, &goodHits)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"bad": badSrv.URL, "good": goodSrv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cfg := &config.ClientConfig{ServerURL: badSrv.URL, AuthToken: "t"}
	resp, ok := reregisterAcrossRegions(cfg, nil, "sub", nil)
	if !ok {
		t.Fatalf("expected registration to succeed on the healthy region")
	}
	if resp.SessionToken != "tok" {
		t.Errorf("expected the healthy region's response, got %+v", resp)
	}
	if cfg.Region != "good" {
		t.Errorf("expected to end up on 'good', got %q", cfg.Region)
	}
	// Whichever region was elected first, the broken one must never be retried after
	// failing -- one attempt at most, then it is cooled down and skipped.
	if got := atomic.LoadInt32(&badHits); got > 1 {
		t.Errorf("broken region should be tried at most once then cooled down, got %d attempts", got)
	}
}

// TestReregisterAcrossRegionsStopsOnTerminal makes sure a 403 is not retried against
// every remaining region, since none of them can satisfy it.
func TestReregisterAcrossRegionsStopsOnTerminal(t *testing.T) {
	resetCooldowns(t)
	shortenBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusForbidden, `{"error":"subdomain reserved"}`, &hits)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"a": srv.URL, "b": srv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
	if _, ok := reregisterAcrossRegions(cfg, nil, "sub", nil); ok {
		t.Fatalf("expected a terminal 403 to abort the failover")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected a terminal failure to stop after one attempt, got %d", got)
	}
}

// TestReregisterAcrossRegionsGivesUpCleanly covers the total-outage case: every region
// failing must return false rather than exiting the process or looping forever.
func TestReregisterAcrossRegionsGivesUpCleanly(t *testing.T) {
	resetCooldowns(t)
	shortenBackoff(t)

	var hits int32
	srv := registrationServer(t, http.StatusBadGateway, `{}`, &hits)

	origFetch, origSave := fetchRemoteRegionsFn, saveRegionCacheFn
	defer func() { fetchRemoteRegionsFn, saveRegionCacheFn = origFetch, origSave }()
	fetchRemoteRegionsFn = func(c *config.ClientConfig) {
		c.Regions = map[string]string{"a": srv.URL, "b": srv.URL}
	}
	saveRegionCacheFn = func(string, string, bool, []string) {}

	cfg := &config.ClientConfig{ServerURL: srv.URL, AuthToken: "t"}
	if _, ok := reregisterAcrossRegions(cfg, nil, "sub", nil); ok {
		t.Fatalf("expected failure when every region is unhealthy")
	}
	if got := atomic.LoadInt32(&hits); got != maxFailoverAttempts {
		t.Errorf("expected exactly %d attempts before giving up, got %d", maxFailoverAttempts, got)
	}
}

func TestRewriteRemotes(t *testing.T) {
	regResp := &client.RegisterResponse{
		Remotes: []string{"60000:0.0.0.0:8080:8080"},
	}
	portMap := map[int]int{
		8080: 8080,
	}

	rewriteRemotes(regResp, portMap)
	if len(regResp.Remotes) != 1 {
		t.Errorf("Expected 1 remote")
	}
	if regResp.Remotes[0] != "60000:0.0.0.0:8080:8080" {
		t.Errorf("Expected rewritten remote, got %s", regResp.Remotes[0])
	}
}

func TestResolvePortsAndMappings(t *testing.T) {
	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()
	cfg := &config.ClientConfig{
		Ports: []int{8080},
	}

	mappings := resolvePortsAndMappings(cfg)
	if len(mappings) != 1 {
		t.Errorf("Expected 1 mapping")
	}
}

func TestOverrideConfigWithFlags(t *testing.T) {
	// Reset flags explicitly for tests
	origServer := *serverURL
	origInsecure := *insecureSkipVerify
	defer func() {
		*serverURL = origServer
		*insecureSkipVerify = origInsecure
	}()

	*serverURL = "https://test-override.com"
	*insecureSkipVerify = true

	cfg := &config.ClientConfig{
		ServerURL:          "https://default.com",
		InsecureSkipVerify: false,
	}

	overrideConfigWithFlags(cfg)

	if cfg.ServerURL != "https://test-override.com" {
		t.Errorf("Expected ServerURL override to be https://test-override.com, got %s", cfg.ServerURL)
	}
	if !cfg.InsecureSkipVerify {
		t.Errorf("Expected InsecureSkipVerify override to be true, got %v", cfg.InsecureSkipVerify)
	}
}

func TestMain_ValidationFailure(t *testing.T) {
	if os.Getenv("BE_CRASHER_VALIDATION") == "1" {
		*bandwidth = "invalid"
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMain_ValidationFailure")
	cmd.Env = append(os.Environ(), "BE_CRASHER_VALIDATION=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatalf("process ran with err %v, want exit status 1", err)
}

// The failback prober only knows the primary is answering. The cooldown knows whether we left
// it deliberately, and that is the difference between returning to a recovered gateway and
// being dragged back onto one that just told us to go (#1310).
func TestCoolingDown_ReportsADeliberateDeparture(t *testing.T) {
	resetCooldowns(t)

	primary := "http://tunnel.lfr-demo.se"
	if cooling, _ := cooldowns.coolingDown(primary); cooling {
		t.Fatal("nothing has happened yet; the primary must not be on cooldown")
	}

	// What a planned move does: excludeFailedRegion with planned=true.
	cooldowns.exclude(primary, plannedShutdownCooldown)

	cooling, remaining := cooldowns.coolingDown(primary)
	if !cooling {
		t.Fatal("a gateway we deliberately left is not reported as cooling down, so failback would return to it immediately")
	}
	if remaining <= 0 || remaining > plannedShutdownCooldown {
		t.Errorf("remaining = %v, want 0 < n <= %v", remaining, plannedShutdownCooldown)
	}
}

// Keyed on the gateway host, so the alias a region happens to be advertised under cannot let
// a client back onto the same box -- the same trap as #1166.
func TestCoolingDown_IsKeyedOnTheGatewayNotTheName(t *testing.T) {
	resetCooldowns(t)

	cooldowns.exclude("http://tunnel.lfr-demo.se", plannedShutdownCooldown)

	if cooling, _ := cooldowns.coolingDown("https://tunnel.lfr-demo.se"); !cooling {
		t.Error("the same gateway on another scheme was not recognised as cooling down")
	}
	if cooling, _ := cooldowns.coolingDown("http://in.lfr-demo.se"); cooling {
		t.Error("an unrelated gateway was reported as cooling down")
	}
}

// An expired cooldown must stop holding the client away, or a client that moved off during a
// nightly stop would never go home.
func TestCoolingDown_ExpiresAndAllowsFailback(t *testing.T) {
	resetCooldowns(t)

	primary := "http://tunnel.lfr-demo.se"
	cooldowns.exclude(primary, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if cooling, remaining := cooldowns.coolingDown(primary); cooling {
		t.Errorf("an expired cooldown still blocks failback (%v remaining)", remaining)
	}
}

// Reconnecting to a gateway clears its cooldown, so a client that returned by any route is not
// then told it may not be there.
func TestCoolingDown_ClearedOnReconnect(t *testing.T) {
	resetCooldowns(t)

	primary := "http://tunnel.lfr-demo.se"
	cooldowns.exclude(primary, plannedShutdownCooldown)
	cooldowns.clear(primary)

	if cooling, _ := cooldowns.coolingDown(primary); cooling {
		t.Error("a cooldown survived a successful reconnection to that gateway")
	}
}

// --- #1710: the discovery branches of resolvePortsAndMappings ---
//
// DefaultClientConfig used to seed Ports with []int{8080}, so cfg.Ports was never empty and
// the workspace/auto-discovery branches below could not be reached by any user who had not
// written an explicit `ports: []` into their config file. These tests pin each of the three
// cases the fix has to keep straight: explicit ports still win, an unset list reaches
// discovery, and 8080 is still what you get when discovery finds nothing.

type portDiscoverySpies struct {
	workspaceChecked int
	workspaceScanned int
	autoDiscovered   int
}

// stubPortDiscovery replaces the three discovery seams for the duration of a test and
// returns the call counters. Every seam defaults to "would fail the test if reached"
// behaviour that the caller overrides as needed.
func stubPortDiscovery(t *testing.T) *portDiscoverySpies {
	t.Helper()
	oldIs, oldDetect, oldAuto := isLiferayWorkspace, detectWorkspacePorts, autoDiscoverTarget
	t.Cleanup(func() {
		isLiferayWorkspace, detectWorkspacePorts, autoDiscoverTarget = oldIs, oldDetect, oldAuto
	})
	return &portDiscoverySpies{}
}

// TestResolvePortsAndMappings_ExplicitConfigPortsSkipDiscovery — the user who wrote
// `ports:` in their config file. Nothing about discovery may run for them.
func TestResolvePortsAndMappings_ExplicitConfigPortsSkipDiscovery(t *testing.T) {
	spies := stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { spies.workspaceChecked++; return true }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) {
		spies.workspaceScanned++
		return nil, nil
	}
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) {
		spies.autoDiscovered++
		return nil, nil
	}

	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()

	mappings := resolvePortsAndMappings(&config.ClientConfig{Ports: []int{9090, 7070}})

	if spies.workspaceChecked != 0 || spies.workspaceScanned != 0 || spies.autoDiscovered != 0 {
		t.Fatalf("explicit ports must skip discovery entirely, got %+v", *spies)
	}
	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d (%+v)", len(mappings), mappings)
	}
	if mappings[0].LocalPort != 9090 || mappings[0].NameSuffix != "" {
		t.Errorf("expected primary 9090 with no suffix, got %+v", mappings[0])
	}
	if mappings[1].LocalPort != 7070 || mappings[1].NameSuffix != "7070" {
		t.Errorf("expected secondary 7070 suffixed \"7070\", got %+v", mappings[1])
	}
}

// TestResolvePortsAndMappings_ExplicitFlagSkipsDiscovery — the -ports flag wins over an
// unset config, and still skips discovery.
func TestResolvePortsAndMappings_ExplicitFlagSkipsDiscovery(t *testing.T) {
	spies := stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { spies.workspaceChecked++; return true }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) {
		spies.workspaceScanned++
		return nil, nil
	}
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) {
		spies.autoDiscovered++
		return nil, nil
	}

	oldPortsStr := *portsStr
	*portsStr = "3000"
	defer func() { *portsStr = oldPortsStr }()

	mappings := resolvePortsAndMappings(&config.ClientConfig{})

	if spies.workspaceChecked != 0 || spies.workspaceScanned != 0 || spies.autoDiscovered != 0 {
		t.Fatalf("-ports must skip discovery entirely, got %+v", *spies)
	}
	if len(mappings) != 1 || mappings[0].LocalPort != 3000 {
		t.Fatalf("expected a single mapping on 3000, got %+v", mappings)
	}
}

// TestResolvePortsAndMappings_UnsetReachesWorkspaceScan — the bug in #1710. An unset port
// list inside a Liferay workspace must reach DetectWorkspacePorts.
func TestResolvePortsAndMappings_UnsetReachesWorkspaceScan(t *testing.T) {
	spies := stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { spies.workspaceChecked++; return true }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) {
		spies.workspaceScanned++
		return []client.PortMapping{
			{LocalPort: 8080},
			{LocalPort: 3000, NameSuffix: "my-remote-app"},
		}, nil
	}
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) {
		spies.autoDiscovered++
		return nil, nil
	}

	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()

	mappings := resolvePortsAndMappings(&config.ClientConfig{})

	if spies.workspaceScanned != 1 {
		t.Fatalf("workspace scan unreachable: DetectWorkspacePorts called %d times", spies.workspaceScanned)
	}
	if spies.autoDiscovered != 0 {
		t.Errorf("a workspace must not also run host auto-discovery")
	}
	if len(mappings) != 2 || mappings[1].NameSuffix != "my-remote-app" {
		t.Fatalf("expected the scan's mappings to be used, got %+v", mappings)
	}
}

// TestResolvePortsAndMappings_ScansARealWorkspaceOnDisk exercises the genuine
// IsLiferayWorkspace/DetectWorkspacePorts pair against real files -- no seams stubbed -- so
// that a change which reconnects the branch but breaks the scan still fails.
func TestResolvePortsAndMappings_ScansARealWorkspaceOnDisk(t *testing.T) {
	workspace := t.TempDir()
	extDir := filepath.Join(workspace, "client-extensions", "my-remote-app")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("failed to build workspace fixture: %v", err)
	}
	yaml := "my-remote-app:\n    name: My Remote App\n    type: customElement\n    port: 4001\n"
	if err := os.WriteFile(filepath.Join(extDir, "client-extension.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("failed to write client-extension.yaml: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to read cwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to chdir into the workspace fixture: %v", err)
	}
	t.Cleanup(func() {
		// Checked rather than discarded: a chdir that silently failed to restore would
		// leave every later test in this binary running from the temp workspace.
		if cerr := os.Chdir(cwd); cerr != nil {
			t.Errorf("failed to restore the working directory: %v", cerr)
		}
	})

	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()

	// An unset Ports list, exactly as DefaultClientConfig now produces it.
	mappings := resolvePortsAndMappings(&config.ClientConfig{})

	if len(mappings) != 2 {
		t.Fatalf("expected 8080 plus the client extension's port, got %+v", mappings)
	}
	if mappings[0].LocalPort != 8080 || mappings[0].NameSuffix != "" {
		t.Errorf("8080 must stay the primary mapping so existing public URLs do not move, got %+v", mappings[0])
	}
	if mappings[1].LocalPort != 4001 || mappings[1].NameSuffix != "my-remote-app" {
		t.Errorf("expected the client extension on 4001 suffixed \"my-remote-app\", got %+v", mappings[1])
	}
}

// TestResolvePortsAndMappings_UnsetReachesAutoDiscovery — outside a workspace, an unset
// port list must reach AutoDiscoverTarget.
func TestResolvePortsAndMappings_UnsetReachesAutoDiscovery(t *testing.T) {
	spies := stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { spies.workspaceChecked++; return false }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) {
		spies.workspaceScanned++
		return nil, nil
	}
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) {
		spies.autoDiscovered++
		return &client.AutoDiscoverResult{Host: "localhost", Ports: []int{8080, 3000}, Type: "Docker (LDM)"}, nil
	}

	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()

	mappings := resolvePortsAndMappings(&config.ClientConfig{})

	if spies.autoDiscovered != 1 {
		t.Fatalf("auto-discovery unreachable: AutoDiscoverTarget called %d times", spies.autoDiscovered)
	}
	if spies.workspaceScanned != 0 {
		t.Errorf("a non-workspace directory must not run the client-extension scan")
	}
	if len(mappings) != 2 || mappings[0].LocalPort != 8080 || mappings[1].LocalPort != 3000 {
		t.Fatalf("expected the discovered ports to be used, got %+v", mappings)
	}
	if mappings[1].NameSuffix != "3000" {
		t.Errorf("expected the secondary discovered port to be suffixed \"3000\", got %q", mappings[1].NameSuffix)
	}
}

// TestResolvePortsAndMappings_DefaultsTo8080WhenNothingIsDiscovered is the compatibility
// guarantee: the user who has no config file, passes no flag and is not in a workspace
// still ends up on 8080 whenever discovery turns up nothing.
func TestResolvePortsAndMappings_DefaultsTo8080WhenNothingIsDiscovered(t *testing.T) {
	cases := []struct {
		name       string
		result     *client.AutoDiscoverResult
		discoveErr error
	}{
		{name: "nothing found", result: nil},
		{name: "discovery failed", discoveErr: errDiscoveryForTest},
		{name: "result with no ports", result: &client.AutoDiscoverResult{Host: "localhost"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubPortDiscovery(t)
			isLiferayWorkspace = func(string) bool { return false }
			detectWorkspacePorts = func(string) ([]client.PortMapping, error) { return nil, nil }
			autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) { return tc.result, tc.discoveErr }

			oldPortsStr := *portsStr
			*portsStr = ""
			defer func() { *portsStr = oldPortsStr }()

			mappings := resolvePortsAndMappings(&config.ClientConfig{})

			if len(mappings) != 1 || mappings[0].LocalPort != 8080 || mappings[0].NameSuffix != "" {
				t.Fatalf("expected the unchanged 8080 default, got %+v", mappings)
			}
		})
	}
}

// TestResolvePortsAndMappings_DefaultsTo8080WhenTheWorkspaceScanFails — same guarantee on
// the workspace side.
func TestResolvePortsAndMappings_DefaultsTo8080WhenTheWorkspaceScanFails(t *testing.T) {
	stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { return true }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) { return nil, errDiscoveryForTest }
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) { return nil, nil }

	oldPortsStr := *portsStr
	*portsStr = ""
	defer func() { *portsStr = oldPortsStr }()

	mappings := resolvePortsAndMappings(&config.ClientConfig{})

	if len(mappings) != 1 || mappings[0].LocalPort != 8080 {
		t.Fatalf("expected the 8080 fallback after a failed scan, got %+v", mappings)
	}
}

var errDiscoveryForTest = errors.New("discovery unavailable")
