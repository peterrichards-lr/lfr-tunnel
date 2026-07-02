package main

import (
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
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
	path, _ := getPIDFilePath(sub)
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
	_ = probeFastestRegion(regions)
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
	cfg := &config.ClientConfig{
		Ports: []int{8080},
	}

	mappings := resolvePortsAndMappings(cfg)
	if len(mappings) != 1 {
		t.Errorf("Expected 1 mapping")
	}
}

func TestMain_ValidationFailure(t *testing.T) {
	if os.Getenv("BE_CRASHER_VALIDATION") == "1" {
		os.Args = []string{"cmd", "-ports", "invalid"}
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
