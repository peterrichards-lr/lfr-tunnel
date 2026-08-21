package main

import (
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
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
	_ = probeFastestRegion(regions) //nolint:errcheck
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
	saveRegionCacheFn = func(bestRegion, serverURL string) {
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
