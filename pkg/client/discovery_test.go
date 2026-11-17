package client

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type mockExecutor struct {
	output []byte
	err    error
}

func (m mockExecutor) Output(command string, args ...string) ([]byte, error) {
	return m.output, m.err
}

// The fixtures are `docker ps --format` lines: name||image||ports||com.liferay.ldm.project label.
//
// ldmProjectLines is what LDM actually runs for one project (#2462 review), taken from an
// LDM-generated docker-compose.yml: the portal bound to LOOPBACK as "127.0.0.1:<port>:8080", plus
// its database, its tunnel sidecar and a client extension, all labelled with the project. The
// first version of this fix was tested against a 0.0.0.0 portal LDM never produces, and passed
// while the real portal stayed invisible.
func ldmProjectLines(project, hostPort string) string {
	return strings.Join([]string{
		project + "||liferay/dxp:2025.q1.0-lts||127.0.0.1:" + hostPort + "->8080/tcp||" + project,
		project + "-db||postgres:16||5432/tcp||" + project,
		project + "-lfr-tunnel||peterrichards/lfr-tunnel:1.52.7||0.0.0.0:4040->4040/tcp||" + project,
		project + "-myext||" + project + "-myext:latest||0.0.0.0:3000->3000/tcp||" + project,
	}, "\n")
}

// ldmSharedLines are LDM's machine-wide containers, which serve every project.
const ldmSharedLines = `liferay-proxy-global||traefik:v3.1||0.0.0.0:80->80/tcp, [::]:80->80/tcp, 0.0.0.0:443->443/tcp, [::]:443->443/tcp||
liferay-search-global||elasticsearch:8.17.0||127.0.0.1:9200->9200/tcp||`

func TestDiscoverDocker(t *testing.T) {
	// Save the original executor and restore it after the test
	originalExecutor := cmdExecutor
	defer func() { cmdExecutor = originalExecutor }()

	tests := []struct {
		name           string
		mockOutput     string
		mockError      error
		expectedResult *AutoDiscoverResult
		expectedError  bool
	}{
		{
			name:           "Docker command fails",
			mockOutput:     "",
			mockError:      errors.New("executable file not found in $PATH"),
			expectedResult: nil,
			expectedError:  true,
		},
		{
			name: "No Liferay or LDM containers",
			mockOutput: `postgres_db||postgres:14||0.0.0.0:5432->5432/tcp||
redis_cache||redis:alpine||0.0.0.0:6379->6379/tcp||`,
			mockError:      nil,
			expectedResult: nil,
			expectedError:  false,
		},
		{
			name: "LDM container running with typical port",
			mockOutput: `ldm-project-xyz||peterrichards/ldm-runtime||0.0.0.0:8080->8080/tcp||
postgres_db||postgres:14||0.0.0.0:5432->5432/tcp||`,
			mockError: nil,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (LDM)",
				Container: "ldm-project-xyz",
			},
			expectedError: false,
		},
		{
			name:       "Native Liferay DXP container running",
			mockOutput: `liferay-dxp-custom||liferay/dxp:7.4||0.0.0.0:443->443/tcp, 0.0.0.0:8080->8080/tcp||`,
			mockError:  nil,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{443, 8080},
				Type:      "Docker (Liferay)",
				Container: "liferay-dxp-custom",
			},
			expectedError: false,
		},
		{
			// The real LDM shape (#2462). Before: the loopback-bound portal was invisible and the
			// global proxy's 80/443 were published. Now: the portal alone, named -- not its
			// database, sidecar or extension, and not the shared proxy or search.
			name:       "an LDM project on a real LDM machine",
			mockOutput: ldmProjectLines("acme", "8080") + "\n" + ldmSharedLines,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (LDM)",
				Container: "acme",
			},
		},
		{
			// An LDM project needs no "liferay" in its name: the label identifies it.
			name:       "an LDM project on a non-standard host port",
			mockOutput: ldmProjectLines("shop", "8081"),
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8081},
				Type:      "Docker (LDM)",
				Container: "shop",
			},
		},
		{
			name:           "only LDM's shared containers are running",
			mockOutput:     ldmSharedLines,
			expectedResult: nil,
		},
		{
			name:       "a port Docker lists for IPv4 and IPv6 (every notation) is published once",
			mockOutput: `ldm-project-xyz||peterrichards/ldm-runtime||0.0.0.0:8080->8080/tcp, [::]:8080->8080/tcp, :::8080->8080/tcp, 127.0.0.1:8080->8080/tcp, [::1]:8080->8080/tcp||`,
			expectedResult: &AutoDiscoverResult{
				Host:      "localhost",
				Ports:     []int{8080},
				Type:      "Docker (LDM)",
				Container: "ldm-project-xyz",
			},
		},
		{
			// A port bound to another interface is not reachable on localhost, and the old
			// unanchored pattern read 10.0.0.0 as 0.0.0.0.
			name:           "a port bound to another address is not a candidate",
			mockOutput:     `liferay-dxp-custom||liferay/dxp:7.4||10.0.0.0:8080->8080/tcp||`,
			expectedResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdExecutor = mockExecutor{
				output: []byte(tt.mockOutput),
				err:    tt.mockError,
			}

			result, err := discoverDocker()
			if (err != nil) != tt.expectedError {
				t.Errorf("discoverDocker() error = %v, expectedError %v", err, tt.expectedError)
				return
			}
			if !reflect.DeepEqual(result, tt.expectedResult) {
				t.Errorf("discoverDocker() result = %v, expected %v", result, tt.expectedResult)
			}
		})
	}
}

// Two projects on one machine (#2462): publishing both shared one the user never meant to, and
// choosing one would be a guess. Discovery refuses, names both, and does not fall back to probing
// 8080 -- which is very likely one of them. LDM gives the second project 8081.
func TestDiscoveryRefusesWhenSeveralContainersQualify(t *testing.T) {
	originalExecutor := cmdExecutor
	defer func() { cmdExecutor = originalExecutor }()
	cmdExecutor = mockExecutor{output: []byte(ldmProjectLines("acme", "8080") + "\n" +
		ldmProjectLines("other-project", "8081") + "\n" + ldmSharedLines)}

	result, err := AutoDiscoverTarget()
	var ambiguous *AmbiguousDiscoveryError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("AutoDiscoverTarget() = %+v, %v; want an AmbiguousDiscoveryError", result, err)
	}
	if result != nil {
		t.Errorf("a refusal must publish nothing, got %+v", result)
	}
	want := []DiscoveredContainer{{Name: "acme", Ports: []int{8080}}, {Name: "other-project", Ports: []int{8081}}}
	if !reflect.DeepEqual(ambiguous.Candidates, want) {
		t.Errorf("candidates = %+v, want %+v", ambiguous.Candidates, want)
	}
	for _, s := range []string{"acme on 8080", "other-project on 8081", "-ports"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("the refusal %q does not mention %q", err.Error(), s)
		}
	}
}

func TestAutoDiscoverTarget(t *testing.T) {
	// We leave the integration/fallback test as-is for local state.
	result, err := AutoDiscoverTarget()
	if err != nil {
		t.Logf("Expected no error, got %v", err)
	}

	if result != nil {
		t.Logf("Discovered something: %+v", result)
	} else {
		t.Log("Nothing discovered, as expected in CI")
	}
}
