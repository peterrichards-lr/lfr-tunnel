package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
)

// #2595: a tunnel started with -domain wrote its state as `lfr-tunnel-.state`, so
// `-status-json -subdomain <sub>` reported {"running": false} for a connected tunnel and LDM's
// health check stopped it.
func TestTunnelStateNameKeepsTheRequestedSubdomainForACustomDomain(t *testing.T) {
	cases := []struct {
		name, requested, customDomain, assigned, want string
	}{
		{"custom domain", "ldm-proj", "dev.example.com", "", "ldm-proj"},
		{"custom domain ignores a prefix", "ldm-proj", "dev.example.com", "peter-ldm-proj", "ldm-proj"},
		{"assigned prefix", "ldm-proj", "", "peter-ldm-proj", "peter-ldm-proj"},
		{"plain subdomain", "ldm-proj", "", "", "ldm-proj"},
	}
	for _, c := range cases {
		if got := tunnelStateName(c.requested, c.customDomain, c.assigned); got != c.want {
			t.Errorf("%s: tunnelStateName(%q, %q, %q) = %q, want %q", c.name, c.requested, c.customDomain, c.assigned, got, c.want)
		}
	}
}

// The round trip LDM depends on: the state a custom-domain tunnel writes is the one
// `-status-json -subdomain <the subdomain it was started with>` reads, and it reports running.
func TestCustomDomainTunnelIsFoundByStatusJSONForItsSubdomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	const sub = "ldm-proj"
	name := tunnelStateName(sub, "dev.example.com", "")
	state := &client.ClientState{
		PID:          os.Getpid(),
		InspectorURL: "http://127.0.0.1:1", // nothing listens; status stays "starting"
		Subdomain:    name,
		PublicURLs:   []string{"https://dev.example.com"},
	}
	if err := client.WriteState(name, state); err != nil {
		t.Fatal(err)
	}

	path, err := client.GetStateFilePath(sub) // what handleStatusJSON(sub, true) reads
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.QueryStatusJSON(path, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var out client.StatusOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Running || out.Subdomain != sub {
		t.Fatalf("status for -subdomain %s = %s; want running with subdomain %q", sub, raw, sub)
	}
}

// The name has to reach every write and the delete; one left on subHost brings the bug back.
func TestMainKeysTheStateFileOnTheStateName(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"WriteState(subHost", "DeleteState(subHost", "Subdomain:     subHost"} {
		if strings.Contains(string(src), bad) {
			t.Errorf("main.go still has %q: a custom-domain tunnel's state would be keyed on \"\" (#2595)", bad)
		}
	}
	if n := strings.Count(string(src), "WriteState(stateName, state)"); n != 2 {
		t.Errorf("main.go writes the state under stateName %d times, want 2 (start and re-registration)", n)
	}
}
