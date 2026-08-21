package main

import (
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	saveRegionCacheFn = func(bestRegion, serverURL string, _ bool) {
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
	saveRegionCacheFn = func(string, string, bool) {}

	cooldowns.exclude("apac", regionFailoverCooldown)

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
	saveRegionCacheFn = func(string, string, bool) {}

	cooldowns.exclude("eu", regionFailoverCooldown)

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

	cooldowns.exclude("apac", 1*time.Millisecond)
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

	cooldowns.exclude("APAC", regionFailoverCooldown)
	if _, present := cooldowns.filter(regions)["apac"]; present {
		t.Fatalf("exclude should be case-insensitive")
	}

	cooldowns.clear("apac")
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

	if got := regionDiscoveryURL(cfg, nil, "apac"); got != "https://central.example" {
		t.Errorf("expected the central control plane to be preferred, got %q", got)
	}

	// With central itself the failed region, any other region will do.
	cfg2 := &config.ClientConfig{Regions: map[string]string{"central": "https://central.example"}}
	if got := regionDiscoveryURL(cfg2, nil, "central"); got != "" {
		t.Errorf("expected no discovery URL when only the failed region is known, got %q", got)
	}

	// Falls back to the primary map when cfg has been emptied.
	cfg3 := &config.ClientConfig{}
	primary := map[string]string{"eu": "https://eu.example"}
	if got := regionDiscoveryURL(cfg3, primary, "apac"); got != "https://eu.example" {
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
	excludeFailedRegion(cfg, nil, "eu", true)
	if _, present := cooldowns.filter(cfg.Regions)["eu"]; !present {
		t.Error("the region returned to after a failed failback must stay a candidate; cooling it down abandons a healthy edge")
	}
	if cfg.ServerURL != "https://eu.example" {
		t.Errorf("discovery should not move off a region that did not fail, got %q", cfg.ServerURL)
	}

	// A genuine connection loss still cools the region down and moves discovery.
	resetCooldowns(t)
	cfg = newCfg()
	excludeFailedRegion(cfg, nil, "eu", false)
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
	cooldowns.exclude("apac", regionFailoverCooldown)
	excludeFailedRegion(cfg, nil, "eu", true)

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
	saveRegionCacheFn = func(string, string, bool) {}

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
	saveRegionCacheFn = func(string, string, bool) {}

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
	saveRegionCacheFn = func(string, string, bool) {}

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
