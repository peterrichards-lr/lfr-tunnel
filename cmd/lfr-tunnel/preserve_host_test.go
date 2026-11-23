package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// #2560: preserve_host goes on for a detected Liferay port, unless someone set it explicitly.

// The full precedence matrix, through the real loader: preserve_host in the file (absent,
// false, true) x preserve_host_auto (unset, false) x -preserve-host (absent, on, off), with
// Liferay detected on 8080. The decision recorded for #2560:
//
//	-preserve-host            -> every port
//	-preserve-host=false      -> no port (no every-port setting, no detection)
//	preserve_host: true       -> every port
//	preserve_host_auto: false -> no port
//	otherwise                 -> 8080 only; preserve_host: false in the file is "not set"
func TestPreserveHostPrecedenceMatrix(t *testing.T) {
	files := map[string]string{"absent": "", "false": "preserve_host: false\n", "true": "preserve_host: true\n"}
	autos := map[string]string{"unset": "", "false": "preserve_host_auto: false\n"}
	flags := []string{"", sourceFlagOn, sourceFlagOff}

	for fileName, fileYAML := range files {
		for autoName, autoYAML := range autos {
			for _, flagSource := range flags {
				name := "file=" + fileName + "/auto=" + autoName + "/flag=" + flagSource
				t.Run(name, func(t *testing.T) {
					resetFacts(t)
					path := filepath.Join(t.TempDir(), "config.yaml")
					if err := os.WriteFile(path, []byte("subdomain: demo\n"+fileYAML+autoYAML), 0o600); err != nil {
						t.Fatal(err)
					}
					cfg, err := config.LoadClientConfig(path)
					if err != nil {
						t.Fatalf("LoadClientConfig: %v", err)
					}
					facts.liferayPort = 8080
					facts.preserveHostSource = flagSource

					wantAll, wantPorts, wantReason := false, []int{8080}, "Liferay detected"
					switch {
					case flagSource == sourceFlagOn:
						wantAll, wantPorts, wantReason = true, nil, sourceFlagOn
					case flagSource == sourceFlagOff:
						wantPorts, wantReason = nil, sourceFlagOff
					case fileName == "true":
						wantAll, wantPorts, wantReason = true, nil, sourceFileOn
					case autoName == "false":
						wantPorts, wantReason = nil, sourceFileAutoDisabled
					}

					plan := currentPreserveHostPlan(cfg)
					if plan.all != wantAll || !sameInts(plan.ports, wantPorts) {
						t.Errorf("all=%t ports=%v, want all=%t ports=%v (%s)", plan.all, plan.ports, wantAll, wantPorts, plan.summary)
					}
					if !strings.Contains(plan.summary, wantReason) {
						t.Errorf("summary %q does not name %q as the reason", plan.summary, wantReason)
					}
				})
			}
		}
	}
}

func TestPlanPreserveHost_WithoutLiferay(t *testing.T) {
	if p := planPreserveHost(preserveHostInputs{}, 0); p.all || len(p.ports) != 0 || !strings.HasPrefix(p.summary, "off (default") {
		t.Errorf("nothing set, nothing detected: %+v", p)
	}
	if p := planPreserveHost(preserveHostInputs{}, 8081); !sameInts(p.ports, []int{8081}) {
		t.Errorf("discovered on 8081: %+v", p)
	}
	if p := planPreserveHost(preserveHostInputs{autoDisable: true}, 0); p.summary != "off ("+sourceFileAutoDisabled+")" {
		t.Errorf("auto off without Liferay: %q", p.summary)
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

// -preserve-host=false used to override nothing (`if *preserveHost` only). It now switches off
// both the every-port setting from the file and detection, for this run only -- and leaves the
// config untouched, so a save cannot carry the flag into the file.
func TestOverrideConfigWithFlags_PreserveHostFalseIsExplicit(t *testing.T) {
	resetFacts(t)
	oldGiven, oldVal := preserveHostFlagGiven, *preserveHost
	t.Cleanup(func() { preserveHostFlagGiven, *preserveHost = oldGiven, oldVal })

	preserveHostFlagGiven = func() bool { return true }
	*preserveHost = false

	on := true
	cfg := &config.ClientConfig{PreserveHost: &on}
	overrideConfigWithFlags(cfg)

	if cfg.PreserveHost == nil || !*cfg.PreserveHost || cfg.PreserveHostAuto != nil {
		t.Errorf("the flag was written into the config struct")
	}
	facts.liferayPort = 8080
	plan := currentPreserveHostPlan(cfg)
	if plan.all || len(plan.ports) != 0 || !strings.HasPrefix(plan.summary, "off (-preserve-host=false)") {
		t.Errorf("after -preserve-host=false: %+v, want off everywhere with the flag named", plan)
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
