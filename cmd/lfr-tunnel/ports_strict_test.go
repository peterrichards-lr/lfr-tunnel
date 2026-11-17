package main

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// stubDiscoveryCounting stubs every discovery seam so that reaching any of them is counted,
// and so that the old fall-through, had it run, would have produced the 8080 mapping #2477
// reported rather than touching the real machine.
func stubDiscoveryCounting(t *testing.T) *portDiscoverySpies {
	t.Helper()
	spies := stubPortDiscovery(t)
	isLiferayWorkspace = func(string) bool { spies.workspaceChecked++; return false }
	detectWorkspacePorts = func(string) ([]client.PortMapping, error) {
		spies.workspaceScanned++
		return nil, nil
	}
	autoDiscoverTarget = func() (*client.AutoDiscoverResult, error) {
		spies.autoDiscovered++
		return nil, nil
	}
	return spies
}

func withPortsFlag(t *testing.T, val string) {
	t.Helper()
	old := *portsStr
	*portsStr = val
	t.Cleanup(func() { *portsStr = old })
}

// TestResolvePortsAndMappings_MalformedPortsFlagIsFatal is #2477. Each of these was
// reproduced publishing the app on 8080: the parts strconv.Atoi refused were dropped, an
// empty list fell through to cfg.Ports, the workspace scan, auto-discovery and finally
// defaultLocalPort. Now each is an error naming the value, and nothing downstream runs --
// not even when the config file has a perfectly good port list of its own, because -ports
// was given and that list is not what the user asked for.
func TestResolvePortsAndMappings_MalformedPortsFlagIsFatal(t *testing.T) {
	cases := []struct {
		value   string
		badPart string
	}{
		{"9999:80", "9999:80"},
		{"localhost:9999", "localhost:9999"},
		{"9999 8095", "9999 8095"},
		{"9999;8095", "9999;8095"},
		{"9999,x", "x"},
		{"0", "0"},
		{"70000", "70000"},
		{"9999,", ""},
		{" ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			spies := stubDiscoveryCounting(t)
			withPortsFlag(t, tc.value)

			mappings, err := resolvePortsAndMappings(&config.ClientConfig{Ports: []int{3000}})
			if err == nil {
				t.Fatalf("-ports %q resolved to %+v with no error; it must be refused, never "+
					"replaced by another port", tc.value, mappings)
			}
			if !strings.Contains(err.Error(), `"`+tc.badPart+`"`) || !strings.Contains(err.Error(), "-ports") {
				t.Errorf("error %q does not name -ports and the bad part %q", err, tc.badPart)
			}
			if mappings != nil {
				t.Errorf("an error came back with mappings %+v", mappings)
			}
			if *spies != (portDiscoverySpies{}) {
				t.Errorf("a refused -ports still reached discovery: %+v", *spies)
			}
		})
	}
}

// TestResolvePortsAndMappings_ConfigFilePortOutOfRangeIsFatal covers the third source of the
// list. YAML decoding already refuses a non-integer under `ports:`, but 0 and 70000 decode as
// ints and used to be published as-is.
func TestResolvePortsAndMappings_ConfigFilePortOutOfRangeIsFatal(t *testing.T) {
	t.Setenv("LFT_CLIENT_PORTS", "")
	for _, bad := range []int{0, -1, 65536, 70000} {
		spies := stubDiscoveryCounting(t)
		withPortsFlag(t, "")
		mappings, err := resolvePortsAndMappings(&config.ClientConfig{Ports: []int{9999, bad}})
		if err == nil {
			t.Errorf("config ports [9999 %d] resolved to %+v with no error", bad, mappings)
			continue
		}
		if !strings.Contains(err.Error(), `"`+strconv.Itoa(bad)+`"`) || !strings.Contains(err.Error(), "1-65535") {
			t.Errorf("error %q does not name the bad port %d and the accepted range", err, bad)
		}
		if *spies != (portDiscoverySpies{}) {
			t.Errorf("a refused config port list still reached discovery: %+v", *spies)
		}
	}
}

// TestResolvePortsAndMappings_ValidPortsFlagUnchanged pins the forms that worked before.
func TestResolvePortsAndMappings_ValidPortsFlagUnchanged(t *testing.T) {
	cases := []struct {
		value string
		want  []client.PortMapping
	}{
		{"9999", []client.PortMapping{{LocalPort: 9999}}},
		{"9999,8080", []client.PortMapping{{LocalPort: 9999}, {LocalPort: 8080, NameSuffix: "8080"}}},
		{" 9999 , 8080 ", []client.PortMapping{{LocalPort: 9999}, {LocalPort: 8080, NameSuffix: "8080"}}},
	}
	for _, tc := range cases {
		spies := stubDiscoveryCounting(t)
		withPortsFlag(t, tc.value)
		got, err := resolvePortsAndMappings(&config.ClientConfig{})
		if err != nil {
			t.Errorf("-ports %q was refused: %v", tc.value, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("-ports %q = %+v, want %+v", tc.value, got, tc.want)
		}
		if *spies != (portDiscoverySpies{}) {
			t.Errorf("-ports %q reached discovery: %+v", tc.value, *spies)
		}
	}
}

// TestNewClientEngine_CarriesResolvedPorts is #2478 from main's side: the engine main()
// builds must report the ports -ports resolved to, not NewInterceptorEngine's 8080. A test in
// pkg/client alone could not see main() forgetting to hand them over.
func TestNewClientEngine_CarriesResolvedPorts(t *testing.T) {
	stubDiscoveryCounting(t)
	withPortsFlag(t, "9999,8095")

	cfg := &config.ClientConfig{}
	mappings, err := resolvePortsAndMappings(cfg)
	if err != nil {
		t.Fatalf("resolvePortsAndMappings: %v", err)
	}
	engine := newClientEngine(cfg, mappings)

	if engine.DestPort != 9999 {
		t.Errorf("engine.DestPort = %d, want 9999 (the primary -ports value) -- the TUI, "+
			"/api/healthz and /api/info all read it", engine.DestPort)
	}
	targets, origin := engine.LocalTargets()
	want := []client.PortMapping{{LocalPort: 9999}, {LocalPort: 8095, NameSuffix: "8095"}}
	if !reflect.DeepEqual(targets, want) {
		t.Errorf("engine.LocalTargets() = %+v, want %+v", targets, want)
	}
	if origin != "from -ports" {
		t.Errorf("port origin = %q, want \"from -ports\"", origin)
	}

	// The interceptor rewrites the caller's slice in place afterwards (main.go, the
	// InterceptPort loop); the engine must have kept its own copy of the originals.
	mappings[0].LocalPort = 54321
	if targets, _ := engine.LocalTargets(); targets[0].LocalPort != 9999 {
		t.Errorf("rewriting the mappings after the engine was built changed its primary port to %d",
			targets[0].LocalPort)
	}
}

// TestResolvePortsAndMappings_RecordsOrigin pins the short origin the TUI shows per source.
func TestResolvePortsAndMappings_RecordsOrigin(t *testing.T) {
	t.Run("config file", func(t *testing.T) {
		t.Setenv("LFT_CLIENT_PORTS", "")
		stubDiscoveryCounting(t)
		withPortsFlag(t, "")
		if _, err := resolvePortsAndMappings(&config.ClientConfig{Ports: []int{3000}}); err != nil {
			t.Fatal(err)
		}
		if facts.portOrigin != "from config file" {
			t.Errorf("origin = %q, want \"from config file\"", facts.portOrigin)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv("LFT_CLIENT_PORTS", "3000")
		stubDiscoveryCounting(t)
		withPortsFlag(t, "")
		if _, err := resolvePortsAndMappings(&config.ClientConfig{Ports: []int{3000}}); err != nil {
			t.Fatal(err)
		}
		if facts.portOrigin != "from LFT_CLIENT_PORTS" {
			t.Errorf("origin = %q, want \"from LFT_CLIENT_PORTS\"", facts.portOrigin)
		}
	})
	t.Run("auto-discovered", func(t *testing.T) {
		stubDiscoveryReturning(t, "localhost")
		if _, err := resolvePortsAndMappings(&config.ClientConfig{}); err != nil {
			t.Fatal(err)
		}
		if facts.portOrigin != "auto-discovered" {
			t.Errorf("origin = %q, want \"auto-discovered\"", facts.portOrigin)
		}
	})
}

// mustResolvePorts is resolvePortsAndMappings for the tests whose inputs are all valid.
func mustResolvePorts(t *testing.T, cfg *config.ClientConfig) []client.PortMapping {
	t.Helper()
	mappings, err := resolvePortsAndMappings(cfg)
	if err != nil {
		t.Fatalf("resolvePortsAndMappings: %v", err)
	}
	return mappings
}

// Review of #2484: `-ports ""` is given-but-empty, an error -- not "not given", which fell through
// to discovery for a wrapper running `-ports "$PORTS"` with PORTS unset.
func TestValidateRequestedPorts_ExplicitlyEmptyFlagIsRefused(t *testing.T) {
	withPortsFlag(t, "")
	old := portsFlagGiven
	portsFlagGiven = func() bool { return true }
	t.Cleanup(func() { portsFlagGiven = old })

	if err := validateRequestedPorts(&config.ClientConfig{}); err == nil {
		t.Fatal(`-ports "" was accepted; want an error, not a fall-through to discovery`)
	}
}

// Review of #2484: a malformed LFT_CLIENT_PORTS is reported when a tunnel starts -- including
// before -background hands off -- but an explicit -ports outranks it, and it no longer fails the
// whole config load (so -stop and -status keep working).
func TestValidateRequestedPorts_EnvErrorSurfacesOnlyWithoutAFlag(t *testing.T) {
	envErr := errors.New(`invalid port "9999:80" in LFT_CLIENT_PORTS`)
	cfg := &config.ClientConfig{PortsEnvErr: envErr}

	withPortsFlag(t, "")
	if err := validateRequestedPorts(cfg); err == nil || !strings.Contains(err.Error(), "LFT_CLIENT_PORTS") {
		t.Errorf("no -ports, malformed LFT_CLIENT_PORTS: got %v, want the env error", err)
	}

	withPortsFlag(t, "9999")
	if err := validateRequestedPorts(cfg); err != nil {
		t.Errorf("a valid -ports must outrank a malformed LFT_CLIENT_PORTS, got %v", err)
	}
}
