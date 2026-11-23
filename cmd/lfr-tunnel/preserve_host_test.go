package main

import (
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// #2560: preserve_host goes on for a detected Liferay port, unless someone set it explicitly.

func boolPtr(v bool) *bool { return &v }

func TestPlanPreserveHost_Precedence(t *testing.T) {
	cases := []struct {
		name        string
		explicit    *bool
		source      string
		liferayPort int
		wantAll     bool
		wantPorts   []int
		wantSummary string
	}{
		{name: "nothing set, nothing detected", wantSummary: "off (default"},
		{name: "nothing set, Liferay detected", liferayPort: 8080,
			wantPorts: []int{8080}, wantSummary: "on for 8080 (Liferay detected)"},
		{name: "discovered on a non-default host port", liferayPort: 8081,
			wantPorts: []int{8081}, wantSummary: "on for 8081 (Liferay detected)"},
		{name: "explicit flag off beats detection", explicit: boolPtr(false), source: "-preserve-host=false",
			liferayPort: 8080, wantSummary: "off (-preserve-host=false), although Liferay was detected on 8080"},
		{name: "explicit config off beats detection", explicit: boolPtr(false),
			source: "preserve_host: false in the config file", liferayPort: 8080,
			wantSummary: "off (preserve_host: false in the config file)"},
		{name: "explicit on covers every port", explicit: boolPtr(true), source: "-preserve-host",
			liferayPort: 8080, wantAll: true, wantSummary: "on for every port (-preserve-host)"},
		{name: "explicit on without Liferay", explicit: boolPtr(true), source: "-preserve-host",
			wantAll: true, wantSummary: "on for every port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planPreserveHost(tc.explicit, tc.source, tc.liferayPort)
			if plan.all != tc.wantAll {
				t.Errorf("all = %t, want %t", plan.all, tc.wantAll)
			}
			if !sameInts(plan.ports, tc.wantPorts) {
				t.Errorf("ports = %v, want %v", plan.ports, tc.wantPorts)
			}
			if !strings.HasPrefix(plan.summary, tc.wantSummary) {
				t.Errorf("summary = %q, want it to start %q", plan.summary, tc.wantSummary)
			}
		})
	}
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// -preserve-host=false used to override nothing (`if *preserveHost` only), so it could not
// switch off a value from the config file -- or, now, from detection.
func TestOverrideConfigWithFlags_PreserveHostFalseIsExplicit(t *testing.T) {
	resetFacts(t)
	oldGiven, oldVal := preserveHostFlagGiven, *preserveHost
	t.Cleanup(func() { preserveHostFlagGiven, *preserveHost = oldGiven, oldVal })

	preserveHostFlagGiven = func() bool { return true }
	*preserveHost = false

	cfg := &config.ClientConfig{PreserveHost: boolPtr(true)}
	overrideConfigWithFlags(cfg)

	if cfg.PreserveHost == nil || *cfg.PreserveHost {
		t.Fatalf("after -preserve-host=false, cfg.PreserveHost is not an explicit false (nil=%t)", cfg.PreserveHost == nil)
	}
	facts.liferayPort = 8080
	if got := preserveHostSummary(cfg); !strings.HasPrefix(got, "off (-preserve-host=false)") {
		t.Errorf("summary = %q, want the flag named as the reason it is off", got)
	}
}

// Not given at all leaves the config file's value, and an unset one, alone.
func TestOverrideConfigWithFlags_PreserveHostAbsentLeavesConfig(t *testing.T) {
	resetFacts(t)
	oldGiven := preserveHostFlagGiven
	t.Cleanup(func() { preserveHostFlagGiven = oldGiven })
	preserveHostFlagGiven = func() bool { return false }

	cfg := &config.ClientConfig{}
	overrideConfigWithFlags(cfg)
	if cfg.PreserveHost != nil {
		t.Errorf("an absent flag set preserve_host to %v; it must stay unset so detection can apply", *cfg.PreserveHost)
	}
}

// Which mapping is the Liferay port, per detection path.
func TestResolvePortsAndMappings_IdentifiesTheLiferayPort(t *testing.T) {
	cases := []struct {
		name      string
		workspace bool
		scan      []client.PortMapping
		discover  *client.AutoDiscoverResult
		want      int
	}{
		{
			name:      "workspace: the portal's 8080, not the client extension",
			workspace: true,
			scan:      []client.PortMapping{{LocalPort: 8080}, {LocalPort: 3000, NameSuffix: "my-ext"}},
			want:      8080,
		},
		{
			name:     "LDM container: the host port in front of the container's 8080",
			discover: &client.AutoDiscoverResult{Host: "localhost", Ports: []int{443, 8081}, Type: "Docker (LDM)", LiferayPort: 8081},
			want:     8081,
		},
		{
			name:     "native probe: an open port is not an identified Liferay",
			discover: &client.AutoDiscoverResult{Host: "localhost", Ports: []int{8080, 3000}, Type: "Native Service"},
			want:     0,
		},
		{
			name: "nothing discovered: the 8080 fallback is not an identified Liferay",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetFacts(t)
			stubPortDiscovery(t)
			isLiferayWorkspace = func(string) bool { return tc.workspace }
			detectWorkspacePorts = func(string) ([]client.PortMapping, error) { return tc.scan, nil }
			autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) { return tc.discover, nil }
			oldPortsStr := *portsStr
			*portsStr = ""
			t.Cleanup(func() { *portsStr = oldPortsStr })

			mustResolvePorts(t, &config.ClientConfig{})
			if facts.liferayPort != tc.want {
				t.Errorf("facts.liferayPort = %d, want %d", facts.liferayPort, tc.want)
			}
		})
	}
}

// Explicit ports are the user's choice, not a detection: nothing is switched on for them.
func TestResolvePortsAndMappings_ExplicitPortsIdentifyNoLiferayPort(t *testing.T) {
	resetFacts(t)
	stubPortDiscovery(t)
	facts.liferayPort = 9999 // stale value from an earlier resolution must not survive
	oldPortsStr := *portsStr
	*portsStr = ""
	t.Cleanup(func() { *portsStr = oldPortsStr })

	mustResolvePorts(t, &config.ClientConfig{Ports: []int{8080}})
	if facts.liferayPort != 0 {
		t.Errorf("facts.liferayPort = %d for explicit ports, want 0", facts.liferayPort)
	}
}

// End to end through the two consumers: the startup block names the reason, and the engine
// preserves the Host for the Liferay port alone.
func TestApplyPreserveHost_LiferayPortOnlyAndLogged(t *testing.T) {
	resetFacts(t)
	facts.liferayPort = 8080
	cfg := &config.ClientConfig{ServerURL: "https://eu.lfr-demo.se"}

	out := captureStartupLog(t, cfg, []client.PortMapping{{LocalPort: 8080}, {LocalPort: 3000}})
	if !strings.Contains(out, "preserve_host  on for 8080 (Liferay detected)") {
		t.Errorf("startup block does not explain preserve_host:\n%s", out)
	}

	engine := client.NewInterceptorEngine("127.0.0.1", nil)
	applyPreserveHost(engine, cfg)
	if engine.PreserveHost {
		t.Error("detection turned preserve_host on for every port; it must be the Liferay port only")
	}
	if !engine.PreservesHostFor(8080) || engine.PreservesHostFor(3000) {
		t.Errorf("PreservesHostFor(8080)=%t PreservesHostFor(3000)=%t, want true/false",
			engine.PreservesHostFor(8080), engine.PreservesHostFor(3000))
	}
	if !engine.IsLiferayPort(8080) {
		t.Error("the engine was not told which port is Liferay")
	}
}
